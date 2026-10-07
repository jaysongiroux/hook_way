package acceptance

import (
	"context"
	"github.com/jaysongiroux/hook_way/internal/replay/replaytest"
	"net/http"
	"os"
	"os/exec"
	"strings"

	"gopkg.in/yaml.v3"
	"testing"
	"time"
)

// Exercise the coordinated stop/migrate/start procedure with one build. This
// proves retained records survive replacement; it cannot establish compatibility
// with an older wire contract or a database schema not present in this checkout.
func TestAcceptance_CoordinatedReplacementPreservesReplay(t *testing.T) {
	testReplacement(t, false, false)
}

func TestAcceptance_BackupRestorePreservesReplay(t *testing.T) {
	testReplacement(t, true, false)
}

func testReplacement(t *testing.T, restore, protected bool) {
	t.Helper()
	var s *system
	if protected {
		s = startSystemWithProtection(t, "postgres", "")
	} else {
		s = startSystem(t)
	}
	s.audit.failing.Store(true)
	post := func() int { return s.post(t, []byte(`{"order":"replacement"}`)) }
	if protected {
		post = func() int {
			code, err := signedRequest(s.coreURL+"/protected", "restore-event", []byte(`{"order":"replacement"}`))
			if err != nil {
				t.Fatal(err)
			}
			return code
		}
		for range 2 {
			if code, err := signedRequest(s.coreURL+"/quota", "", []byte(`{}`)); err != nil || code != 202 {
				t.Fatal(code, err)
			}
		}
	}
	if code := post(); code != http.StatusAccepted {
		t.Fatalf("ingest status %d", code)
	}
	var open struct {
		Items []failureItem `json:"items"`
	}
	waitFor(t, "captured failure before replacement", 30*time.Second, func() bool {
		open.Items = nil
		return s.api(t, http.MethodGet, "/v1/records?kind=failure&delivery_status=open&dismissed=false&node_id=audit", nil, &open) == http.StatusOK && len(open.Items) == 1
	})
	failure := open.Items[0]
	// No additional ingress or operator retries: capture has reached PostgreSQL,
	// synchronous publishes are complete, and no replay job has been queued.
	for _, p := range append(append([]*process{}, s.replays...), s.cores...) {
		p.stop()
		if !p.cmd.ProcessState.Success() {
			t.Fatalf("%s did not shut down cleanly: %s", p.name, p.out.String())
		}
	}
	if restore {
		var source string
		for _, env := range s.replays[0].cmd.Env {
			if value, ok := strings.CutPrefix(env, "REPLAY_DATABASE_URL="); ok {
				source = value
			}
		}
		restored := replaytest.BackupRestore(t, source)
		for _, p := range append(append([]*process{}, s.replays...), s.cores...) {
			for i, env := range p.cmd.Env {
				if strings.HasPrefix(env, "REPLAY_DATABASE_URL=") {
					p.cmd.Env[i] = "REPLAY_DATABASE_URL=" + restored
				}
			}
		}
	}
	replay := s.replays[0]
	migration := exec.CommandContext(context.Background(), replay.cmd.Path, "migrate", "--config", replay.cmd.Args[len(replay.cmd.Args)-1]) // #nosec G204 -- same repository-built binary/config as startSystem
	migration.Env = replay.cmd.Env
	if out, err := migration.CombinedOutput(); err != nil {
		t.Fatalf("migration after quiescence: %v\n%s", err, out)
	}
	for i, p := range s.cores {
		s.cores[i] = startProcess(t, p.name+"-replacement", p.cmd.Path, p.cmd.Env, p.cmd.Args[1:]...)
	}
	for _, u := range s.coreReady {
		waitReady(t, "replacement core", u)
	}
	for i, p := range s.replays {
		s.replays[i] = startProcess(t, p.name+"-replacement", p.cmd.Path, p.cmd.Env, p.cmd.Args[1:]...)
		data, err := os.ReadFile(p.cmd.Args[len(p.cmd.Args)-1])
		if err != nil {
			t.Fatal(err)
		}
		var cfg struct {
			Server struct {
				Addr string `yaml:"addr"`
			} `yaml:"server"`
		}
		if err := yaml.Unmarshal(data, &cfg); err != nil {
			t.Fatal(err)
		}
		waitReady(t, p.name+" replacement", "http://"+cfg.Server.Addr+"/readyz")
	}
	waitReady(t, "replacement replay", s.replayURL+"/readyz")
	if protected {
		if code := post(); code != 409 {
			t.Fatalf("restored guard admitted duplicate%d", code)
		}
		admitted := 0
		for range 10 {
			code, err := signedRequest(s.coreURL+"/quota", "", []byte(`{}`))
			if err != nil {
				t.Fatal(err)
			}
			if code == 202 {
				admitted++
			} else if code != 429 {
				t.Fatal(code)
			}
		}
		if admitted != 8 {
			t.Fatalf("backup/restore reset quota: remaining%d want8", admitted)
		}
	}

	s.audit.failing.Store(false)
	var batch struct {
		Results []struct {
			Status int    `json:"status"`
			RunID  string `json:"run_id"`
		} `json:"results"`
	}
	if code := s.api(t, http.MethodPost, "/v1/retries", map[string]any{"record_ids": []string{failure.ID}}, &batch, "Idempotency-Key", "replacement-retry"); code != http.StatusOK || len(batch.Results) != 1 || batch.Results[0].Status != http.StatusAccepted {
		t.Fatalf("retry status %d result %+v", code, batch)
	}
	waitFor(t, "replacement retry success", 60*time.Second, func() bool {
		var run struct {
			Status string `json:"status"`
		}
		s.api(t, http.MethodGet, "/v1/retries/"+batch.Results[0].RunID, nil, &run)
		if run.Status == "unknown" || run.Status == "failed" || run.Status == "blocked" {
			t.Fatalf("retry finished %s", run.Status)
		}
		return run.Status == "succeeded"
	})
	for name, receipts := range map[string]map[string]int{"orders": s.orders.counts(), "audit": s.audit.counts()} {
		if len(receipts) != 1 || receipts[failure.EventID] != 1 {
			t.Fatalf("%s receipts after replacement: %v", name, receipts)
		}
	}
}

// One current-version archive retains both replay evidence and the separate
// protection schema. This does not prove off-host/HA recovery or old-version rollback.
func TestAcceptance_ProtectionBackupRestorePreservesReservations(t *testing.T) {
	testReplacement(t, true, true)
}
