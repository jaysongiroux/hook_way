package queue

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"

	"github.com/jaysongiroux/hook_way/internal/config"
)

var envMatcher = mustMatcher("{env}-events", map[string]string{"env": "^tenant-[a-z0-9]+$"})

func dynamicMsg() Message {
	return Message{ID: "evt-1", Endpoint: "ep", ReceivedAt: time.Date(2026, 9, 3, 12, 0, 0, 0, time.UTC), Body: map[string]any{"a": 1}, Variables: map[string]string{"env": "tenant-a"}}
}

func TestS3PublisherDynamicTarget(t *testing.T) {
	fake := newFakeS3()
	fake.buckets["b"] = true
	pub, err := NewS3Publisher(context.Background(), fake, S3Config{Bucket: "b", DestinationID: "archive", Targets: []S3Target{{Name: "static"}, {Name: "{env}-events", Template: envMatcher}}})
	if err != nil {
		t.Fatal(err)
	}
	if err := pub.Publish(context.Background(), "archive", "tenant-a-events", dynamicMsg()); err != nil {
		t.Fatalf("dynamic publish: %v", err)
	}
	if len(fake.puts) != 1 || !strings.HasPrefix(aws.ToString(fake.puts[0].Key), "tenant-a-events/") {
		t.Fatalf("expected key under rendered folder, got %#v", fake.puts)
	}
	if err := pub.Publish(context.Background(), "archive", "prod-events", dynamicMsg()); err == nil {
		t.Fatal("non-matching rendered target must be rejected")
	}
	if err := pub.Publish(context.Background(), "archive", "static", dynamicMsg()); err != nil {
		t.Fatalf("static publish: %v", err)
	}
}

func TestPostgresPublisherDynamicTarget(t *testing.T) {
	fake := &fakePG{}
	pub, err := NewPostgresPublisher(context.Background(), fake, PostgresConfig{Schema: "public", Table: "t", DestinationID: "wh", Targets: []PostgresTarget{{Name: "{env}-events", Template: envMatcher}}})
	if err != nil {
		t.Fatal(err)
	}
	if err := pub.Publish(context.Background(), "wh", "tenant-a-events", dynamicMsg()); err != nil {
		t.Fatalf("dynamic publish: %v", err)
	}
	ins, ok := fake.findExec("INSERT INTO")
	if !ok || ins.args[7] != "tenant-a-events" {
		t.Fatalf("target column should be the rendered target: %#v", ins.args)
	}
	if err := pub.Publish(context.Background(), "wh", "nope", dynamicMsg()); err == nil {
		t.Fatal("unknown target must be rejected")
	}
}

func TestMongoPublisherDynamicTarget(t *testing.T) {
	fake := &fakeMongo{}
	pub, err := NewMongoPublisher(context.Background(), fake, MongoConfig{DestinationID: "archive", Targets: []MongoTarget{{Name: "{env}-events", Template: envMatcher}}})
	if err != nil {
		t.Fatal(err)
	}
	if err := pub.Publish(context.Background(), "archive", "tenant-a-events", dynamicMsg()); err != nil {
		t.Fatalf("dynamic publish: %v", err)
	}
	if len(fake.inserts) != 1 || fake.inserts[0].collection != "tenant-a-events" {
		t.Fatalf("collection should be rendered target: %#v", fake.inserts)
	}
}

func TestRedisListPublisherDynamicTarget(t *testing.T) {
	fake := newFakeRedisOps()
	pub, err := NewRedisListPublisher(context.Background(), fake, RedisListConfig{KeyPrefix: "hw", DestinationID: "lists", Targets: []RedisListTarget{{Name: "{env}-events", MaxLen: 5, Template: envMatcher}}})
	if err != nil {
		t.Fatal(err)
	}
	if err := pub.Publish(context.Background(), "lists", "tenant-a-events", dynamicMsg()); err != nil {
		t.Fatalf("dynamic publish: %v", err)
	}
	if len(fake.lpushTrimCalls) != 1 || fake.lpushTrimCalls[0].key != "hw:tenant-a-events" || fake.lpushTrimCalls[0].approxMaxLen != 5 {
		t.Fatalf("list key/maxlen from template: %#v", fake.lpushTrimCalls)
	}
}

