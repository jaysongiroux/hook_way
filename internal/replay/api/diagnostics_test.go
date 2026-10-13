package api

import (
	"bytes"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/prometheus/client_golang/prometheus/testutil"

	"github.com/jaysongiroux/hook_way/internal/replay/store"
)

func TestBackendAndPanicDiagnosticsWithholdSecrets(t *testing.T) {
	for _, panicValue := range []bool{false, true} {
		var logs bytes.Buffer
		fs := notFoundStore()
		backend := errors.New("postgres://user:URLSECRET@database.example.test/private/SECRET?token=QUERYSECRET password=DSNSECRET") // #nosec G101 -- synthetic redaction markers, never real credentials.
		fs.ready = func() error { return backend }
		fs.get = func(string) (store.RecordDetail, error) {
			if panicValue {
				panic("PAYLOADSECRET")
			}
			return store.RecordDetail{}, backend
		}
		h := newHarness(t, fs, func(o *Options) { o.Logger = slog.New(slog.NewJSONHandler(&logs, nil)) })
		ready := h.do(t, "GET", "/readyz", "", nil, nil)
		response := h.do(t, "GET", "/v1/records/"+testID, readToken, nil, nil)
		if ready.Code != http.StatusServiceUnavailable || response.Code != http.StatusInternalServerError {
			t.Fatal("diagnostic sanitization changed status contract")
		}
		all := logs.String() + ready.Body.String() + response.Body.String()
		for _, marker := range []string{"URLSECRET", "QUERYSECRET", "DSNSECRET", "PAYLOADSECRET", "/private/SECRET"} {
			if strings.Contains(all, marker) {
				t.Fatalf("diagnostic leaked%s", marker)
			}
		}
		if !strings.Contains(logs.String(), "database.example.test") || (panicValue && !strings.Contains(logs.String(), "panic_type")) {
			t.Fatal("diagnostic lost backend origin or panic type")
		}
	}
}

func TestStoreFailureReasonSeparatesDiskFullFromRetentionCapacity(t *testing.T) {
	for _, tc := range []struct {
		err    error
		reason string
	}{{fmt.Errorf("insert: %w", &pgconn.PgError{Code: "53100", Message: "could not extend file"}), "storage_exhausted"},
		{errors.New("dial refused"), "other"}, {&pgconn.PgError{Code: "53300", Message: "too many connections"}, "other"}} {
		fs := notFoundStore()
		fs.get = func(string) (store.RecordDetail, error) { return store.RecordDetail{}, tc.err }
		h := newHarness(t, fs)
		if response := h.do(t, "GET", "/v1/records/"+testID, readToken, nil, nil); response.Code != http.StatusInternalServerError {
			t.Fatal("unexpected store failure must remain generic500")
		}
		want := "\n# HELP hookway_replay_store_failures_total Unexpected API or readiness store failures, by bounded reason (storage_exhausted or other).\n# TYPE hookway_replay_store_failures_total counter\n"
		for _, reason := range []string{"other", "storage_exhausted"} {
			count := 0
			if reason == tc.reason {
				count = 1
			}
			want += fmt.Sprintf("hookway_replay_store_failures_total{reason=%q} %d\n", reason, count)
		}
		if err := testutil.GatherAndCompare(h.metrics.Registry(), strings.NewReader(want), "hookway_replay_store_failures_total"); err != nil {
			t.Fatal(err)
		}
	}
}
