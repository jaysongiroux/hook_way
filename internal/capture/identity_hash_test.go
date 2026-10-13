package capture

import (
	"context"
	"errors"
	"fmt"
	"net"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"

	"github.com/jaysongiroux/hook_way/internal/config"
	"github.com/jaysongiroux/hook_way/internal/graph"
	"github.com/jaysongiroux/hook_way/internal/queue"
	"github.com/jaysongiroux/hook_way/internal/replaywire"
)

// Postgres URLs carrying passwords: DestinationHash must ignore the userinfo.
const (
	pgURL        = "postgres://app:pw1@db:5432/app" //nolint:gosec // G101: test fixture
	pgURLRotated = "postgres://app:pw2@db:5432/app" //nolint:gosec // G101: test fixture
	pgURLShort   = "postgres://app:pw@db/app"       //nolint:gosec // G101: test fixture
)

func nodeConfig(t *testing.T, src string) graph.NodeConfig {
	t.Helper()
	var nc graph.NodeConfig
	if err := yaml.Unmarshal([]byte(src), &nc); err != nil {
		t.Fatal(err)
	}
	return nc
}

func TestNodeConfigHashFollowsTheAuthoredOptions(t *testing.T) {
	a := nodeConfig(t, "{id: out, type: publish, destination: q, target: t1}")
	same := nodeConfig(t, "{id: out, type: publish, destination: q, target: t1, description: renamed}")
	changed := nodeConfig(t, "{id: out, type: publish, destination: q, target: t2}")
	if h := NodeConfigHash(a); len(h) != 64 || h != NodeConfigHash(same) || h == NodeConfigHash(changed) {
		t.Fatalf("hashes: %s %s %s", h, NodeConfigHash(same), NodeConfigHash(changed))
	}
	if NodeConfigHash(graph.NodeConfig{}) != "" {
		t.Fatal("an unknown node must hash to empty")
	}
}

func TestDestinationHashIgnoresCredentials(t *testing.T) {
	base := config.DestinationConfig{Type: "postgres", Postgres: config.PostgresConfig{URL: pgURL, Table: "events"}}
	rotated := base
	rotated.Postgres.URL = pgURLRotated
	moved := base
	moved.Postgres.Table = "other"
	if h := DestinationHash(base); h == "" || h != DestinationHash(rotated) || h == DestinationHash(moved) {
		t.Fatalf("hashes: %s %s %s", h, DestinationHash(rotated), DestinationHash(moved))
	}
	hook := config.DestinationConfig{Type: "webhook", Webhook: config.WebhookConfig{URL: "https://x", Auth: config.WebhookAuthConfig{Type: "hmac", Secret: "s1"}}}
	hook2 := hook
	hook2.Webhook.Auth.Secret = "s2"
	if DestinationHash(hook) != DestinationHash(hook2) {
		t.Fatal("rotating a webhook secret changed the destination hash")
	}
	if DestinationHash(config.DestinationConfig{}) != "" {
		t.Fatal("an unknown destination must hash to empty")
	}
}

func TestDestinationHashDoesNotMutateTheConfig(t *testing.T) {
	d := config.DestinationConfig{Type: "postgres", Postgres: config.PostgresConfig{URL: pgURLShort}}
	DestinationHash(d)
	if d.Postgres.URL != pgURLShort {
		t.Fatalf("config changed to %q", d.Postgres.URL)
	}
}

type timeoutErr struct{}

func (timeoutErr) Error() string   { return "i/o timeout" }
func (timeoutErr) Timeout() bool   { return true }
func (timeoutErr) Temporary() bool { return true }

func TestErrorClass(t *testing.T) {
	for want, err := range map[string]error{
		replaywire.ErrorClass5xx:        fmt.Errorf("webhook: %w", &queue.DeliveryError{Outcome: "5xx", Attempts: 3, Err: errors.New("500")}),
		replaywire.ErrorClass429:        &queue.DeliveryError{Outcome: "429", Err: errors.New("429")},
		replaywire.ErrorClassConnection: &queue.DeliveryError{Outcome: "error", Err: errors.New("refused")},
		replaywire.ErrorClassQueueFull:  fmt.Errorf("enqueue: %w", queue.ErrWebhookQueueFull),
		replaywire.ErrorClassPanic:      graph.PanicError{},
		replaywire.ErrorClassTimeout:    fmt.Errorf("publish: %w", context.DeadlineExceeded),
		"timeout2":                      &net.OpError{Op: "dial", Err: timeoutErr{}},
		replaywire.ErrorClassOther:      errors.New("s3: access denied"),
	} {
		if want == "timeout2" {
			want = replaywire.ErrorClassTimeout
		}
		if got := ErrorClass(err); got != want {
			t.Errorf("ErrorClass(%v) = %s, want %s", err, got, want)
		}
	}
	if got := ErrorClass(&net.OpError{Op: "dial", Err: errors.New("connection refused")}); got != replaywire.ErrorClassConnection {
		t.Errorf("dial error = %s, want connection", got)
	}
}

