package httpapi

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus/promhttp"

	"github.com/jaysongiroux/hook_way/internal/queue"
)

type selectiveBatchPublisher struct{ fakePublisher }

func (p *selectiveBatchPublisher) Publish(ctx context.Context, d, target string, m queue.Message) error {
	if err := p.fakePublisher.Publish(ctx, d, target, m); err != nil {
		return err
	}
	if m.ID == "fail" {
		return errors.New("injected broker failure")
	}
	return nil
}
func adapterSource(t *testing.T, name string) string {
	t.Helper()
	raw, err := os.ReadFile("../../examples/adapters/" + name + ".yaml") // #nosec G304 -- Controlled fixture name or test-owned temporary path.
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}
func signBatch(t *testing.T, key *ecdsa.PrivateKey, stamp, body string) string {
	t.Helper()
	hash := sha256.Sum256([]byte(stamp + body))
	sig, err := ecdsa.SignASN1(rand.Reader, key, hash[:])
	if err != nil {
		t.Fatal(err)
	}
	return base64.StdEncoding.EncodeToString(sig)
}
func TestSignedBatchProfilesPreserveChildBodiesAndIDs(t *testing.T) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	der, _ := x509.MarshalPKIXPublicKey(&key.PublicKey)
	t.Setenv("SAMPLE_ECDSA_PUBLIC_KEY", base64.StdEncoding.EncodeToString(der))
	for _, mode := range []string{"shared", "dedicated"} {
		t.Run(mode, func(t *testing.T) {
			pub := &selectiveBatchPublisher{}
			src := adapterSource(t, "batch-"+mode)
			srv, err := NewFlowServer(loadFlowCfg(t, src), pub, slog.New(slog.NewTextHandler(io.Discard, nil)), nil)
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = srv.Close() }()
			path := "/webhooks/sample"
			if mode == "shared" {
				path = "/webhooks/shared/sample"
			}
			stamp := strconv.FormatInt(time.Now().Unix(), 10)
			request := func(body, sig string) *httptest.ResponseRecorder {
				r := httptest.NewRequestWithContext(t.Context(), "POST", path, strings.NewReader(body))
				r.Header.Set("X-Sample-Timestamp", stamp)
				r.Header.Set("X-Sample-Signature", sig)
				w := httptest.NewRecorder()
				srv.ServeHTTP(w, r)
				return w
			}
			body := ` [ {"environment":"staging","event_id":"one","message_id":"group"}, {"environment":"secondary","event_id":"other"}, {"environment":"preview","event_id":"fail"}, {"environment":"preview","event_id":"three"} ] `
			if w := request(body, signBatch(t, key, stamp, strings.TrimSpace(body))); len(pub.published) != 0 || w.Code != (map[string]int{"shared": 403, "dedicated": 400}[mode]) {
				t.Fatal("raw signature bypass", w.Code)
			}
			w := request(body, signBatch(t, key, stamp, body))
			wantStatus, wantCalls := 200, 4
			if mode == "dedicated" {
				wantStatus = 502
				wantCalls = 3
			}
			if w.Code != wantStatus || len(pub.published) != wantCalls {
				t.Fatal(w.Code, w.Body.String(), len(pub.published))
			}
			for _, p := range pub.published {
				if p.msg.Publish == nil || p.msg.Publish.FIFO == nil || strings.HasPrefix(string(p.msg.Publish.Body), "[") {
					t.Fatal(p.msg)
				}
				var child map[string]any
				if json.Unmarshal(p.msg.Publish.Body, &child) != nil {
					t.Fatal(string(p.msg.Publish.Body))
				}
				id := child["event_id"].(string)
				group := id
				if mode == "shared" && id == "one" {
					group = "group"
				}
				if p.msg.Publish.FIFO.GroupID != group || p.msg.Publish.FIFO.DeduplicationID != id || p.msg.ID != id {
					t.Fatal(p.msg.Publish)
				}
				if p.msg.RequestID == "" || strings.Contains(fmt.Sprint(p.msg.Headers), base64.StdEncoding.EncodeToString(der)) {
					t.Fatal("bad child metadata")
				}
				for key, values := range p.msg.Headers {
					if strings.EqualFold(key, "X-Sample-Signature") && len(values) > 0 && values[0] != "redacted" {
						t.Fatal("signature leaked", p.msg.Headers)
					}
				}
			}
			var aggregate map[string]any
			_ = json.Unmarshal(w.Body.Bytes(), &aggregate)
			if aggregate["primary"] != (map[string]float64{"shared": 3, "dedicated": 2}[mode]) {
				t.Fatal(aggregate)
			}
			metrics := httptest.NewRecorder()
			promhttp.HandlerFor(srv.metrics.Registry(), promhttp.HandlerOpts{}).ServeHTTP(metrics, httptest.NewRequestWithContext(t.Context(), "GET", "/metrics", nil))
			m := metrics.Body.String()
			if !strings.Contains(m, `hookway_batch_children_total{node="batch",result="publish_failed"} 1`) || !strings.Contains(m, `outcome="partial_failure"`) {
				t.Fatal("missing truthful failure metrics", m)
			}
			before := len(pub.published)
			single := `{"environment":"preview","event_id":"single"}`
			w = request(single, signBatch(t, key, stamp, single))
			if mode == "shared" {
				if w.Code != 200 || len(pub.published) != before+1 {
					t.Fatal(w.Code)
				}
			} else if w.Code != 400 || len(pub.published) != before {
				t.Fatal("dedicated accepted object", w.Code)
			}
		})
	}
}
func TestSchemaProjectionAndEventDrops(t *testing.T) {
	t.Setenv("SAMPLE_INGRESS_TOKEN", "synthetic")
	for _, name := range []string{"json-projection", "event-filter", "changed-fields-sns"} {
		t.Run(name, func(t *testing.T) {
			pub := &fakePublisher{}
			srv, err := NewFlowServer(loadFlowCfg(t, adapterSource(t, name)), pub, nil, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = srv.Close() }()
			send := func(body string) *httptest.ResponseRecorder {
				path := map[string]string{"json-projection": "/webhooks/schema/sample", "event-filter": "/webhooks/filter/sample", "changed-fields-sns": "/webhooks/changes/sample"}[name]
				r := httptest.NewRequestWithContext(t.Context(), "POST", path, strings.NewReader(body))
				r.Header.Set("X-Api-Key", "synthetic")
				r.Header.Set("Authorization", "Bearer synthetic")
				w := httptest.NewRecorder()
				srv.ServeHTTP(w, r)
				return w
			}
			switch name {
			case "json-projection":
				if w := send(`{"event_type":true,"event_id":12,"file_id":"13","object":{"id":3,"content":"file","extra":"keep"},"extra":true}`); w.Code != 200 {
					t.Fatal(w.Code, w.Body.String())
				}
				var body map[string]any
				_ = json.Unmarshal(pub.published[0].msg.Publish.Body, &body)
				if body["event_type"] != "true" || body["event_id"] != float64(12) || body["object"].(map[string]any)["content"] != nil || body["object"].(map[string]any)["extra"] != "keep" {
					t.Fatal(body)
				}
				if w := send(`{"event_type":"sample"}`); w.Code != 400 || len(pub.published) != 1 {
					t.Fatal("schema failure published", w.Code)
				}
			case "event-filter":
				if w := send(`{"event_type":"unused","event_id":"valid"}`); w.Code != 200 || len(pub.published) != 0 {
					t.Fatal(w.Code)
				}
				if w := send(`{"event_type":"unused"}`); w.Code != 400 {
					t.Fatal("ID check bypassed", w.Code)
				}
				metrics := httptest.NewRecorder()
				promhttp.HandlerFor(srv.metrics.Registry(), promhttp.HandlerOpts{}).ServeHTTP(metrics, httptest.NewRequestWithContext(t.Context(), "GET", "/metrics", nil))
				if !strings.Contains(metrics.Body.String(), `level="dropped_unused_event_type"`) {
					t.Fatal(metrics.Body.String())
				}
			case "changed-fields-sns":
				for _, body := range []string{`{"event_id":"same","previous":{"status":{"value":1}},"current":{"status":{"value":1}}}`, `{"event_id":"changed","previous":{"status":{"value":1}},"current":{"status":{"value":2}}}`} {
					if w := send(body); w.Code != 202 {
						t.Fatal(w.Code, w.Body.String())
					}
				}
				if len(pub.published[0].msg.MessageAttributes) != 0 || pub.published[1].msg.MessageAttributes["fields_changed"].DataType != "String.Array" {
					t.Fatal(pub.published)
				}
			}
		})
	}
}
func TestOrderedXMLManagedExchangeRelayAndFailures(t *testing.T) {
	t.Setenv("SAMPLE_INGRESS_TOKEN", "synthetic")
	t.Setenv("SAMPLE_USERNAME", "sample")
	t.Setenv("SAMPLE_PASSWORD", "synthetic-password")
	var tokens, calls atomic.Int32
	var tokenFailure atomic.Bool
	var resourceStatus atomic.Int32
	resourceStatus.Store(200)
	token := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		tokens.Add(1)
		if tokenFailure.Load() {
			w.WriteHeader(500)
			return
		}
		_, _ = fmt.Fprint(w, `{"token_field":"synthetic-token"}`)
	}))
	defer token.Close()
	resource := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		raw, _ := io.ReadAll(r.Body)
		if r.Header.Get("Authorization") != "Bearer synthetic-token" || r.Header.Get("X-Api-Key") != "" || strings.Contains(string(raw), "PlaceholderA") || !strings.Contains(string(raw), `SequenceIndex="1"`) || !strings.Contains(string(raw), "before<NAME>Real</NAME>after") {
			t.Error("bad managed XML request", r.Header, string(raw))
		}
		w.Header().Set("Content-Type", "application/xml")
		w.WriteHeader(int(resourceStatus.Load()))
		_, _ = fmt.Fprint(w, `<RESULT>relay</RESULT>`)
	}))
	defer resource.Close()
	src := adapterSource(t, "xml-ordered")
	src = strings.Replace(src, "https://login.example.net/token", token.URL, 1)
	src = strings.Replace(src, "allowed_origins: [https://api.example.net]", "allowed_origins: ["+resource.URL+"]\n    allow_private_targets: true", 1)
	src = strings.Replace(src, `url: "https://api.example.net/xml"`, `url: "`+resource.URL+`", allow_private_targets: true`, 1)
	srv, err := NewFlowServer(loadFlowCfg(t, src), &fakePublisher{}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = srv.Close() }()
	raw := `<ROOT xmlns="urn:sample"><TOP><ENTITY><NAME>PlaceholderA</NAME></ENTITY><ENTITY>before<NAME>Real</NAME>after</ENTITY></TOP></ROOT>`
	send := func(body, key string) *httptest.ResponseRecorder {
		r := httptest.NewRequestWithContext(t.Context(), "POST", "/proxy/xml", strings.NewReader(body))
		r.Header.Set("X-Api-Key", key)
		w := httptest.NewRecorder()
		srv.ServeHTTP(w, r)
		return w
	}
	if w := send(raw, "wrong"); w.Code != 401 || tokens.Load() != 0 {
		t.Fatal("auth crossed exchange", w.Code)
	}
	if w := send("not XML", "synthetic"); w.Code != 400 || tokens.Load() != 0 {
		t.Fatal("bad XML crossed exchange", w.Code)
	}
	for _, status := range []int32{200, 422} {
		resourceStatus.Store(status)
		if w := send(raw, "synthetic"); w.Code != int(status) || w.Body.String() != `<RESULT>relay</RESULT>` {
			t.Fatal(w.Code, w.Body.String())
		}
	}
	if tokens.Load() != 2 || calls.Load() != 2 {
		t.Fatal("unexpected token cache/retry", tokens.Load(), calls.Load())
	}
	tokenFailure.Store(true)
	if w := send(raw, "synthetic"); w.Code < 500 || tokens.Load() != 3 || calls.Load() != 2 {
		t.Fatal("failed exchange retried/reached resource", w.Code, tokens.Load(), calls.Load())
	}
}
