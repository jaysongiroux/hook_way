package consolebrowser

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jaysongiroux/hook_way/internal/control"
	"github.com/jaysongiroux/hook_way/internal/eventdelivery"
	"github.com/jaysongiroux/hook_way/internal/eventops"
	"github.com/jaysongiroux/hook_way/internal/flowconfig"
	"github.com/jaysongiroux/hook_way/internal/tlsfiles"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
)

func prepareEvents(t *testing.T, database string, pool *pgxpool.Pool) (*eventops.Client, func() error) {
	t.Helper()
	ctx := context.Background()
	raw, e := os.ReadFile("../../../examples/mcp-events.yaml")
	if e != nil {
		t.Fatal(e)
	}
	cfg, issues := flowconfig.LoadBytes([]byte(strings.ReplaceAll(string(raw), "${EVENT_DATABASE_URL}", database)), "fixture", false)
	if len(issues) > 0 {
		t.Fatal(issues)
	}
	t.Setenv("EVENT_ENCRYPTION_KEY", base64.StdEncoding.EncodeToString([]byte(strings.Repeat("k", 32))))
	store, e := eventdelivery.Open(ctx, *cfg.EventDelivery)
	if e != nil {
		t.Fatal(e)
	}
	if e = store.Migrate(ctx); e != nil {
		t.Fatal(e)
	}
	_ = store.Close()
	runtime, e := eventdelivery.New(ctx, cfg, nil)
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { _ = runtime.Close() })
	runtime.BindEventAuthorizer("agent_tools", func(_ context.Context, p flowconfig.EventPrincipal) bool {
		return p.ID == "key:reader" && p.Key == "reader"
	})
	ctl := control.New(control.Options{Token: "console-event-control-0123456789", Events: runtime})
	server := httptest.NewServer(ctl.HTTPServer().Handler)
	t.Cleanup(server.Close)
	client, e := eventops.NewClient(server.URL, "console-event-control-0123456789", tlsfiles.Files{})
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(client.Close)
	seed := func() error {
		ns := cfg.EventDelivery.Namespace
		if _, e = pool.Exec(ctx, `DELETE FROM hookway_events.actions WHERE namespace=$1;`, ns); e != nil {
			return e
		}
		if _, e = pool.Exec(ctx, `DELETE FROM hookway_events.deliveries WHERE namespace=$1`, ns); e != nil {
			return e
		}
		if _, e = pool.Exec(ctx, `DELETE FROM hookway_events.occurrences WHERE namespace=$1`, ns); e != nil {
			return e
		}
		if _, e = pool.Exec(ctx, `DELETE FROM hookway_events.subscriptions WHERE namespace=$1`, ns); e != nil {
			return e
		}
		principal, _ := json.Marshal(flowconfig.EventPrincipal{ID: "key:reader", Key: "reader"})
		if _, e = pool.Exec(ctx, `INSERT INTO hookway_events.subscriptions(namespace,id,endpoint,principal,owner,event,arguments,callback,secret,expires_at) VALUES($1,'sub_console','agent_tools',$2,'key:reader','issue.opened','{"repository":"acme/app"}','https://receiver.example.test/private?token=hidden','opaque'::bytea,now()+interval '1 hour')`, ns, principal); e != nil {
			return e
		}
		body := []byte(`{"eventId":"console-issue","name":"issue.opened","timestamp":"2026-10-21T00:00:00Z","data":{"issue_id":"42","repository":"acme/app","title":"Example issue"},"cursor":null}`)
		if _, e = pool.Exec(ctx, `INSERT INTO hookway_events.occurrences(namespace,endpoint,event,event_id,body,data_hash) VALUES($1,'agent_tools','issue.opened','console-issue',$2,'hash'::bytea)`, ns, body); e != nil {
			return e
		}
		_, e = pool.Exec(ctx, `INSERT INTO hookway_events.deliveries(namespace,endpoint,event,event_id,subscription,state,attempts,cycle_attempts,finished_at,http_status,outcome) VALUES($1,'agent_tools','issue.opened','console-issue','sub_console','failed',2,2,now(),400,'terminal')`, ns)
		return e
	}
	return client, seed
}
