package httpapi

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	dt "github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
	"github.com/aws/aws-sdk-go-v2/service/sqs"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/modules/localstack"

	"github.com/jaysongiroux/hook_way/internal/awsconf"
	"github.com/jaysongiroux/hook_way/internal/routing"
	"github.com/jaysongiroux/hook_way/internal/routingstore"
	"github.com/jaysongiroux/hook_way/internal/testenv"
)

// The test harness consumes its own queue solely to inspect publication.
// The standalone Hookway process has no SQS consumer.
func TestAWSStandaloneRoutingIngress(t *testing.T) {
	testenv.SkipIfShort(t)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	container, err := localstack.Run(ctx, "localstack/localstack:3.4", testcontainers.WithEnv(map[string]string{"SERVICES": "dynamodb,sqs"}))
	testenv.RequireContainer(t, err, "localstack")
	t.Cleanup(func() { _ = container.Terminate(context.Background()) })
	host, err := container.Host(ctx)
	if err != nil {
		t.Fatal(err)
	}
	port, err := container.MappedPort(ctx, "4566/tcp")
	if err != nil {
		t.Fatal(err)
	}
	endpoint := "http://" + net.JoinHostPort(host, port.Port())
	t.Setenv("AWS_ACCESS_KEY_ID", "test")
	t.Setenv("AWS_SECRET_ACCESS_KEY", "test")
	t.Setenv("AWS_REGION", "us-east-1")
	t.Setenv("AWS_EC2_METADATA_DISABLED", "true")
	cfg, err := awsconf.Load(ctx, "us-east-1", endpoint, "test", "test")
	if err != nil {
		t.Fatal(err)
	}
	ddb := dynamodb.NewFromConfig(cfg)
	_, err = ddb.CreateTable(ctx, &dynamodb.CreateTableInput{TableName: aws.String("synthetic-routing"), KeySchema: []dt.KeySchemaElement{{AttributeName: aws.String("id"), KeyType: dt.KeyTypeHash}, {AttributeName: aws.String("id_type"), KeyType: dt.KeyTypeRange}}, AttributeDefinitions: []dt.AttributeDefinition{{AttributeName: aws.String("id"), AttributeType: dt.ScalarAttributeTypeS}, {AttributeName: aws.String("id_type"), AttributeType: dt.ScalarAttributeTypeS}}, BillingMode: dt.BillingModePayPerRequest})
	if err != nil {
		t.Fatal(err)
	}
	if err := dynamodb.NewTableExistsWaiter(ddb).Wait(ctx, &dynamodb.DescribeTableInput{TableName: aws.String("synthetic-routing")}, 20*time.Second); err != nil {
		t.Fatal(err)
	}
	store, err := routingstore.NewWithAdmissionBudget(ctx, routing.StoreConfig{Table: "synthetic-routing", Region: "us-east-1", Endpoint: endpoint}, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = store.Close() }()
	put := func(id, typ, env string, ts int64) {
		t.Helper()
		if err := store.Put(ctx, routing.Record{Key: routing.Key{ID: id, IDType: typ}, Environment: env, CreatedAt: ts}); err != nil {
			t.Fatal(err)
		}
	}
	put("same", "record_a", "preview", 100)
	put("same", "record_b", "production", 200)
	broker := sqs.NewFromConfig(cfg)
	queue, err := broker.CreateQueue(ctx, &sqs.CreateQueueInput{QueueName: aws.String("preview-events")})
	if err != nil {
		t.Fatal(err)
	}
	temp := t.TempDir()
	binary := filepath.Join(temp, "hookway")
	cmd := exec.CommandContext(ctx, "go", "build", "-o", binary, "../../cmd/hookway") // #nosec G204 -- Fixed test commands and test-owned binaries; no shell or request input.
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build: %v %s", err, output)
	}
	listener, err := (&net.ListenConfig{}).Listen(ctx, "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := listener.Addr().String()
	_ = listener.Close()
	src, err := os.ReadFile("../../examples/routing/shared.yaml")
	if err != nil {
		t.Fatal(err)
	}
	source := strings.ReplaceAll(string(src), "${SAMPLE_WEBHOOK_SECRET}", "synthetic")
	source = strings.Replace(source, `addr: ":8080"`, `addr: "`+addr+`"`, 1)
	source = strings.Replace(source, "timeout: 2s}", "timeout: 2s, endpoint: "+endpoint+"}", 1)
	source = strings.Replace(source, "sqs: {region: us-east-1}", "sqs: {region: us-east-1, endpoint: "+endpoint+"}", 1)
	path := filepath.Join(temp, "config.yaml")
	if err := os.WriteFile(path, []byte(source), 0600); err != nil { // #nosec G703 -- Destination is inside the test-owned temporary directory.
		t.Fatal(err)
	}
	log, err := os.Create(filepath.Join(temp, "server.log")) // #nosec G304 -- Controlled fixture name or test-owned temporary path.
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = log.Close() }()
	command := exec.CommandContext(ctx, binary, "serve", "--config", path) // #nosec G204 -- Fixed test commands and test-owned binaries; no shell or request input.
	command.Stdout = log
	command.Stderr = log
	if err := command.Start(); err != nil {
		t.Fatal(err)
	}
	exited := make(chan error, 1)
	go func() { exited <- command.Wait() }()
	t.Cleanup(func() {
		_ = command.Process.Kill()
		select {
		case <-exited:
		case <-time.After(5 * time.Second):
			t.Error("server failed to exit")
		}
	})
	client := &http.Client{Timeout: 3 * time.Second}
	base := "http://" + addr
	ready := false
	for deadline := time.Now().Add(30 * time.Second); time.Now().Before(deadline); {
		probe, probeErr := http.NewRequestWithContext(ctx, http.MethodGet, base+"/readyz", nil)
		if probeErr != nil {
			t.Fatal(probeErr)
		}
		r, err := client.Do(probe)
		if err == nil {
			_ = r.Body.Close()
			if r.StatusCode == 200 {
				ready = true
				break
			}
		}
		select {
		case err := <-exited:
			exited <- err
			data, _ := os.ReadFile(log.Name())
			t.Fatalf("server exited: %v %s", err, data)
		default:
		}
		time.Sleep(100 * time.Millisecond)
	}
	if !ready {
		data, _ := os.ReadFile(log.Name())
		t.Fatalf("not ready: %s", data)
	}
	post := func(event, id string, want int) {
		t.Helper()
		request, _ := http.NewRequestWithContext(ctx, "POST", base+"/webhooks/sample", strings.NewReader(fmt.Sprintf(`{"event":%q,"data":{"object":{"id":%q}}}`, event, id)))
		request.Header.Set("Authorization", "Api-Key synthetic")
		response, err := client.Do(request)
		if err != nil {
			t.Fatal(err)
		}
		body, _ := io.ReadAll(response.Body)
		_ = response.Body.Close()
		if response.StatusCode != want {
			t.Fatalf("post %s: %d %s, want %d", id, response.StatusCode, body, want)
		}
	}
	post("changed", "same", 202)
	post("changed", "new", 400)
	post("unused", "missing", 202)
	// Register after a miss: strongly consistent, uncached routing must see a
	// mapping written outside this ingress process immediately.
	put("new", "record_a", "preview", 250)
	post("changed", "new", 202)
	put("new", "record_a", "production", 300)
	post("changed", "new", 400)
	// Verify only admitted publications reached the queue.
	received, err := broker.ReceiveMessage(ctx, &sqs.ReceiveMessageInput{QueueUrl: queue.QueueUrl, MaxNumberOfMessages: 10, WaitTimeSeconds: 2})
	if err != nil {
		t.Fatal(err)
	}
	if len(received.Messages) != 2 {
		t.Fatalf("received %d messages, want exactly the two admitted sends", len(received.Messages))
	}
	// A broken table binding fails startup rather than mounting a reject-all flow.
	_, err = routingstore.NewWithAdmissionBudget(ctx, routing.StoreConfig{Table: "missing-table", Region: "us-east-1", Endpoint: endpoint}, 0)
	if err == nil {
		t.Fatal("missing table accepted")
	}
}
