package composee2e

import (
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/jaysongiroux/hook_way/internal/replaywire"
)

// Reuse the already running stack after proving ordinary failure replay. Only
// this isolated project's core mount changes; the repository example stays intact.
func (s *stack) checkInspections(t *testing.T) {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(s.root, "examples", "inspect.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	src := strings.NewReplacer(":8083", ":8080", ":9093", ":9092", "http://localhost:8081", "http://replay:8080",
		"amqp://guest:guest@localhost:5672/", "amqp://guest:guest@rabbitmq:5672/").Replace(string(raw))
	src = strings.Replace(src, "replay:\n", "metrics: {enabled: true, addr: ':9090', path: /metrics}\nreplay:\n  max_record_bytes: 2048\n", 1)
	preamble, flow, ok := strings.Cut(src, "flows:\n")
	if !ok {
		t.Fatal("inspection example has no flows")
	}
	zero := strings.NewReplacer("inspected_events", "inspect_zero", "/webhooks/inspect", "/inspect/zero", "rate: 0.05", "rate: 0").Replace(flow)
	one := strings.NewReplacer("inspected_events", "inspect_one", "/webhooks/inspect", "/inspect/one", "rate: 0.05", "rate: 1").Replace(flow)
	dir := t.TempDir()
	config := filepath.Join(dir, "core.yaml")
	// The path combines t.TempDir with a constant basename, never external input.
	// #nosec G306 G703 -- Synthetic dev tokens; bind-mounted config must be readable by container UID 65532.
	if err := os.WriteFile(config, []byte(preamble+"flows:\n"+zero+one), 0o644); err != nil {
		t.Fatal(err)
	}
	override := filepath.Join(dir, "inspect.yaml")
	if err := os.WriteFile(override, []byte(fmt.Sprintf("services:\n  hookway:\n    volumes: !override\n      - %s:/etc/hookway/config.yaml:ro\n    ports: !override\n      - '127.0.0.1::8080'\n      - '127.0.0.1::9090'\n", config)), 0o600); err != nil {
		t.Fatal(err)
	}
	previousArgs := s.args
	defer func() { s.args = previousArgs }()
	s.args = append(s.args, "--file", override)
	s.compose(t, "up", "--detach", "--no-build", "hookway")
	waitFor(t, "inspection core readiness", time.Minute, func() bool {
		code, _, err := do(http.MethodGet, s.url(t, "hookway", 8080)+"/readyz", nil)
		return err == nil && code == http.StatusOK
	})
	post := func(path, body string) {
		t.Helper()
		code, raw, err := do(http.MethodPost, s.url(t, "hookway", 8080)+path, []byte(body), "Content-Type", "application/json",
			"Authorization", "Bearer synthetic-secret", "X-Request-ID", "inspect-compose")
		if err != nil || code != http.StatusAccepted {
			t.Fatalf("inspection webhook: status=%d error=%v body=%s", code, err, raw)
		}
	}
	for i := range 10 {
		body := fmt.Sprintf(`{"sequence":%d,"large_number":9007199254740993}`, i)
		post("/inspect/zero", body)
		post("/inspect/one", body)
	}
	var list struct {
		Items []struct {
			failure
			Kind                     string    `json:"kind"`
			OccurredAt               time.Time `json:"occurred_at"`
			NotRetryableReason       string    `json:"not_retryable_reason"`
			PayloadUnavailableReason string    `json:"payload_unavailable_reason"`
		} `json:"items"`
	}
	waitFor(t, "ten view-only records", time.Minute, func() bool {
		code, _ := s.api(t, http.MethodGet, "/v1/records?kind=inspection", nil, &list)
		return code == http.StatusOK && len(list.Items) == 10
	})
	for _, record := range list.Items {
		if record.Flow != "inspect_one" || record.Kind != "inspection" || record.NodeID != "inspect_sample" || record.OccurredAt.IsZero() || record.Retryable || record.NotRetryableReason != "inspection_only" {
			t.Fatalf("inspection metadata = %+v", record)
		}
	}
	for _, queue := range []string{"inspect_zero", "inspect_one"} {
		if got := s.rabbitMessages(t, queue); len(got) != 10 {
			t.Fatalf("%s received %d of ten webhooks", queue, len(got))
		}
	}
	id := list.Items[0].ID
	var payload replaywire.InspectionPayload
	if code, raw := s.api(t, http.MethodGet, "/v1/records/"+id+"/payload", nil, &payload); code != 200 || payload.Version != 1 || !strings.Contains(payload.Body.Content, "9007199254740993") || strings.Contains(string(raw), "synthetic-secret") || payload.Method != "POST" || payload.Path != "/inspect/one" {
		t.Fatalf("view-only payload: status=%d body=%s", code, raw)
	}
	var identity struct {
		Capabilities []string `json:"capabilities"`
	}
	s.api(t, http.MethodGet, "/v1/me", nil, &identity)
	if !slices.Contains(identity.Capabilities, "inspection_records") {
		t.Fatal("deployed API did not advertise inspections")
	}
	if code, raw := s.api(t, http.MethodPost, "/v1/records/"+id+"/retries", map[string]any{}, nil, "Idempotency-Key", "inspect-compose-single"); code != 422 {
		t.Fatalf("inspection retry = %d %s", code, raw)
	}
	var batch struct {
		Results []struct {
			Status int `json:"status"`
		} `json:"results"`
	}
	if code, raw := s.api(t, http.MethodPost, "/v1/retries", map[string]any{"record_ids": []string{id}}, &batch, "Idempotency-Key", "inspect-compose-batch"); code != 200 || len(batch.Results) != 1 || batch.Results[0].Status != 422 {
		t.Fatalf("inspection batch retry = %d %s", code, raw)
	}
	if code, raw := s.api(t, http.MethodPost, "/v1/records/"+id+"/resolve", map[string]any{"note": "Synthetic local inspection"}, nil, "Idempotency-Key", "inspect-compose-resolve"); code != 422 {
		t.Fatalf("inspection resolution = %d %s", code, raw)
	}
	var defaults struct {
		Items []failure `json:"items"`
	}
	s.api(t, http.MethodGet, "/v1/records?kind=failure", nil, &defaults)
	if len(defaults.Items) != 1 || defaults.Items[0].DeliveryStatus != "resolved" {
		t.Fatalf("failure-filtered list changed: %+v", defaults.Items)
	}

	var mixed struct {
		Items []struct {
			Kind string `json:"kind"`
		} `json:"items"`
	}
	s.api(t, http.MethodGet, "/v1/records", nil, &mixed)
	kinds := map[string]int{}
	for _, r := range mixed.Items {
		kinds[r.Kind]++
	}
	if kinds["failure"] != 1 || kinds["inspection"] != 10 {
		t.Fatalf("mixed records = %+v", kinds)
	}

	// The request is accepted and delivered even when only metadata fits.
	post("/inspect/one", `{"large":"`+strings.Repeat("x", 4096)+`"}`)
	waitFor(t, "oversized inspection metadata", time.Minute, func() bool {
		s.api(t, http.MethodGet, "/v1/records?kind=inspection", nil, &list)
		return len(list.Items) == 11
	})
	var oversizedID string
	for _, record := range list.Items {
		if record.PayloadUnavailableReason == "too_large" && record.NotRetryableReason == "inspection_only" {
			oversizedID = record.ID
		}
	}
	if oversizedID == "" {
		t.Fatal("oversized inspection retained executable or payload state")
	}
	if code, _ := s.api(t, http.MethodGet, "/v1/records/"+oversizedID+"/payload", nil, nil); code != 404 {
		t.Fatalf("oversized payload = %d", code)
	}

	// An unavailable replay service changes neither responses nor delivery.
	s.compose(t, "stop", "replay")
	for range 3 {
		post("/inspect/one", `{"replay_outage":true}`)
	}
	waitFor(t, "three bounded upload drops", time.Minute, func() bool {
		code, raw, err := do(http.MethodGet, s.url(t, "hookway", 9090)+"/metrics", nil)
		return err == nil && code == 200 && strings.Contains(string(raw), `hookway_inspection_dropped_total{reason="upload_failed"} 3`)
	})
	if got := s.rabbitMessages(t, "inspect_one"); len(got) != 4 {
		t.Fatalf("oversized/outage deliveries = %d, want four", len(got))
	}
	s.compose(t, "start", "replay")
	waitFor(t, "replay recovery", time.Minute, func() bool {
		code, _, err := do(http.MethodGet, s.url(t, "replay", 8080)+"/readyz", nil)
		return err == nil && code == 200
	})
	s.api(t, http.MethodGet, "/v1/records?kind=inspection", nil, &list)
	if len(list.Items) != 11 {
		t.Fatalf("outage unexpectedly persisted uploads: %d", len(list.Items))
	}
	if code, raw := s.api(t, http.MethodPost, "/v1/records/"+id+"/dismiss", nil, nil); code != 200 {
		t.Fatalf("dismiss = %d %s", code, raw)
	}
	if code, _ := s.api(t, http.MethodGet, "/v1/records/"+id+"/payload", nil, nil); code != 410 {
		t.Fatalf("dismissed payload = %d", code)
	}
	t.Log("Inspect Compose proof: Sample 0/1 = 0/10 inspections, 20/20 normal deliveries; oversized metadata and replay outage retained 202 with 4/4 deliveries; single/batch retry and resolution rejected; payload precision/redaction, failure isolation, dismissal passed")
}
