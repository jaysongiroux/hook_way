package httpapi

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/jaysongiroux/hook_way/internal/capture"
	"github.com/jaysongiroux/hook_way/internal/metrics"
	"github.com/jaysongiroux/hook_way/internal/replaywire"
)

func inspectionBranchConfig(t *testing.T, replayURL string) string {
	t.Helper()
	src := `replay:
  url: ` + replayURL + `
  capture_token: capture-token-0123456789abcdefghijkl
  control: {addr: ':9092', token: control-token-0123456789abcdefghijkl}
` + sampleFlowYAML(1)
	src = strings.Replace(src, "  github:\n", "  github:\n    capture_failures: false\n", 1)
	src = strings.Replace(src, "    edges:", "      - {id: look, type: inspect}\n    edges:", 1)
	return strings.Replace(src, "filt>out]", "filt>look, env>out]", 1)
}

func TestInspectBufferSaturationAndShutdownPreserveDelivery(t *testing.T) {
	release := make(chan struct{})
	replay := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		<-release
		w.WriteHeader(http.StatusCreated)
	}))
	defer replay.Close()
	defer close(release)
	cfg := loadFlowCfg(t, strings.Replace(inspectionBranchConfig(t, replay.URL), "max_entries: 100", "max_entries: 1000", 1))
	m := metrics.New()
	c, err := capture.New(capture.Options{Config: cfg, Metrics: m})
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := c.Close(); err != nil {
			t.Error(err)
		}
	}()
	pub := &fakePublisher{}
	srv, err := NewFlowServer(cfg, pub, slog.New(slog.DiscardHandler), m)
	if err != nil {
		t.Fatal(err)
	}
	srv.SetCapturer(c)
	defer func() {
		if err := srv.Close(); err != nil {
			t.Error(err)
		}
	}()
	// Keep the uploader occupied while inbound requests saturate its queue.
	for i := range 200 {
		if rec := postSignedFlow(srv, []byte(fmt.Sprintf(`{"action":"opened","id":%d}`, i))); rec.Code != 202 {
			t.Fatalf("saturated response %d", rec.Code)
		}
	}
	if len(pub.published) != 200 {
		t.Fatalf("deliveries = %d, want 200", len(pub.published))
	}
	families, _ := m.Registry().Gather()
	dropped := float64(0)
	for _, f := range families {
		if f.GetName() != "hookway_inspection_dropped_total" {
			continue
		}
		for _, metric := range f.Metric {
			for _, label := range metric.Label {
				if label.GetName() == "reason" && label.GetValue() == "buffer_full" {
					dropped += metric.GetCounter().GetValue()
				}
			}
		}
	}
	if dropped == 0 {
		t.Fatal("saturation did not report dropped inspections")
	}
	// Closing collection may cancel uploads but must not alter later delivery.
	_ = c.Close()
	if rec := postSignedFlow(srv, []byte(`{"action":"opened","id":200}`)); rec.Code != 202 || len(pub.published) != 201 {
		t.Fatalf("post-close response %d deliveries=%d", rec.Code, len(pub.published))
	}
}

func TestInspectionSurvivesLaterPublishFailureWithoutFailureCapture(t *testing.T) {
	sink := &uploadSink{done: make(chan struct{}, 4)}
	replay := httptest.NewServer(sink)
	defer replay.Close()
	cfg := loadFlowCfg(t, inspectionBranchConfig(t, replay.URL))
	pub := &fakePublisher{err: errors.New("broker unavailable")}
	srv := redriveServer(t, cfg, pub)
	if rec := postSignedFlow(srv, []byte(`{"action":"opened"}`)); rec.Code != 503 {
		t.Fatalf("failed publish response = %d", rec.Code)
	}
	_ = srv.Close()
	_ = srv.capture.Close()
	sink.mu.Lock()
	defer sink.mu.Unlock()
	if len(sink.got) != 1 || sink.got[0].meta.Kind != replaywire.KindInspection || sink.got[0].meta.InboundStatus != 503 {
		t.Fatalf("settled inspections = %+v", sink.got)
	}
}

func TestInspectSampleBranchPreservesAllDeliveries(t *testing.T) {
	for _, rate := range []string{"0", "1"} {
		t.Run(rate, func(t *testing.T) {
			var mu sync.Mutex
			var records []replaywire.RecordMetadata
			replay := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				mr, err := r.MultipartReader()
				if err != nil {
					t.Error(err)
					return
				}
				for {
					p, err := mr.NextPart()
					if err == io.EOF {
						break
					}
					if err != nil {
						t.Error(err)
						return
					}
					if p.FormName() == "metadata" {
						var m replaywire.RecordMetadata
						if err := json.NewDecoder(p).Decode(&m); err != nil {
							t.Error(err)
						}
						mu.Lock()
						records = append(records, m)
						mu.Unlock()
					}
				}
				w.WriteHeader(201)
			}))
			defer replay.Close()
			src := `replay:
  url: ` + replay.URL + `
  capture_token: capture-token-0123456789abcdefghijkl
  control: {addr: ':9092', token: control-token-0123456789abcdefghijkl}
` + sampleFlowYAML(1)
			src = strings.Replace(src, "  github:\n", "  github:\n    capture_failures: false\n", 1)
			src = strings.Replace(src, "type: sample, rate: 1", "type: sample, rate: "+rate, 1)
			src = strings.Replace(src, "    edges:", "      - {id: look, type: inspect}\n    edges:", 1)
			src = strings.Replace(src, "filt>out]", "filt>look, env>out]", 1)
			cfg := loadFlowCfg(t, src)
			m := metrics.New()
			c, err := capture.New(capture.Options{Config: cfg, Metrics: m})
			if err != nil {
				t.Fatal(err)
			}
			pub := &fakePublisher{}
			srv, err := NewFlowServer(cfg, pub, slog.New(slog.DiscardHandler), m)
			if err != nil {
				t.Fatal(err)
			}
			srv.SetCapturer(c)
			for i := range 3 {
				rec := postSignedFlow(srv, []byte(fmt.Sprintf(`{"action":"opened","id":%d}`, i)))
				if rec.Code != 202 {
					t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
				}
			}
			_ = srv.Close()
			_ = c.Close()
			want := 0
			if rate == "1" {
				want = 3
			}
			if len(records) != want || len(pub.published) != 3 {
				t.Fatalf("inspections=%d deliveries=%d", len(records), len(pub.published))
			}
			for _, rec := range records {
				if rec.Kind != "inspection" || rec.Retryable || rec.InboundStatus != 202 {
					t.Fatal(rec)
				}
			}
		})
	}
}

