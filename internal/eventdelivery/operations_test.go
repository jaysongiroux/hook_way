package eventdelivery

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jaysongiroux/hook_way/internal/eventops"
)

func op(t *testing.T, r *Runtime, q eventops.Request) any {
	t.Helper()
	v, e := r.Operate(context.Background(), q)
	if e != nil {
		t.Fatalf("%s: %v", q.Operation, e)
	}
	return v
}
func TestPostgresOperatorLifecycleAndEvidence(t *testing.T) {
	cfg := fixtureConfig(t)
	r := runtimeFor(t, cfg)
	var status atomic.Int32
	server, received, _ := callback(t, &status)
	r.client = server.Client()
	s, e := subscribe(t, r, server.URL+"/secret-path?token=hidden", nil)
	if e != nil {
		t.Fatal(e)
	}
	sid := s["id"].(string)
	ctx := context.Background()
	if e = r.Publish(ctx, "agent_tools", "issue.opened", occurrence("operators", "acme/app")); e != nil {
		t.Fatal(e)
	}
	j, e := r.claim(ctx)
	if e != nil {
		t.Fatal(e)
	}
	status.Store(400)
	r.attempt(ctx, j)
	first := <-received
	d := op(t, r, eventops.Request{Operation: "delivery", ID: fmt.Sprint(j.id)}).(eventops.Delivery)
	if !d.Retry.Allowed || len(d.History) != 1 || d.History[0].HTTPStatus == nil || *d.History[0].HTTPStatus != 400 {
		t.Fatalf("missing evidence: %+v", d)
	}
	for _, code := range []int{410, 413} {
		if _, e = r.store.pool.Exec(ctx, `UPDATE hookway_events.deliveries SET http_status=$2 WHERE id=$1`, j.id, code); e != nil {
			t.Fatal(e)
		}
		blocked := op(t, r, eventops.Request{Operation: "delivery", ID: d.ID}).(eventops.Delivery)
		if blocked.Retry.Allowed {
			t.Fatalf("HTTP %d is retryable", code)
		}
	}
	if _, e = r.store.pool.Exec(ctx, `UPDATE hookway_events.deliveries SET http_status=400 WHERE id=$1`, j.id); e != nil {
		t.Fatal(e)
	}
	if _, e = r.store.pool.Exec(ctx, `UPDATE hookway_events.occurrences SET body='{}'::bytea WHERE namespace=$1`, r.cfg.Namespace); e != nil {
		t.Fatal(e)
	}
	if invalid := op(t, r, eventops.Request{Operation: "delivery", ID: d.ID}).(eventops.Delivery); invalid.Retry.Allowed {
		t.Fatal("invalid retained payload is retryable")
	}
	if _, e = r.store.pool.Exec(ctx, `UPDATE hookway_events.occurrences SET body=$2 WHERE namespace=$1`, r.cfg.Namespace, first); e != nil {
		t.Fatal(e)
	}
	sub := op(t, r, eventops.Request{Operation: "subscription", ID: sid}).(eventops.Subscription)
	if strings.Contains(sub.CallbackOrigin, "secret-path") || strings.Contains(sub.CallbackOrigin, "hidden") {
		t.Fatal("callback secret exposed")
	}
	request := eventops.Request{Operation: "retry", ID: d.ID, Actor: "operator", Key: "retry-one", Input: eventops.ActionInput{Version: d.Version, AcknowledgeDuplicateRisk: true}}
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := r.Operate(ctx, request); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	if count(t, r, "deliveries", "AND cycle=2 AND attempts=1 AND cycle_attempts=0") != 1 {
		t.Fatal("retry cycle or lifetime count wrong")
	}
	request.Key = "different"
	if _, e = r.Operate(ctx, request); e == nil {
		t.Fatal("concurrent retry accepted")
	}
	status.Store(200)
	j, e = r.claim(ctx)
	if e != nil {
		t.Fatal(e)
	}
	r.attempt(ctx, j)
	second := <-received
	if string(first) != string(second) {
		t.Fatal("manual retry changed event bytes")
	}
	d = op(t, r, eventops.Request{Operation: "delivery", ID: d.ID}).(eventops.Delivery)
	if d.Attempts != 2 || d.CycleAttempts != 1 || len(d.History) != 2 || len(d.Actions) != 1 || d.Retry.Allowed {
		t.Fatalf("cycle history wrong: %+v", d)
	}
	raw := op(t, r, eventops.Request{Operation: "payload", ID: d.ID}).(json.RawMessage)
	if string(raw) != string(first) {
		t.Fatal("payload changed")
	}
	revoke := eventops.Request{Operation: "revoke", ID: sid, Actor: "operator", Key: "revoke-one", Input: eventops.ActionInput{Version: sub.Version, Reason: "Stop monitoring"}}
	op(t, r, revoke)
	op(t, r, revoke)
	_, _ = r.store.pool.Exec(ctx, `UPDATE hookway_events.subscriptions SET expires_at=now()-interval '25 hours';UPDATE hookway_events.deliveries SET finished_at=now()-interval '25 hours';UPDATE hookway_events.occurrences SET created_at=now()-interval '25 hours'`)
	if e = r.store.GC(ctx); e != nil {
		t.Fatal(e)
	}
	if count(t, r, "subscriptions", "AND revoked_at IS NOT NULL AND octet_length(secret)=0") != 1 {
		t.Fatal("GC lost revocation or retained keys")
	}
	if _, e = subscribe(t, r, server.URL+"/secret-path?token=hidden", nil); e == nil {
		t.Fatal("revoked subscription refreshed")
	}
	sub = op(t, r, eventops.Request{Operation: "subscription", ID: sid}).(eventops.Subscription)
	op(t, r, eventops.Request{Operation: "restore", ID: sid, Actor: "operator", Key: "restore-one", Input: eventops.ActionInput{Version: sub.Version, Reason: "Allow a new subscription"}})
	if count(t, r, "subscriptions", "AND active") != 0 {
		t.Fatal("restoration activated subscription")
	}
	if _, e = subscribe(t, r, server.URL+"/secret-path?token=hidden", nil); e != nil {
		t.Fatal(e)
	}
	if count(t, r, "subscriptions", "AND active AND generation=3") != 1 {
		t.Fatal("new monitoring interval missing")
	}
	if _, e = r.Operate(ctx, eventops.Request{Operation: "delivery", ID: d.ID}); e == nil {
		t.Fatal("retention failed")
	}
}
func TestPostgresOperatorFiltersIsolationAndRecovery(t *testing.T) {
	cfg := fixtureConfig(t)
	r := runtimeFor(t, cfg)
	var status atomic.Int32
	server, received, _ := callback(t, &status)
	r.client = server.Client()
	s, e := subscribe(t, r, server.URL, nil)
	if e != nil {
		t.Fatal(e)
	}
	sid := s["id"].(string)
	ctx := context.Background()
	for i := 0; i < 3; i++ {
		if e = r.Publish(ctx, "agent_tools", "issue.opened", occurrence(fmt.Sprint(i), "acme/app")); e != nil {
			t.Fatal(e)
		}
	}
	page := op(t, r, eventops.Request{Operation: "deliveries", Filter: eventops.Filter{Limit: 1}}).(eventops.DeliveryPage)
	if len(page.Items) != 1 || page.NextCursor == "" {
		t.Fatal("pagination failed")
	}
	next := op(t, r, eventops.Request{Operation: "deliveries", Filter: eventops.Filter{Limit: 1, Cursor: page.NextCursor}}).(eventops.DeliveryPage)
	if next.Items[0].ID == page.Items[0].ID {
		t.Fatal("repeated page")
	}
	subs := op(t, r, eventops.Request{Operation: "subscriptions", Filter: eventops.Filter{Endpoint: "agent_tools", Event: "issue.opened", Owner: "key:reader", State: "active", Subscription: sid, Since: time.Now().Add(-time.Hour).Format(time.RFC3339), Until: time.Now().Add(2 * time.Hour).Format(time.RFC3339)}}).(eventops.SubscriptionPage)
	if len(subs.Items) != 1 {
		t.Fatal("subscription filters")
	}
	for _, f := range []eventops.Filter{{Limit: 201}, {Cursor: "!"}, {State: "bad"}, {Since: "bad"}, {Until: "bad"}, {Owner: "\x00"}, {Since: time.Now().Format(time.RFC3339), Until: time.Now().Add(-time.Hour).Format(time.RFC3339)}, {Cursor: page.NextCursor, Owner: "someone-else"}} {
		if _, e = r.Operate(ctx, eventops.Request{Operation: "deliveries", Filter: f}); e == nil {
			t.Fatalf("invalid filter accepted: %+v", f)
		}
	}
	j, e := r.claim(ctx)
	if e != nil {
		t.Fatal(e)
	}
	_, _ = r.store.pool.Exec(ctx, `UPDATE hookway_events.deliveries SET lease_until=now()-interval '1 second' WHERE id=$1`, j.id)
	recovered, e := r.claim(ctx)
	if e != nil {
		t.Fatal(e)
	}
	r.finish(ctx, j, "delivered", "stale", 0)
	status.Store(410)
	r.attempt(ctx, recovered)
	<-received
	d := op(t, r, eventops.Request{Operation: "delivery", ID: fmt.Sprint(j.id)}).(eventops.Delivery)
	if len(d.History) != 2 || d.History[1].Outcome != "unknown" || d.Retry.Allowed {
		t.Fatal("lease evidence or 410 eligibility")
	}
	sub := op(t, r, eventops.Request{Operation: "subscription", ID: sid}).(eventops.Subscription)
	op(t, r, eventops.Request{Operation: "revoke", ID: sid, Actor: "operator", Key: "stop", Input: eventops.ActionInput{Version: sub.Version, Reason: "Stop"}})
	if count(t, r, "deliveries", "AND state='pending'") != 0 {
		t.Fatal("revoke left pending work")
	}
	otherCfg := cfg
	c := *cfg.EventDelivery
	c.Namespace = "other"
	otherCfg.EventDelivery = &c
	other := runtimeFor(t, otherCfg)
	if _, e = other.Operate(ctx, eventops.Request{Operation: "subscription", ID: sid}); e == nil {
		t.Fatal("cross namespace read")
	}
	if _, e = other.Operate(ctx, eventops.Request{Operation: "restore", ID: sid, Actor: "operator", Key: "restore", Input: eventops.ActionInput{Version: sub.Version, Reason: "Other"}}); e == nil {
		t.Fatal("cross namespace mutation")
	}
	for i := 0; i < 20; i++ {
		if _, e = subscribe(t, r, server.URL, map[string]any{"arguments": map[string]any{"repository": strings.Repeat("x", 60000) + fmt.Sprint(i)}}); e != nil {
			t.Fatal(e)
		}
	}
	firstPage := op(t, r, eventops.Request{Operation: "subscriptions", Filter: eventops.Filter{Limit: 200}}).(eventops.SubscriptionPage)
	if firstPage.NextCursor == "" || len(firstPage.Items) >= 21 {
		t.Fatal("large metadata exceeded the private response budget")
	}
	lastPage := op(t, r, eventops.Request{Operation: "subscriptions", Filter: eventops.Filter{Limit: 200, Cursor: firstPage.NextCursor}}).(eventops.SubscriptionPage)
	if len(firstPage.Items)+len(lastPage.Items) != 21 || lastPage.NextCursor != "" {
		t.Fatal("large metadata pagination lost records")
	}
}
func TestPostgresOperatorCannotRetryOldMonitoringInterval(t *testing.T) {
	cfg := fixtureConfig(t)
	r := runtimeFor(t, cfg)
	var status atomic.Int32
	server, received, _ := callback(t, &status)
	r.client = server.Client()
	s, e := subscribe(t, r, server.URL, nil)
	if e != nil {
		t.Fatal(e)
	}
	ctx := context.Background()
	if e = r.Publish(ctx, "agent_tools", "issue.opened", occurrence("old", "acme/app")); e != nil {
		t.Fatal(e)
	}
	j, e := r.claim(ctx)
	if e != nil {
		t.Fatal(e)
	}
	status.Store(400)
	r.attempt(ctx, j)
	<-received
	_, _ = r.store.pool.Exec(ctx, `UPDATE hookway_events.subscriptions SET expires_at=now()-interval '1 second' WHERE id=$1`, s["id"])
	if _, e = subscribe(t, r, server.URL, nil); e != nil {
		t.Fatal(e)
	}
	d := op(t, r, eventops.Request{Operation: "delivery", ID: fmt.Sprint(j.id)}).(eventops.Delivery)
	if d.Retry.Allowed {
		t.Fatal("historical catch-up enabled")
	}
	if _, e = r.Operate(ctx, eventops.Request{Operation: "retry", ID: d.ID, Actor: "operator", Key: "old", Input: eventops.ActionInput{Version: d.Version, AcknowledgeDuplicateRisk: true}}); e == nil {
		t.Fatal("old interval retried")
	}
}

