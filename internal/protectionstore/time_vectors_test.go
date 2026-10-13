package protectionstore

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/jaysongiroux/hook_way/internal/protection"
)

func TestRedisAndGoBackendTimeVectors(t *testing.T) {
	_, store, _ := backendPair(t, "redis")
	s := store.(*Redis)
	ctx := context.Background()
	for _, p := range []protection.Policy{{Kind: protection.RateLimit, ID: "time/token", KeyScope: "ip", Strategy: protection.TokenBucket, RPM: 3, Burst: 2, MaxEntries: 10}, {Kind: protection.RateLimit, ID: "time/window", KeyScope: "ip", Strategy: protection.SlidingWindowCounter, Limit: 2, Window: time.Second, MaxEntries: 10}} {
		if err := s.EnsurePolicy(ctx, p); err != nil {
			t.Fatal(err)
		}
		var state protection.State
		exists := false
		for _, now := range []int64{999, 999, 1000, 1499, 1500, 20999, 21000, 1000} {
			raw, _ := json.Marshal(p)
			script := strings.Replace(redisScript, "local clock=redis.call('TIME')", fmt.Sprintf("local clock={%d,%d}", now/1000, (now%1000)*1000), 1)
			reply, err := s.client.Eval(ctx, script, s.keys(p.ID), "allow", string(raw), p.Fingerprint(), protection.HashIdentity("identity"), "", "", 100).Slice()
			if err != nil {
				t.Fatal(err)
			}
			// Logical deletion is safe only once the old state is fully replenished.
			if exists && state.ExpiresAt <= now {
				exists = false
			}
			var decision protection.Decision
			state, decision = protection.Admit(p, state, exists, now)
			state.Version = p.Fingerprint()
			exists = true
			if (redisInt(reply[1]) == 1) != decision.Allowed || redisInt(reply[2]) != decision.Remaining || redisInt(reply[3]) != decision.RetryAfter.Milliseconds() {
				t.Fatalf("%s at%d decision mismatch: %+v %v", p.Strategy, now, decision, reply)
			}
			persisted, err := s.client.HGet(ctx, s.keys(p.ID)[1], protection.HashIdentity("identity")).Result()
			if err != nil {
				t.Fatal(err)
			}
			var actual protection.State
			if err = json.Unmarshal([]byte(persisted), &actual); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(actual, state) {
				t.Fatalf("%s at%d state mismatch: %+v != %+v", p.Strategy, now, actual, state)
			}
		}
	}
}