func TestRedisPubSubPublisherDynamicTarget(t *testing.T) {
	fake := newFakeRedisOps()
	pub, err := NewRedisPubSubPublisher(context.Background(), fake, RedisPubSubConfig{KeyPrefix: "hw", DestinationID: "ps", Targets: []RedisPubSubTarget{{Name: "{env}-events", Template: envMatcher}}})
	if err != nil {
		t.Fatal(err)
	}
	if err := pub.Publish(context.Background(), "ps", "tenant-a-events", dynamicMsg()); err != nil {
		t.Fatalf("dynamic publish: %v", err)
	}
	if len(fake.publishCalls) != 1 || fake.publishCalls[0].channel != "hw:tenant-a-events" {
		t.Fatalf("channel from template: %#v", fake.publishCalls)
	}
}

func TestRedisKVPublisherDynamicTarget(t *testing.T) {
	fake := newFakeRedisOps()
	pub, err := NewRedisKVPublisher(context.Background(), fake, RedisKVConfig{KeyPrefix: "hw", DestinationID: "kv", Targets: []RedisKVTarget{{Name: "{env}-events", TTL: time.Minute, KeyTemplate: "{prefix}:{target}:{id}", Template: envMatcher}}})
	if err != nil {
		t.Fatal(err)
	}
	if err := pub.Publish(context.Background(), "kv", "tenant-a-events", dynamicMsg()); err != nil {
		t.Fatalf("dynamic publish: %v", err)
	}
	if len(fake.setCalls) != 1 || fake.setCalls[0].key != "hw:tenant-a-events:evt-1" || fake.setCalls[0].ttl != time.Minute {
		t.Fatalf("kv key/ttl from template: %#v", fake.setCalls)
	}
}

func TestTemporalPublisherDynamicTarget(t *testing.T) {
	fake := &fakeTemporalOps{}
	pub, _ := newTestTemporalPublisher(fake, TemporalTarget{TaskQueue: "{env}-events", WorkflowType: "W", Input: "body", Template: envMatcher})
	if err := pub.Publish(context.Background(), "orchestrator", "tenant-a-events", dynamicMsg()); err != nil {
		t.Fatalf("dynamic publish: %v", err)
	}
	if len(fake.starts) != 1 || fake.starts[0].opts.TaskQueue != "tenant-a-events" || fake.starts[0].opts.WorkflowType != "W" {
		t.Fatalf("task queue rendered, settings from template: %#v", fake.starts)
	}
	if _, ok := fake.starts[0].input.(map[string]any); !ok {
		t.Fatalf("input mode from template: %#v", fake.starts[0].input)
	}
}

func TestWebhookPublisherDynamicTargetAndTemplatedURL(t *testing.T) {
	var hits int32
	var paths []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&hits, 1)
		paths = append(paths, r.URL.Path)
		_, _ = io.ReadAll(r.Body)
		w.WriteHeader(200)
	}))
	defer srv.Close()
	cfg := config.WebhookConfig{URL: srv.URL + "/{env}/hub", Timeout: time.Second, DeliveryMode: "sync", Auth: config.WebhookAuthConfig{Type: "none"}}
	metrics := &fakeWebhookMetrics{}
	pub, err := NewWebhookPublisher(cfg, "hub", []WebhookTarget{{Name: "event"}, {Name: "{env}-events", Template: envMatcher}}, WebhookPublisherOptions{Workers: 1, DefaultDrain: time.Second, Metrics: metrics, Logger: testLogger()})
	if err != nil {
		t.Fatal(err)
	}
	// Static target, templated URL: host/path rendered from msg.Variables.
	if err := pub.Publish(context.Background(), "hub", "event", dynamicMsg()); err != nil {
		t.Fatalf("static target publish: %v", err)
	}
	// Dynamic target with the same templated URL.
	if err := pub.Publish(context.Background(), "hub", "tenant-a-events", dynamicMsg()); err != nil {
		t.Fatalf("dynamic target publish: %v", err)
	}
	// Missing variable: cannot render the URL; terminal error, no request.
	noVars := dynamicMsg()
	noVars.Variables = nil
	if err := pub.Publish(context.Background(), "hub", "event", noVars); err == nil {
		t.Fatal("missing url variable must fail the publish")
	}
	if err := pub.Publish(context.Background(), "hub", "prod-events", dynamicMsg()); err == nil {
		t.Fatal("non-matching dynamic target must be rejected")
	}
	// A url variable that escapes the safe-name set (e.g. from a tampered or
	// replayed envelope) must never be rendered into the delivery url.
	unsafe := dynamicMsg()
	unsafe.Variables = map[string]string{"env": "evil.example/x"}
	if err := pub.Publish(context.Background(), "hub", "event", unsafe); err == nil {
		t.Fatal("unsafe url variable must fail the publish")
	}
	_ = pub.Close()
	if atomic.LoadInt32(&hits) != 2 || paths[0] != "/tenant-a/hub/event" || paths[1] != "/tenant-a/hub/tenant-a-events" {
		t.Fatalf("unexpected deliveries: hits=%d paths=%v", hits, paths)
	}
}