func TestPostgresRetryRevokeRaceAndPersistentReceipts(t *testing.T) {
	cfg := fixtureConfig(t)
	r := runtimeFor(t, cfg)
	ctx := context.Background()
	var status atomic.Int32
	server, received, challenges := callback(t, &status)
	r.client = server.Client()
	s, err := subscribe(t, r, server.URL, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err = r.Publish(ctx, "agent_tools", "issue.opened", occurrence("race", "acme/app")); err != nil {
		t.Fatal(err)
	}
	j, err := r.claim(ctx)
	if err != nil {
		t.Fatal(err)
	}
	status.Store(400)
	r.attempt(ctx, j)
	<-received
	d := op(t, r, eventops.Request{Operation: "delivery", ID: fmt.Sprint(j.id)}).(eventops.Delivery)
	sub := op(t, r, eventops.Request{Operation: "subscription", ID: s["id"].(string)}).(eventops.Subscription)
	retry := eventops.Request{Operation: "retry", ID: d.ID, Actor: "operator", Key: "race-retry", Input: eventops.ActionInput{Version: d.Version, AcknowledgeDuplicateRisk: true}}
	revoke := eventops.Request{Operation: "revoke", ID: sub.ID, Actor: "operator", Key: "race-revoke", Input: eventops.ActionInput{Version: sub.Version, Reason: "Stop delivery"}}
	start := make(chan struct{})
	var wg sync.WaitGroup
	for _, req := range []eventops.Request{retry, revoke} {
		wg.Add(1)
		go func(req eventops.Request) {
			defer wg.Done()
			<-start
			_, e := r.Operate(ctx, req)
			if req.Operation == "revoke" && e != nil {
				t.Error(e)
			}
		}(req)
	}
	close(start)
	wg.Wait()
	if count(t, r, "deliveries", "AND state='pending'") != 0 {
		t.Fatal("retry escaped revocation")
	}
	// A stale worker cannot overwrite the administrative cancellation.
	r.finish(ctx, j, "delivered", "accepted", 0)
	if count(t, r, "deliveries", "AND state='delivered'") != 0 {
		t.Fatal("stale completion escaped revocation")
	}
	if err = r.Close(); err != nil {
		t.Fatal(err)
	}
	restarted := runtimeFor(t, cfg)
	restarted.client = server.Client()
	receipt := op(t, restarted, revoke).(eventops.Receipt)
	if receipt.Action != "revoke" || receipt.Actor != "operator" {
		t.Fatal("receipt lost after restart")
	}
	if _, err = subscribe(t, restarted, server.URL, nil); err == nil {
		t.Fatal("restart lost persistent revocation")
	}
	sub = op(t, restarted, eventops.Request{Operation: "subscription", ID: sub.ID}).(eventops.Subscription)
	op(t, restarted, eventops.Request{Operation: "restore", ID: sub.ID, Actor: "operator", Key: "race-restore", Input: eventops.ActionInput{Version: sub.Version, Reason: "Allow fresh monitoring"}})
	if _, err = subscribe(t, restarted, server.URL, nil); err != nil {
		t.Fatal(err)
	}
	if challenges.Load() != 2 {
		t.Fatal("restoration skipped fresh callback verification")
	}
	d = op(t, restarted, eventops.Request{Operation: "delivery", ID: d.ID}).(eventops.Delivery)
	if d.Retry.Allowed {
		t.Fatal("restoration revived old work")
	}
}
