package httpapi

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
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
	"github.com/aws/aws-sdk-go-v2/service/sqs"
	sqstypes "github.com/aws/aws-sdk-go-v2/service/sqs/types"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/modules/localstack"

	"github.com/jaysongiroux/hook_way/internal/awsconf"
	"github.com/jaysongiroux/hook_way/internal/testenv"
)

// This test consumes its isolated queue to inspect publication. Hookway itself
// only runs the standalone HTTP publisher; it has no queue consumer.
func TestAWSStandaloneFIFOIngress(t *testing.T) {
	testenv.SkipIfShort(t)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	container, err := localstack.Run(ctx, "localstack/localstack:3.4", testcontainers.WithEnv(map[string]string{"SERVICES": "sqs"}))
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
	temp := t.TempDir()
	binary := filepath.Join(temp, "hookway")
	build := exec.CommandContext(ctx, "go", "build", "-o", binary, "../../cmd/hookway") // #nosec G204 -- Fixed test commands and test-owned binaries; no shell or request input.
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build: %v %s", err, output)
	}
	listener, err := (&net.ListenConfig{}).Listen(ctx, "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := listener.Addr().String()
	_ = listener.Close()
	source, err := os.ReadFile("../../examples/destinations/fifo-sqs.yaml")
	if err != nil {
		t.Fatal(err)
	}
	config := strings.Replace(string(source), `addr: ":8080"`, `addr: "`+addr+`"`, 1)
	config = strings.Replace(config, "      # endpoint: http://localhost:4566", "      endpoint: "+endpoint+"\n      access_key_id: test\n      secret_access_key: test", 1)
	config = strings.Replace(config, "${SAMPLE_WEBHOOK_SECRET}", "synthetic-secret", 1)
	configPath := filepath.Join(temp, "config.yaml")
	if err = os.WriteFile(configPath, []byte(config), 0600); err != nil { // #nosec G703 -- Destination is inside the test-owned temporary directory.
		t.Fatal(err)
	}
	logs, err := os.Create(filepath.Join(temp, "server.log")) // #nosec G304 -- Controlled fixture name or test-owned temporary path.
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = logs.Close() }()
	command := exec.CommandContext(ctx, binary, "serve", "--config", configPath) // #nosec G204 -- Fixed test commands and test-owned binaries; no shell or request input.
	command.Stdout = logs
	command.Stderr = logs
	if err = command.Start(); err != nil {
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
		response, err := client.Do(probe)
		if err == nil {
			_ = response.Body.Close()
			if response.StatusCode == 200 {
				ready = true
				break
			}
		}
		select {
		case err := <-exited:
			exited <- err
			data, _ := os.ReadFile(logs.Name())
			t.Fatalf("server exited: %v %s", err, data)
		default:
		}
		time.Sleep(100 * time.Millisecond)
	}
	if !ready {
		data, _ := os.ReadFile(logs.Name())
		t.Fatalf("server did not become ready: %s", data)
	}
	for _, c := range []struct {
		token, body string
		status      int
	}{
		{"Api-Key synthetic-secret", ` { "b": 2, "a": 1 } `, 200},
		{"synthetic-secret", `{"b":2,"a":1}`, 200},
		{"invalid", `{"x":1}`, 400},
		{"synthetic-secret", `{"x":`, 400},
	} {
		request, _ := http.NewRequestWithContext(ctx, "POST", base+"/webhooks/sample", strings.NewReader(c.body))
		request.Header.Set("Authorization", c.token)
		request.Header.Set("Content-Type", "application/json")
		response, err := client.Do(request)
		if err != nil {
			t.Fatal(err)
		}
		body, _ := io.ReadAll(response.Body)
		_ = response.Body.Close()
		if response.StatusCode != c.status {
			t.Fatalf("response = %d %s, want %d", response.StatusCode, body, c.status)
		}
	}
	cfg, err := awsconf.Load(ctx, "us-east-1", endpoint, "test", "test")
	if err != nil {
		t.Fatal(err)
	}
	broker := sqs.NewFromConfig(cfg)
	url, err := broker.GetQueueUrl(ctx, &sqs.GetQueueUrlInput{QueueName: aws.String("synthetic-events.fifo")})
	if err != nil {
		t.Fatal(err)
	}
	received, err := broker.ReceiveMessage(ctx, &sqs.ReceiveMessageInput{QueueUrl: url.QueueUrl, MaxNumberOfMessages: 10, WaitTimeSeconds: 2, AttributeNames: []sqstypes.QueueAttributeName{sqstypes.QueueAttributeNameAll}, MessageSystemAttributeNames: []sqstypes.MessageSystemAttributeName{sqstypes.MessageSystemAttributeNameAll}}) //nolint:staticcheck // LocalStack 3.4 needs the legacy selector alongside the current SQS field.
	if err != nil {
		t.Fatal(err)
	}
	if len(received.Messages) != 1 {
		t.Fatalf("received %d messages; expected one deduplicated publication", len(received.Messages))
	}
	body := `{"b":2,"a":1}`
	sum := sha256.Sum256([]byte(body))
	id := hex.EncodeToString(sum[:])[:10]
	message := received.Messages[0]
	if aws.ToString(message.Body) != body || message.Attributes["MessageGroupId"] != id || message.Attributes["MessageDeduplicationId"] != id {
		t.Fatalf("body or IDs differ: %+v", message)
	}
}
