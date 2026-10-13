package httpapi

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	dt "github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/modules/localstack"

	"github.com/jaysongiroux/hook_way/internal/awsconf"
	"github.com/jaysongiroux/hook_way/internal/routing"
	"github.com/jaysongiroux/hook_way/internal/routingstore"
	"github.com/jaysongiroux/hook_way/internal/testenv"
)

// Native process acceptance: an outage after a known create must remain 201.
// Recovery uses a fresh core process and its registration-only flow, without
// calling the create API again.
func TestAWSRegistrationRecoveryAfterProcessExit(t *testing.T) {
	testenv.SkipIfShort(t)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	container, e := localstack.Run(ctx, "localstack/localstack:3.4", testcontainers.WithEnv(map[string]string{"SERVICES": "dynamodb"}))
	testenv.RequireContainer(t, e, "localstack")
	t.Cleanup(func() { _ = container.Terminate(context.Background()) })
	host, e := container.Host(ctx)
	if e != nil {
		t.Fatal(e)
	}
	port, e := container.MappedPort(ctx, "4566/tcp")
	if e != nil {
		t.Fatal(e)
	}
	endpoint := "http://" + net.JoinHostPort(host, port.Port())
	t.Setenv("AWS_ACCESS_KEY_ID", "test")
	t.Setenv("AWS_SECRET_ACCESS_KEY", "test")
	t.Setenv("AWS_REGION", "us-east-1")
	t.Setenv("AWS_EC2_METADATA_DISABLED", "true")
	t.Setenv("SAMPLE_PRIVATE_TOKEN", "synthetic-ingress")
	awsConfig, e := awsconf.Load(ctx, "us-east-1", endpoint, "test", "test")
	if e != nil {
		t.Fatal(e)
	}
	ddb := dynamodb.NewFromConfig(awsConfig)
	_, e = ddb.CreateTable(ctx, &dynamodb.CreateTableInput{TableName: aws.String("synthetic-routing"), KeySchema: []dt.KeySchemaElement{{AttributeName: aws.String("id"), KeyType: dt.KeyTypeHash}, {AttributeName: aws.String("id_type"), KeyType: dt.KeyTypeRange}}, AttributeDefinitions: []dt.AttributeDefinition{{AttributeName: aws.String("id"), AttributeType: dt.ScalarAttributeTypeS}, {AttributeName: aws.String("id_type"), AttributeType: dt.ScalarAttributeTypeS}}, BillingMode: dt.BillingModePayPerRequest})
	if e != nil {
		t.Fatal(e)
	}
	if e := dynamodb.NewTableExistsWaiter(ddb).Wait(ctx, &dynamodb.DescribeTableInput{TableName: aws.String("synthetic-routing")}, 20*time.Second); e != nil {
		t.Fatal(e)
	}
	// Initialize the emulator's lazy write handler outside the timed ingress
	// calls. Core keeps the example's normal two-second store deadline.
	store, e := routingstore.NewWithAdmissionBudget(ctx, routing.StoreConfig{Table: "synthetic-routing", Region: "us-east-1", Endpoint: endpoint, Timeout: "10s"}, 0)
	if e != nil {
		t.Fatal(e)
	}
	defer func() { _ = store.Close() }()
	if e := store.Put(ctx, routing.Record{Key: routing.Key{ID: "setup", IDType: "setup"}, Environment: "preview", CreatedAt: 1}); e != nil {
		t.Fatal(e)
	}
	target, _ := url.Parse(endpoint)
	proxy := httputil.NewSingleHostReverseProxy(target)
	var writes atomic.Int32
	fault := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.Header.Get("X-Amz-Target"), ".PutItem") {
			writes.Add(1)
			w.Header().Set("Content-Type", "application/x-amz-json-1.0")
			w.WriteHeader(503)
			_, _ = fmt.Fprint(w, `{"__type":"ServiceUnavailable","message":"synthetic failure"}`)
			return
		}
		proxy.ServeHTTP(w, r)
	}))
	defer fault.Close()
	var creates atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		creates.Add(1)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(201)
		_, _ = fmt.Fprint(w, `{"data":{"id":"known-42"}}`)
	}))
	defer upstream.Close()
	temp := t.TempDir()
	binary := filepath.Join(temp, "hookway")
	if output, e := exec.CommandContext(ctx, "go", "build", "-o", binary, "../../cmd/hookway").CombinedOutput(); e != nil { // #nosec G204 -- Fixed test commands and test-owned binaries; no shell or request input.
		t.Fatal(e, string(output))
	}
	listener, e := (&net.ListenConfig{}).Listen(ctx, "tcp", "127.0.0.1:0")
	if e != nil {
		t.Fatal(e)
	}
	addr := listener.Addr().String()
	_ = listener.Close()
	source, e := os.ReadFile("../../examples/routing/registration.yaml")
	if e != nil {
		t.Fatal(e)
	}
	src := strings.ReplaceAll(string(source), "127.0.0.1:8080", addr)
	src = strings.ReplaceAll(src, "https://api.example.org/resources/a", upstream.URL)
	// Fault only registration writes. Preserve the example's normal startup
	// budget so emulator scheduling cannot turn this into a startup failure.
	src = strings.Replace(src, "timeout: 2s}", "timeout: 2s, endpoint: "+fault.URL+"}", 1)
	path := filepath.Join(temp, "config.yaml")
	if e := os.WriteFile(path, []byte(src), 0600); e != nil { // #nosec G703 -- Destination is inside the test-owned temporary directory.
		t.Fatal(e)
	}
	log, e := os.Create(filepath.Join(temp, "server.log")) // #nosec G304 -- Controlled fixture name or test-owned temporary path.
	if e != nil {
		t.Fatal(e)
	}
	defer func() { _ = log.Close() }()
	var command *exec.Cmd
	done := make(chan error, 1)
	stopped := true
	t.Cleanup(func() {
		if !stopped {
			_ = command.Process.Kill()
			<-done
		}
	})
	client := &http.Client{Timeout: 5 * time.Second}
	base := "http://" + addr
	start := func() {
		t.Helper()
		command = exec.CommandContext(ctx, binary, "serve", "--config", path) // #nosec G204 -- Fixed test commands and test-owned binaries; no shell or request input.
		command.Stdout = log
		command.Stderr = log
		if e := command.Start(); e != nil {
			t.Fatal(e)
		}
		stopped = false
		go func() { done <- command.Wait() }()
		for deadline := time.Now().Add(30 * time.Second); time.Now().Before(deadline); {
			probe, e := http.NewRequestWithContext(ctx, http.MethodGet, base+"/readyz", nil)
			if e != nil {
				t.Fatal(e)
			}
			resp, e := client.Do(probe)
			if e == nil {
				_ = resp.Body.Close()
				if resp.StatusCode == 200 {
					return
				}
			}
			select {
			case e := <-done:
				stopped = true
				contents, _ := os.ReadFile(log.Name())
				t.Fatal(e, string(contents))
			default:
			}
			time.Sleep(100 * time.Millisecond)
		}
		t.Fatal("not ready")
	}
	start()
	req, e := http.NewRequestWithContext(ctx, "POST", base+"/private/create", strings.NewReader(`{"env_name":"staging","payload":{"sample":true}}`))
	if e != nil {
		t.Fatal(e)
	}
	for k, v := range map[string]string{"X-Private-Token": "synthetic-ingress", "X-Intent": "record_a", "X-Sample-Key": "synthetic-key", "X-Sample-Version": "v1", "Cookie": "sample=synthetic-cookie"} {
		req.Header.Set(k, v)
	}
	resp, e := client.Do(req)
	if e != nil {
		t.Fatal(e)
	}
	body, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if resp.StatusCode != 201 || string(body) != `{"data":{"id":"known-42"}}` || resp.Header.Get("X-Routing-Registration") != "pending" || creates.Load() != 1 || writes.Load() == 0 {
		t.Fatal(resp.StatusCode, string(body), resp.Header, creates.Load(), writes.Load())
	}
	_ = command.Process.Kill()
	<-done
	stopped = true
	evidence, e := os.ReadFile(log.Name())
	if e != nil {
		t.Fatal(e)
	}
	if !strings.Contains(string(evidence), "known-42") || !strings.Contains(string(evidence), "registration_pending") || strings.Contains(string(evidence), "synthetic-key") || strings.Contains(string(evidence), "synthetic-cookie") {
		t.Fatal("unsafe or missing retained recovery evidence", string(evidence))
	}
	// Restart core with the healthy store and a registration-only flow for the
	// same identifier type. The creation endpoint is unavailable during recovery.
	upstream.Close()
	src = strings.Replace(src, fault.URL, endpoint, 1)
	src = strings.ReplaceAll(src, "record_c", "record_a")
	if e := os.WriteFile(path, []byte(src), 0600); e != nil { // #nosec G703 -- Destination is inside the test-owned temporary directory.
		t.Fatal(e)
	}
	key := routing.Key{ID: "known-42", IDType: "record_a"}
	if _, found, e := store.Get(ctx, key); e != nil || found {
		t.Fatal("failed registration wrote", found, e)
	}
	start()
	for range 2 {
		req, e := http.NewRequestWithContext(ctx, "POST", base+"/private/register", strings.NewReader(`{"env_name":"staging","id":"known-42"}`))
		if e != nil {
			t.Fatal(e)
		}
		req.Header.Set("X-Private-Token", "synthetic-ingress")
		req.Header.Set("X-Intent", "record_a")
		resp, e := client.Do(req)
		if e != nil {
			t.Fatal(e)
		}
		body, e := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		if e != nil || resp.StatusCode != 200 || string(body) != "registered known-42" {
			contents, _ := os.ReadFile(log.Name())
			t.Fatal(resp.StatusCode, string(body), e, "registration writes through fault proxy:", writes.Load(), string(contents))
		}
	}
	record, found, e := store.Get(ctx, key)
	if e != nil || !found || record.Environment != "preview" || creates.Load() != 1 {
		t.Fatal(record, found, e, creates.Load())
	}
}
