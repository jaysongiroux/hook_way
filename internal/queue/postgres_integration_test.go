package queue

import (
	"context"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jasongiroux/hookway/internal/config"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
)

// startPostgres boots a Postgres container and returns a connection URL. It
// self-skips when Docker is unavailable, mirroring startRedis.
func startPostgres(t *testing.T) string {
	t.Helper()
	ctx := context.Background()
	container, err := tcpostgres.Run(ctx, "postgres:16-alpine",
		tcpostgres.WithDatabase("hookway"),
		tcpostgres.WithUsername("hookway"),
		tcpostgres.WithPassword("hookway"),
		tcpostgres.BasicWaitStrategies(),
	)
	if err != nil {
		t.Skipf("postgres container unavailable: %v", err)
	}
	t.Cleanup(func() { _ = container.Terminate(ctx) })
	url, err := container.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		t.Fatalf("get postgres connection string: %v", err)
	}
	return url
}

// TestPostgresPublishRoundTrip publishes a Message via PostgresPublisher and
// verifies the row contents with a raw pgx connection:
//   - exactly 1 row after publishing the same id twice (ON CONFLICT DO NOTHING)
//   - endpoint, target columns match
//   - jsonb body round-trips
func TestPostgresPublishRoundTrip(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in -short mode")
	}
	url := startPostgres(t)
	ctx := context.Background()

	ops, err := newPostgresClient(ctx, config.PostgresConfig{URL: url, Schema: "public", Table: "webhook_events"})
	if err != nil {
		t.Fatalf("newPostgresClient: %v", err)
	}
	defer ops.Close()

	pub, err := NewPostgresPublisher(ctx, ops, PostgresConfig{
		Schema: "public", Table: "webhook_events", DestinationID: "warehouse",
		Targets: []PostgresTarget{{Name: "orders"}},
	})
	if err != nil {
		t.Fatalf("NewPostgresPublisher: %v", err)
	}
	if err := pub.Ready(ctx); err != nil {
		t.Fatalf("Ready: %v", err)
	}

	msg := Message{
		ID: "evt_1", Endpoint: "orders_webhook", DestinationQueueID: "warehouse",
		Queue: "orders", ReceivedAt: time.Date(2026, 6, 30, 12, 0, 0, 0, time.UTC),
		Headers: map[string][]string{"X-Test": {"1"}},
		Body:    map[string]any{"hello": "world"},
	}
	if err := pub.Publish(ctx, "warehouse", "orders", msg); err != nil {
		t.Fatalf("Publish: %v", err)
	}
	// Publish the same id again — ON CONFLICT DO NOTHING must keep it at one row.
	if err := pub.Publish(ctx, "warehouse", "orders", msg); err != nil {
		t.Fatalf("Publish (dup): %v", err)
	}

	// Verify with a raw pgx connection.
	conn, err := pgx.Connect(ctx, url)
	if err != nil {
		t.Fatalf("pgx.Connect: %v", err)
	}
	defer conn.Close(ctx)

	var count int
	if err := conn.QueryRow(ctx, `SELECT count(*) FROM public.webhook_events WHERE id = $1`, "evt_1").Scan(&count); err != nil {
		t.Fatalf("count query: %v", err)
	}
	if count != 1 {
		t.Fatalf("expected exactly 1 row after duplicate publish, got %d", count)
	}

	var endpoint, target string
	var body map[string]any
	if err := conn.QueryRow(ctx,
		`SELECT endpoint, target, body FROM public.webhook_events WHERE id = $1`, "evt_1").
		Scan(&endpoint, &target, &body); err != nil {
		t.Fatalf("row query: %v", err)
	}
	if endpoint != "orders_webhook" || target != "orders" {
		t.Fatalf("columns wrong: endpoint=%q target=%q", endpoint, target)
	}
	if body["hello"] != "world" {
		t.Fatalf("jsonb body did not round-trip: %#v", body)
	}
}