func TestInspectOnlyResponseOutcome(t *testing.T) {
	src := `replay:
  url: http://127.0.0.1:1
  capture_token: capture-token-0123456789abcdefghijkl
  control: {addr: ':9092', token: control-token-0123456789abcdefghijkl}
` + strings.Replace(sampleFlowYAML(1), "type: publish, destination: primary, target: events", "type: inspect", 1)
	// sampleFlowYAML's publish target may be a different string; replace the full node.
	lines := strings.Split(src, "\n")
	for i, line := range lines {
		if strings.Contains(line, "id: out, type: publish") {
			lines[i] = "      - {id: out, type: inspect}"
		}
	}
	src = strings.Join(lines, "\n")
	src = strings.Replace(src, "    nodes:", "    responses: {inspected: {status: 200, body: inspected}}\n    nodes:", 1)
	srv, err := NewFlowServer(loadFlowCfg(t, src), &fakePublisher{}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := srv.Close(); err != nil {
			t.Error(err)
		}
	}()
	rec := postSignedFlow(srv, []byte(`{"action":"opened"}`))
	if rec.Code != 200 || rec.Body.String() != "inspected" {
		t.Fatalf("%d: %s", rec.Code, rec.Body.String())
	}
}

func TestInspectCapturesLaterNon2xxWithoutCouplingUploadFailures(t *testing.T) {
	for _, uploadStatus := range []int{201, 503} {
		t.Run(fmt.Sprint(uploadStatus), func(t *testing.T) {
			var mu sync.Mutex
			var captured []replaywire.RecordMetadata
			replay := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				mr, err := r.MultipartReader()
				if err != nil {
					t.Error(err)
					return
				}
				for {
					p, err := mr.NextPart()
					if err == io.EOF {
						break
					}
					if err != nil {
						t.Error(err)
						return
					}
					if p.FormName() == "metadata" {
						var m replaywire.RecordMetadata
						if err := json.NewDecoder(p).Decode(&m); err != nil {
							t.Error(err)
						}
						mu.Lock()
						captured = append(captured, m)
						mu.Unlock()
					}
				}
				w.WriteHeader(uploadStatus)
			}))
			defer replay.Close()
			src := `replay:
  url: ` + replay.URL + `
  capture_token: capture-token-0123456789abcdefghijkl
  control: {addr: ':9092', token: control-token-0123456789abcdefghijkl}
` + sampleFlowYAML(1)
			src = strings.Replace(src, "  github:\n", "  github:\n    capture_failures: false\n", 1)
			lines := strings.Split(src, "\n")
			for i, line := range lines {
				if strings.Contains(line, "id: out, type: publish") {
					lines[i] = "      - {id: out, type: inspect}\n      - {id: reply, type: respond, status: 503, body: downstream}"
				}
			}
			src = strings.Join(lines, "\n")
			src = strings.Replace(src, "filt>out]", "filt>out, out>reply]", 1)
			cfg := loadFlowCfg(t, src)
			m := metrics.New()
			c, err := capture.New(capture.Options{Config: cfg, Metrics: m})
			if err != nil {
				t.Fatal(err)
			}
			srv, err := NewFlowServer(cfg, &fakePublisher{}, slog.New(slog.DiscardHandler), m)
			if err != nil {
				t.Fatal(err)
			}
			srv.SetCapturer(c)
			rec := postSignedFlow(srv, []byte(`{"action":"opened"}`))
			if rec.Code != 503 || rec.Body.String() != "downstream" {
				t.Fatalf("response %d %s", rec.Code, rec.Body.String())
			}
			_ = srv.Close()
			_ = c.Close()
			if len(captured) == 0 {
				t.Fatal("non-2xx inspection lost")
			}
			for _, record := range captured {
				if record.InboundStatus != 503 || record.Kind != "inspection" {
					t.Fatal(record)
				}
			}
			if uploadStatus == 503 {
				families, _ := m.Registry().Gather()
				found := false
				for _, f := range families {
					if f.GetName() == "hookway_inspection_dropped_total" {
						for _, metric := range f.Metric {
							for _, label := range metric.Label {
								if label.GetName() == "reason" && label.GetValue() == "upload_failed" && metric.GetCounter().GetValue() == 1 {
									found = true
								}
							}
						}
					}
				}
				if !found {
					t.Fatal("upload drop unobserved")
				}
			}
		})
	}
}
