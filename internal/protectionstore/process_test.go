package protectionstore

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"testing"
	"time"

	"github.com/jaysongiroux/hook_way/internal/protection"
	"github.com/moby/moby/client"
	"github.com/testcontainers/testcontainers-go"
)

type processRequest struct {
	Config    protection.Config
	Policy    protection.Policy
	Operation string
	Count     int
}

func TestProtectionSubprocessHelper(t *testing.T) {
	if os.Getenv("HOOKWAY_PROTECTION_SUBPROCESS") != "1" {
		return
	}
	var request processRequest
	if json.NewDecoder(os.Stdin).Decode(&request) != nil {
		os.Exit(2)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	store, err := Open(ctx, request.Config)
	if err != nil {
		os.Exit(3)
	}
	defer func() { _ = store.Close() }()
	if store.CheckPolicy(ctx, request.Policy) != nil {
		os.Exit(4)
	}
	accepted := 0
	for i := 0; i < request.Count; i++ {
		switch request.Operation {
		case "allow":
			d, err := store.Allow(ctx, request.Policy, "shared_identity")
			if err != nil {
				os.Exit(5)
			}
			if d.Allowed {
				accepted++
			}
		case "reserve":
			r, err := store.Reserve(ctx, request.Policy, "shared_event")
			if err != nil {
				os.Exit(6)
			}
			if r.Accepted {
				accepted++
			}
		}
	}
	if json.NewEncoder(os.Stdout).Encode(accepted) != nil {
		os.Exit(7)
	}
	os.Exit(0)
}
func TestIndependentProcessesShareProtection(t *testing.T) {
	for _, backend := range []string{"redis", "postgres"} {
		t.Run(backend, func(t *testing.T) {
			store, _, cfg := backendPair(t, backend)
			ctx := context.Background()
			for _, operation := range []string{"allow", "reserve"} {
				p := protection.Policy{Kind: protection.RateLimit, ID: "process/" + operation, KeyScope: "id", Strategy: protection.TokenBucket, RPM: 1, Burst: 10, MaxEntries: 10}
				if operation == "reserve" {
					p = protection.Policy{Kind: protection.ReplayGuard, ID: p.ID, KeyScope: "id", TTL: time.Minute, MaxEntries: 10}
				}
				if err := store.EnsurePolicy(ctx, p); err != nil {
					t.Fatal(err)
				}
				request, _ := json.Marshal(processRequest{Config: cfg, Policy: p, Operation: operation, Count: 20})
				results := make(chan int, 2)
				errs := make(chan error, 2)
				for i := 0; i < 2; i++ {
					go func() {
						subctx, cancel := context.WithTimeout(ctx, 15*time.Second)
						defer cancel()
						// #nosec G204 G702 -- execute this test binary with a fixed helper selector; config travels over stdin.
						cmd := exec.CommandContext(subctx, os.Args[0], "-test.run=^TestProtectionSubprocessHelper$")
						cmd.Env = append(os.Environ(), "HOOKWAY_PROTECTION_SUBPROCESS=1")
						cmd.Stdin = bytes.NewReader(request)
						output, err := cmd.Output()
						if err != nil {
							errs <- fmt.Errorf("independent protection process failed: %w", err)
							return
						}
						var count int
						if err = json.Unmarshal(output, &count); err != nil {
							errs <- err
							return
						}
						results <- count
					}()
				}
				total := 0
				for i := 0; i < 2; i++ {
					select {
					case count := <-results:
						total += count
					case err := <-errs:
						t.Fatal(err)
					}
				}
				want := 10
				if operation == "reserve" {
					want = 1
				}
				if total != want {
					t.Fatalf("%s processes admitted %d, want%d", operation, total, want)
				}
			}
		})
	}
}
func TestRemoteOutageFailsClosedAndRecovers(t *testing.T) {
	for _, backend := range []string{"redis", "postgres"} {
		t.Run(backend, func(t *testing.T) {
			a, _, _ := backendPair(t, backend)
			ctx := context.Background()
			p := protection.Policy{Kind: protection.ReplayGuard, ID: "outage", KeyScope: "id", TTL: time.Minute, MaxEntries: 10}
			if err := a.EnsurePolicy(ctx, p); err != nil {
				t.Fatal(err)
			}
			if r, err := a.Reserve(ctx, p, "before"); err != nil || !r.Accepted {
				t.Fatal(r, err)
			}
			docker, err := testcontainers.NewDockerClientWithOpts(ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = docker.Close() }()
			containerID := redisContainerID(backend)
			if _, err = docker.ContainerPause(ctx, containerID, client.ContainerPauseOptions{}); err != nil {
				t.Fatal(err)
			}
			paused := true
			defer func() {
				if paused {
					_, _ = docker.ContainerUnpause(context.Background(), containerID, client.ContainerUnpauseOptions{})
				}
			}()
			if err := a.Check(ctx); err == nil {
				t.Fatal("outage remained usable")
			}
			if r, err := a.Reserve(ctx, p, "during"); err == nil || r.Accepted {
				t.Fatal("outage admitted reservation", r, err)
			}
			if _, err = docker.ContainerUnpause(ctx, containerID, client.ContainerUnpauseOptions{}); err != nil {
				t.Fatal(err)
			}
			paused = false
			deadline := time.Now().Add(10 * time.Second)
			for {
				if err := a.Check(ctx); err == nil {
					break
				}
				if time.Now().After(deadline) {
					t.Fatal("store did not recover")
				}
				time.Sleep(50 * time.Millisecond)
			}
			r, err := a.Reserve(ctx, p, "before")
			if err != nil || r.Accepted {
				t.Fatal("accepted state lost across server restart", r, err)
			}
			r, err = a.Reserve(ctx, p, "during")
			if err != nil || !r.Accepted {
				t.Fatal("recovered store cannot admit", r, err)
			}
		})
	}
}
func redisContainerID(backend string) string {
	if backend == "redis" {
		return redisContainer.GetContainerID()
	}
	return pgContainer.GetContainerID()
}
