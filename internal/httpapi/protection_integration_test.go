package httpapi

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/jaysongiroux/hook_way/internal/flowconfig"
	"github.com/jaysongiroux/hook_way/internal/testenv"
	"github.com/moby/moby/client"
	"github.com/testcontainers/testcontainers-go"
	tcredis "github.com/testcontainers/testcontainers-go/modules/redis"
)

func TestSharedProtectionHTTPOutageAndRecovery(t *testing.T) {
	if testing.Short() {
		t.Skip("requires Docker")
	}
	ctx := t.Context()
	container, err := tcredis.Run(ctx, "redis:7-alpine")
	testenv.RequireContainer(t, err, "redis")
	t.Cleanup(func() { _ = container.Terminate(context.Background()) })
	url, err := container.ConnectionString(ctx)
	if err != nil {
		t.Fatal(err)
	}
	src := fmt.Sprintf(`server:
  addr: ":8080"
  max_body_bytes: 1048576
  readiness_interval: 100ms
state_stores:
  shared:
    namespace: live-http
    timeout: 1s
    startup_timeout: 20s
    redis: {url: %q}
flows:
  quota:
    nodes:
      - {id: in, type: http_trigger, path: /quota}
      - {id: limit, type: rate_limit, store: shared, rpm: 1, burst: 2}
      - {id: body, type: read_body, max_bytes: 1048576}
      - {id: public, type: no_auth}
      - {id: env, type: envelope}
      - {id: done, type: respond, status: 202}
    edges: [in>limit, limit>body, body>public, public>env, env>done]
`, url)
	cfg, issues := flowconfig.LoadBytes([]byte(src), "integration", true)
	for _, issue := range issues {
		if issue.Severity != "warning" {
			t.Fatal(issues)
		}
	}
	request := func(server *FlowServer, path string) int {
		w := httptest.NewRecorder()
		server.ServeHTTP(w, httptest.NewRequestWithContext(ctx, http.MethodPost, path, strings.NewReader(`{}`)))
		return w.Code
	}
	first, err := NewFlowServer(cfg, &fakePublisher{}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = first.Close() })
	second, err := NewFlowServer(cfg, &fakePublisher{}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = second.Close() })
	first.StartReadinessProbe(ctx)
	second.StartReadinessProbe(ctx)
	if code := request(first, "/quota"); code != 202 {
		t.Fatal(code)
	}
	if code := request(second, "/quota"); code != 202 {
		t.Fatal(code)
	}
	if code := request(first, "/quota"); code != 429 {
		t.Fatalf("shared quota: %d", code)
	}
	docker, err := testcontainers.NewDockerClientWithOpts(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = docker.Close() }()
	id := container.GetContainerID()
	if _, err = docker.ContainerPause(ctx, id, client.ContainerPauseOptions{}); err != nil {
		t.Fatal(err)
	}
	paused := true
	defer func() {
		if paused {
			_, _ = docker.ContainerUnpause(context.Background(), id, client.ContainerUnpauseOptions{})
		}
	}()
	if code := request(first, "/quota"); code != 503 {
		t.Fatalf("outage admission %d", code)
	}
	if code := request(first, "/readyz"); code != 503 {
		t.Fatalf("outage readiness %d", code)
	}
	if code := request(first, "/healthz"); code != 200 {
		t.Fatalf("outage liveness %d", code)
	}
	if _, err := NewFlowServer(cfg, &fakePublisher{}, nil, nil); err == nil {
		t.Fatal("startup did not reject unavailable store")
	}
	if _, err = docker.ContainerUnpause(ctx, id, client.ContainerUnpauseOptions{}); err != nil {
		t.Fatal(err)
	}
	paused = false
	deadline := time.Now().Add(8 * time.Second)
	for request(first, "/readyz") != 200 {
		if time.Now().After(deadline) {
			t.Fatal("readiness did not recover")
		}
		time.Sleep(50 * time.Millisecond)
	}
	if code := request(first, "/quota"); code != 429 {
		t.Fatalf("recovery reset quota %d", code)
	}
}