func TestWebhookMetricsLabelDynamicTargetsByTemplate(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(200) }))
	defer srv.Close()
	rec := &recordingWebhookMetrics{}
	pub, err := NewWebhookPublisher(config.WebhookConfig{URL: srv.URL, Timeout: time.Second, DeliveryMode: "sync", Auth: config.WebhookAuthConfig{Type: "none"}},
		"hub", []WebhookTarget{{Name: "{env}-events", Template: envMatcher}}, WebhookPublisherOptions{Workers: 1, DefaultDrain: time.Second, Metrics: rec, Logger: testLogger()})
	if err != nil {
		t.Fatal(err)
	}
	if err := pub.Publish(context.Background(), "hub", "tenant-a-events", dynamicMsg()); err != nil {
		t.Fatal(err)
	}
	if rec.lastDeliveryTarget != "{env}-events" {
		t.Fatalf("delivery metric should be labelled by template, got %q", rec.lastDeliveryTarget)
	}
}

// recordingWebhookMetrics captures the target label of the last delivery metric.
type recordingWebhookMetrics struct {
	fakeWebhookMetrics
	lastDeliveryTarget string
}

func (r *recordingWebhookMetrics) WebhookDelivery(_, target, _ string) { r.lastDeliveryTarget = target }

func TestFactoryCompilesTemplatesForDynamicRoutes(t *testing.T) {
	var gotTargets []S3Target
	factory := NewFactory(FactoryOptions{
		S3Connector: func(ctx context.Context, destinationID string, cfg S3Config) (Publisher, error) {
			gotTargets = cfg.Targets
			return &recordingPublisher{}, nil
		},
	})
	_, err := factory.Create(context.Background(), config.Config{
		Destinations: map[string]config.DestinationConfig{"archive": {Type: "s3", S3: config.S3Config{Region: "us-east-1", Bucket: "b"}}},
		Endpoints: map[string]config.EndpointConfig{"ep": {
			Enabled: true, Path: "/x",
			Variables: map[string]config.VariableConfig{
				"env":    {Source: "header", Path: "X-Env", Values: []string{"qa", "prod"}},
				"tenant": {Source: "header", Path: "X-T", Pattern: "^[a-z]+$"},
			},
			Destinations: []config.EndpointDestinationConfig{{Destination: "archive", Target: "{env}-{tenant}"}},
		}},
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if len(gotTargets) != 2 || gotTargets[0].Name != "prod-{tenant}" || gotTargets[1].Name != "qa-{tenant}" {
		t.Fatalf("expected two partially expanded dynamic targets, got %#v", gotTargets)
	}
	for _, tgt := range gotTargets {
		if !tgt.Template.IsTemplate() || !tgt.Template.Match(strings.Replace(tgt.Name, "{tenant}", "acme", 1)) || tgt.Template.Match(strings.Replace(tgt.Name, "{tenant}", "ACME", 1)) {
			t.Fatalf("template matcher not compiled from pattern sources: %#v", tgt)
		}
	}
}
