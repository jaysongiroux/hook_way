package queue

import (
	"context"
	"testing"
	"time"

	kafka "github.com/segmentio/kafka-go"

	"github.com/jasongiroux/hookway/internal/config"
)

func TestKafkaKeyBalancerHashesOnKey(t *testing.T) {
	b := &keyBalancer{}
	partitions := []int{0, 1, 2, 3}
	p1 := b.Balance(kafka.Message{Key: []byte("account-42")}, partitions...)
	p2 := b.Balance(kafka.Message{Key: []byte("account-42")}, partitions...)
	if p1 != p2 {
		t.Fatalf("same key produced different partitions: %d vs %d", p1, p2)
	}
	if p1 < 0 || p1 > 3 {
		t.Fatalf("partition out of range: %d", p1)
	}
}

func TestKafkaKeyBalancerRoundRobinsWithoutKey(t *testing.T) {
	b := &keyBalancer{}
	partitions := []int{0, 1, 2}
	seen := map[int]bool{}
	for i := 0; i < 9; i++ {
		seen[b.Balance(kafka.Message{}, partitions...)] = true
	}
	if len(seen) < 2 {
		t.Fatalf("expected round-robin to spread across partitions, only saw %v", seen)
	}
}

func TestKafkaMapRequiredAcks(t *testing.T) {
	cases := map[string]kafka.RequiredAcks{
		"all":  kafka.RequireAll,
		"one":  kafka.RequireOne,
		"none": kafka.RequireNone,
		"":     kafka.RequireAll,
	}
	for in, want := range cases {
		if got := mapRequiredAcks(in); got != want {
			t.Errorf("mapRequiredAcks(%q) = %v, want %v", in, got, want)
		}
	}
}

func TestKafkaMapCompression(t *testing.T) {
	if mapCompression("snappy") != kafka.Snappy {
		t.Errorf("snappy mismatch")
	}
	if mapCompression("gzip") != kafka.Gzip {
		t.Errorf("gzip mismatch")
	}
	if mapCompression("none") != kafka.Compression(0) {
		t.Errorf("none should be zero compression")
	}
	if mapCompression("") != kafka.Compression(0) {
		t.Errorf("empty should be zero compression")
	}
}

func TestKafkaBuildSASLMechanism(t *testing.T) {
	if m, err := buildSASLMechanism(config.KafkaSASLConfig{}); err != nil || m != nil {
		t.Fatalf("empty mechanism: got (%v, %v), want (nil, nil)", m, err)
	}
	m, err := buildSASLMechanism(config.KafkaSASLConfig{Mechanism: "plain", Username: "u", Password: "p"})
	if err != nil || m == nil {
		t.Fatalf("plain: got (%v, %v)", m, err)
	}
	if m.Name() != "PLAIN" {
		t.Fatalf("plain mechanism name = %q", m.Name())
	}
	m, err = buildSASLMechanism(config.KafkaSASLConfig{Mechanism: "scram-sha-256", Username: "u", Password: "p"})
	if err != nil || m == nil {
		t.Fatalf("scram256: got (%v, %v)", m, err)
	}
	if _, err := buildSASLMechanism(config.KafkaSASLConfig{Mechanism: "kerberos"}); err == nil {
		t.Fatalf("expected error for unsupported mechanism")
	}
}

func TestKafkaBuildTLSConfig(t *testing.T) {
	cfg, err := buildKafkaTLSConfig(config.KafkaTLSConfig{Enabled: false})
	if err != nil || cfg != nil {
		t.Fatalf("disabled TLS should be nil: got (%v, %v)", cfg, err)
	}
	cfg, err = buildKafkaTLSConfig(config.KafkaTLSConfig{Enabled: true, ServerName: "kafka.internal", InsecureSkipVerify: true})
	if err != nil || cfg == nil {
		t.Fatalf("enabled TLS: got (%v, %v)", cfg, err)
	}
	if cfg.ServerName != "kafka.internal" || !cfg.InsecureSkipVerify {
		t.Fatalf("tls fields not applied: %#v", cfg)
	}
	if _, err := buildKafkaTLSConfig(config.KafkaTLSConfig{Enabled: true, ClientCert: "/only-cert.pem"}); err == nil {
		t.Fatalf("expected error for half mTLS")
	}
}

// TestNewKafkaOpsSetsShortBatchTimeout guards against the kafka-go writer
// default BatchTimeout (1s), which would add up to ~1s of latency to every
// synchronous single-message publish. No brokers are dialed by
// newKafkaOps itself (topic creation is skipped when there are no
// create:true targets), so this runs without a real Kafka broker.
func TestNewKafkaOpsSetsShortBatchTimeout(t *testing.T) {
	ops, err := newKafkaOps(context.Background(), "notify", config.KafkaConfig{Brokers: []string{"broker:9092"}}, nil, nil)
	if err != nil {
		t.Fatalf("newKafkaOps: %v", err)
	}
	t.Cleanup(func() { _ = ops.Close() })
	if ops.writer.BatchTimeout != 10*time.Millisecond {
		t.Fatalf("writer.BatchTimeout = %v, want 10ms", ops.writer.BatchTimeout)
	}
}
