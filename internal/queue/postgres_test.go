package queue

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

type execCall struct {
	sql  string
	args []any
}

type fakePG struct {
	execs   []execCall
	execErr error
	pingErr error
	pinged  int
	closed  int
}

func (f *fakePG) Exec(ctx context.Context, sql string, args ...any) error {
	f.execs = append(f.execs, execCall{sql: sql, args: args})
	return f.execErr
}
func (f *fakePG) Ping(ctx context.Context) error { f.pinged++; return f.pingErr }
func (f *fakePG) Close() error                   { f.closed++; return nil }

func (f *fakePG) findExec(substr string) (execCall, bool) {
	for _, c := range f.execs {
		if strings.Contains(c.sql, substr) {
			return c, true
		}
	}
	return execCall{}, false
}

func newTestPGPublisher(t *testing.T, fake *fakePG) *PostgresPublisher {
	t.Helper()
	pub, err := NewPostgresPublisher(context.Background(), fake, PostgresConfig{
		Schema: "public", Table: "webhook_events", DestinationID: "warehouse",
		Targets: []PostgresTarget{{Name: "orders"}},
	})
	if err != nil {
		t.Fatalf("NewPostgresPublisher: %v", err)
	}
	return pub
}

func TestPostgresPublisherCreatesTable(t *testing.T) {
	fake := &fakePG{}
	newTestPGPublisher(t, fake)
	ddl, ok := fake.findExec("CREATE TABLE IF NOT EXISTS")
	if !ok {
		t.Fatalf("no CREATE TABLE executed; execs=%#v", fake.execs)
	}
	if !strings.Contains(ddl.sql, `"public"."webhook_events"`) {
		t.Fatalf("DDL missing sanitized identifier: %s", ddl.sql)
	}
	if !strings.Contains(ddl.sql, "id text PRIMARY KEY") {
		t.Fatalf("DDL missing id PK: %s", ddl.sql)
	}
	if _, ok := fake.findExec("CREATE INDEX IF NOT EXISTS"); !ok {
		t.Fatalf("expected index DDL; execs=%#v", fake.execs)
	}
}

func TestPostgresPublisherInsertsRow(t *testing.T) {
	fake := &fakePG{}
	pub := newTestPGPublisher(t, fake)
	msg := Message{
		ID: "evt_1", Endpoint: "orders_webhook", DestinationQueueID: "warehouse",
		Queue: "orders", ReceivedAt: time.Date(2026, 6, 30, 12, 0, 0, 0, time.UTC),
		Headers: map[string][]string{"X-Test": {"1"}},
		Body:    map[string]any{"hello": "world"},
	}
	if err := pub.Publish(context.Background(), "warehouse", "orders", msg); err != nil {
		t.Fatalf("Publish: %v", err)
	}
	ins, ok := fake.findExec("INSERT INTO")
	if !ok {
		t.Fatalf("no INSERT executed; execs=%#v", fake.execs)
	}
	if !strings.Contains(ins.sql, "ON CONFLICT (id) DO NOTHING") {
		t.Fatalf("INSERT missing ON CONFLICT clause: %s", ins.sql)
	}
	if !strings.Contains(ins.sql, "::jsonb") {
		t.Fatalf("INSERT missing jsonb casts: %s", ins.sql)
	}
	if ins.args[0] != "evt_1" {
		t.Fatalf("arg[0] (id) = %v", ins.args[0])
	}
	if ins.args[6] != "warehouse" {
		t.Fatalf("arg[6] (destination_id) = %v, want \"warehouse\"", ins.args[6])
	}
	if ins.args[7] != "orders" {
		t.Fatalf("arg[7] (target) = %v, want \"orders\"", ins.args[7])
	}
	// body is bound as a JSON string; find it and confirm it parses.
	var foundBody bool
	for _, a := range ins.args {
		if s, ok := a.(string); ok && strings.Contains(s, "\"hello\"") {
			var m map[string]any
			if err := json.Unmarshal([]byte(s), &m); err != nil {
				t.Fatalf("body arg not valid JSON: %v", err)
			}
			foundBody = true
		}
	}
	if !foundBody {
		t.Fatalf("body JSON arg not found in %#v", ins.args)
	}
}

func TestPostgresPublisherNilBodyBindsNull(t *testing.T) {
	fake := &fakePG{}
	pub := newTestPGPublisher(t, fake)
	msg := Message{ID: "evt_2", Endpoint: "e", Queue: "orders", ReceivedAt: time.Now()}
	if err := pub.Publish(context.Background(), "warehouse", "orders", msg); err != nil {
		t.Fatalf("Publish: %v", err)
	}
	ins, _ := fake.findExec("INSERT INTO")
	// No arg should be the JSON literal "null" — nil maps/body must bind nil.
	for i, a := range ins.args {
		if s, ok := a.(string); ok && s == "null" {
			t.Fatalf("arg[%d] is JSON \"null\" string; expected nil for absent jsonb", i)
		}
	}
}

func TestPostgresPublisherRejectsUnknownTarget(t *testing.T) {
	fake := &fakePG{}
	pub := newTestPGPublisher(t, fake)
	err := pub.Publish(context.Background(), "warehouse", "not-configured", Message{ID: "x", ReceivedAt: time.Now()})
	if err == nil {
		t.Fatal("expected error for unknown target")
	}
}

func TestPostgresPublisherReadyAndClose(t *testing.T) {
	fake := &fakePG{}
	pub := newTestPGPublisher(t, fake)
	if err := pub.Ready(context.Background()); err != nil {
		t.Fatalf("Ready: %v", err)
	}
	if fake.pinged == 0 {
		t.Fatal("Ready did not Ping")
	}
	if err := pub.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if fake.closed == 0 {
		t.Fatal("Close did not close ops")
	}
}
