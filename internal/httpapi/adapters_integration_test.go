package httpapi

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/sns"
	"github.com/aws/aws-sdk-go-v2/service/sqs"
	sqstypes "github.com/aws/aws-sdk-go-v2/service/sqs/types"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/modules/localstack"
	"gopkg.in/yaml.v3"

	"github.com/jaysongiroux/hook_way/internal/awsconf"
	"github.com/jaysongiroux/hook_way/internal/testenv"
)

// Only this harness reads its isolated queues. The fresh native Hookway
// process publishes signed children and changes; it has no SQS consumer.
func TestAWSStandaloneBatchAndSNSFiltering(t *testing.T) {
	testenv.SkipIfShort(t)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	container, err := localstack.Run(ctx, "localstack/localstack:3.4", testcontainers.WithEnv(map[string]string{"SERVICES": "sqs,sns"}))
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
	awsCfg, err := awsconf.Load(ctx, "us-east-1", endpoint, "test", "test")
	if err != nil {
		t.Fatal(err)
	}
	broker := sqs.NewFromConfig(awsCfg)
	topics := sns.NewFromConfig(awsCfg)
	topic, err := topics.CreateTopic(ctx, &sns.CreateTopicInput{Name: aws.String("synthetic-change-events.fifo"), Attributes: map[string]string{"FifoTopic": "true", "ContentBasedDeduplication": "false"}})
	if err != nil {
		t.Fatal(err)
	}
	inbox, err := broker.CreateQueue(ctx, &sqs.CreateQueueInput{QueueName: aws.String("synthetic-filtered.fifo"), Attributes: map[string]string{"FifoQueue": "true", "ContentBasedDeduplication": "false"}})
	if err != nil {
		t.Fatal(err)
	}
	attrs, err := broker.GetQueueAttributes(ctx, &sqs.GetQueueAttributesInput{QueueUrl: inbox.QueueUrl, AttributeNames: []sqstypes.QueueAttributeName{sqstypes.QueueAttributeNameQueueArn}})
	if err != nil {
		t.Fatal(err)
	}
	_, err = topics.Subscribe(ctx, &sns.SubscribeInput{TopicArn: topic.TopicArn, Protocol: aws.String("sqs"), Endpoint: aws.String(attrs.Attributes["QueueArn"]), Attributes: map[string]string{"RawMessageDelivery": "true", "FilterPolicy": `{"fields_changed":["status"]}`}})
	if err != nil {
		t.Fatal(err)
	}
	key, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	der, _ := x509.MarshalPKIXPublicKey(&key.PublicKey)
	var root, changes map[string]any
	_ = yaml.Unmarshal([]byte(adapterSource(t, "batch-shared")), &root)
	_ = yaml.Unmarshal([]byte(adapterSource(t, "changed-fields-sns")), &changes)
	destinations := root["destinations"].(map[string]any)
	destinations["changes"] = changes["destinations"].(map[string]any)["broker"]
	for _, entry := range destinations {
		dest := entry.(map[string]any)
		options := dest[dest["type"].(string)].(map[string]any)
		options["endpoint"] = endpoint
		options["access_key_id"] = "test"
		options["secret_access_key"] = "test"
	}
	changeFlow := changes["flows"].(map[string]any)["sample"].(map[string]any)
	for _, raw := range changeFlow["nodes"].([]any) {
		node := raw.(map[string]any)
		if node["type"] == "publish" {
			node["destination"] = "changes"
		}
	}
	root["flows"].(map[string]any)["changes"] = changeFlow
	listener, err := (&net.ListenConfig{}).Listen(ctx, "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := listener.Addr().String()
	_ = listener.Close()
	root["server"].(map[string]any)["addr"] = addr
	root["metrics"] = map[string]any{"enabled": true, "path": "/metrics"}
	rawCfg, err := yaml.Marshal(root)
	if err != nil {
		t.Fatal(err)
	}
	temp := t.TempDir()
	path := filepath.Join(temp, "config.yaml")
	if err = os.WriteFile(path, rawCfg, 0600); err != nil {
		t.Fatal(err)
	}
	binary := filepath.Join(temp, "hookway")
	build := exec.CommandContext(ctx, "go", "build", "-o", binary, "../../cmd/hookway") // #nosec G204 -- Fixed test commands and test-owned binaries; no shell or request input.
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build: %v %s", err, output)
	}
	logs, err := os.Create(filepath.Join(temp, "server.log")) // #nosec G304 -- Controlled fixture name or test-owned temporary path.
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = logs.Close() }()
	command := exec.CommandContext(ctx, binary, "serve", "--config", path) // #nosec G204 -- Fixed test commands and test-owned binaries; no shell or request input.
	command.Env = append(os.Environ(), "SAMPLE_ECDSA_PUBLIC_KEY="+base64.StdEncoding.EncodeToString(der), "SAMPLE_INGRESS_TOKEN=synthetic", "AWS_EC2_METADATA_DISABLED=true")
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
			t.Error("native process failed to exit")
		}
	})
	client := &http.Client{Timeout: 10 * time.Second}
	base := "http://" + addr
	ready := false
	for deadline := time.Now().Add(30 * time.Second); time.Now().Before(deadline); {
		probe, probeErr := http.NewRequestWithContext(ctx, http.MethodGet, base+"/readyz", nil)
		if probeErr != nil {
			t.Fatal(probeErr)
		}
		response, e := client.Do(probe)
		if e == nil {
			_ = response.Body.Close()
			if response.StatusCode == 200 {
				ready = true
				break
			}
		}
		select {
		case e := <-exited:
			exited <- e
			data, _ := os.ReadFile(logs.Name())
			t.Fatalf("native process exited: %v %s", e, data)
		default:
		}
		time.Sleep(100 * time.Millisecond)
	}
	if !ready {
		data, _ := os.ReadFile(logs.Name())
		t.Fatalf("native process unready: %s", data)
	}
	post := func(path, body string, headers map[string]string) string {
		r, _ := http.NewRequestWithContext(ctx, "POST", base+path, strings.NewReader(body))
		for k, v := range headers {
			r.Header.Set(k, v)
		}
		response, e := client.Do(r)
		if e != nil {
			t.Fatal(e)
		}
		defer func() { _ = response.Body.Close() }()
		data, _ := io.ReadAll(response.Body)
		if response.StatusCode < 200 || response.StatusCode >= 300 {
			t.Fatalf("POST %s: %d %s", path, response.StatusCode, data)
		}
		return string(data)
	}
	body := `[{"environment":"staging","event_id":"one","message_id":"group-one"},{"environment":"preview","event_id":"two"},{"environment":"unknown","event_id":"drop"}]`
	stamp := strconv.FormatInt(time.Now().Unix(), 10)
	headers := map[string]string{"X-Sample-Timestamp": stamp, "X-Sample-Signature": signBatch(t, key, stamp, body)}
	for range 2 {
		response := post("/webhooks/shared/sample", body, headers)
		if response != `{"dropped":1,"message":"Processed","primary":2}` {
			t.Fatal(response)
		}
	}
	queueURL, err := broker.GetQueueUrl(ctx, &sqs.GetQueueUrlInput{QueueName: aws.String("preview-sample-events.fifo")})
	if err != nil {
		t.Fatal(err)
	}
	messages, err := broker.ReceiveMessage(ctx, &sqs.ReceiveMessageInput{QueueUrl: queueURL.QueueUrl, MaxNumberOfMessages: 10, WaitTimeSeconds: 2, AttributeNames: []sqstypes.QueueAttributeName{sqstypes.QueueAttributeNameAll}, MessageSystemAttributeNames: []sqstypes.MessageSystemAttributeName{sqstypes.MessageSystemAttributeNameAll}}) //nolint:staticcheck // LocalStack 3.4 needs the legacy selector alongside the current SQS field.
	if err != nil {
		t.Fatal(err)
	}
	if len(messages.Messages) != 2 {
		t.Fatalf("expected two deduplicated child deliveries, got %d", len(messages.Messages))
	}
	for _, msg := range messages.Messages {
		var child map[string]any
		if err = json.Unmarshal([]byte(aws.ToString(msg.Body)), &child); err != nil {
			t.Fatal("non-child body", msg.Body)
		}
		id := child["event_id"].(string)
		group := id
		if id == "one" {
			group = "group-one"
		}
		if msg.Attributes["MessageGroupId"] != group || msg.Attributes["MessageDeduplicationId"] != id {
			t.Fatal(msg.Attributes)
		}
	}
	for _, event := range []struct{ id, previous, current string }{{"same", `{"status":{"value":1}}`, `{"status":{"value":1}}`}, {"other", `{"name":"a"}`, `{"name":"b"}`}, {"status", `{"status":{"value":1}}`, `{"status":{"value":2}}`}} {
		post("/webhooks/changes/sample", fmt.Sprintf(`{"event_id":%q,"previous":%s,"current":%s}`, event.id, event.previous, event.current), map[string]string{"X-Api-Key": "synthetic"})
	}
	filtered, err := broker.ReceiveMessage(ctx, &sqs.ReceiveMessageInput{QueueUrl: inbox.QueueUrl, MaxNumberOfMessages: 10, WaitTimeSeconds: 2, MessageAttributeNames: []string{"All"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(filtered.Messages) != 1 || !strings.Contains(aws.ToString(filtered.Messages[0].Body), `"event_id":"status"`) {
		t.Fatalf("status subscription mismatch: %+v", filtered.Messages)
	}
	attribute := filtered.Messages[0].MessageAttributes["fields_changed"]
	if aws.ToString(attribute.StringValue) != `["status","status.value"]` {
		t.Fatal(attribute)
	}
	probe, probeErr := http.NewRequestWithContext(ctx, http.MethodGet, base+"/metrics", nil)
	if probeErr != nil {
		t.Fatal(probeErr)
	}
	metricsResponse, err := client.Do(probe)
	if err != nil {
		t.Fatal(err)
	}
	data, _ := io.ReadAll(metricsResponse.Body)
	_ = metricsResponse.Body.Close()
	if !strings.Contains(string(data), `hookway_batch_children_total{node="batch",result="published"} 4`) {
		t.Fatal("native child metrics missing")
	}
}