func TestErrorMessageDropsURLCredentials(t *testing.T) {
	msg := ErrorMessage(errors.New(`post "https://user:hunter2@hooks.example.com/x": dial failed`))
	if strings.Contains(msg, "hunter2") || !strings.Contains(msg, "hooks.example.com") {
		t.Fatalf("ErrorMessage = %q", msg)
	}
}

// Credential-bearing configuration strings for the hash tests.
const (
	pgQueryURL1 = "postgres://db:5432/app?password=p1&sslmode=require" //nolint:gosec // G101: test fixture
	pgQueryURL2 = "postgres://db:5432/app?password=p2&sslmode=require" //nolint:gosec // G101: test fixture
	pgDSN1      = "host=db password=p1 dbname=app"                     //nolint:gosec // G101: test fixture
	pgDSN2      = "host=db password=p2 dbname=app"                     //nolint:gosec // G101: test fixture
	pgDSNMoved  = "host=db password=p1 dbname=other"                   //nolint:gosec // G101: test fixture
)

// Transport errors quote the whole URL; its path and query can carry keys
// (Slack webhook paths, ?api_key=).
func TestErrorMessageKeepsOnlySchemeAndHostOfURLs(t *testing.T) {
	msg := ErrorMessage(errors.New(`Post "http://127.0.0.1:1/services/T0/B0/SLACKSECRET?api_key=QUERYSECRET#frag": dial tcp 127.0.0.1:1: connect: connection refused`))
	for _, secret := range []string{"SLACKSECRET", "QUERYSECRET", "frag"} {
		if strings.Contains(msg, secret) {
			t.Fatalf("ErrorMessage kept %q: %q", secret, msg)
		}
	}
	if !strings.Contains(msg, `"http://127.0.0.1:1"`) || !strings.Contains(msg, "connection refused") {
		t.Fatalf("ErrorMessage = %q", msg)
	}
}

func TestNodeConfigHashIgnoresCredentials(t *testing.T) {
	hash := func(src string) string { return NodeConfigHash(nodeConfig(t, src)) }
	base := hash(`{id: call, type: http_request, url: "https://api.example.com/v1/hook?api_key=k1", headers: {Authorization: "Bearer t1"}}`)
	rotated := hash(`{id: call, type: http_request, url: "https://api.example.com/v1/hook?api_key=k2", headers: {Authorization: "Bearer t2"}}`)
	moved := hash(`{id: call, type: http_request, url: "https://api.example.com/v2/hook?api_key=k1", headers: {Authorization: "Bearer t1"}}`)
	if base == "" || base != rotated || base == moved {
		t.Fatalf("hashes: %s %s %s", base, rotated, moved)
	}
	a := hash(`{id: auth, type: auth, scheme: hmac_sha256_hex, secret: s1, signature_header: X-Sig}`)
	b := hash(`{id: auth, type: auth, scheme: hmac_sha256_hex, secret: s2, signature_header: X-Sig}`)
	if a != b {
		t.Fatal("rotating an auth secret changed the node hash")
	}
}

func TestDestinationHashIgnoresURLQueryAndDSNPasswords(t *testing.T) {
	pg := func(u string) string {
		return DestinationHash(config.DestinationConfig{Type: "postgres", Postgres: config.PostgresConfig{URL: u, Table: "events"}})
	}
	if pg(pgQueryURL1) != pg(pgQueryURL2) {
		t.Fatal("a password in the URL query changed the destination hash")
	}
	if pg(pgDSN1) != pg(pgDSN2) || pg(pgDSN1) == pg(pgDSNMoved) {
		t.Fatalf("keyword DSN hashes: %s %s %s", pg(pgDSN1), pg(pgDSN2), pg(pgDSNMoved))
	}
	hook := func(u string) string {
		return DestinationHash(config.DestinationConfig{Type: "webhook", Webhook: config.WebhookConfig{URL: u}})
	}
	if hook("https://hooks.example.com/in?token=t1") != hook("https://hooks.example.com/in?token=t2") {
		t.Fatal("a token in the webhook URL query changed the destination hash")
	}
}
