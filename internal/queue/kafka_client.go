package queue

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"time"

	kafka "github.com/segmentio/kafka-go"
	"github.com/segmentio/kafka-go/sasl"
	"github.com/segmentio/kafka-go/sasl/plain"
	"github.com/segmentio/kafka-go/sasl/scram"

	"github.com/jasongiroux/hookway/internal/config"
)

// keyBalancer selects a partition by hashing the message key when one is
// present (deterministic per-key ordering), and round-robins across partitions
// when the key is empty. This gives configured partition keys stable ordering
// while unkeyed messages spread evenly.
type keyBalancer struct {
	hash kafka.Hash
	rr   kafka.RoundRobin
}

func (b *keyBalancer) Balance(msg kafka.Message, partitions ...int) int {
	if len(msg.Key) == 0 {
		return b.rr.Balance(msg, partitions...)
	}
	return b.hash.Balance(msg, partitions...)
}

// mapRequiredAcks maps the config acks value to a kafka.RequiredAcks. Empty
// defaults to RequireAll for durability (kafka-go's zero value is RequireNone).
func mapRequiredAcks(acks string) kafka.RequiredAcks {
	switch acks {
	case "none":
		return kafka.RequireNone
	case "one":
		return kafka.RequireOne
	default: // "all" and ""
		return kafka.RequireAll
	}
}

// mapCompression maps the config compression value to a kafka.Compression.
// "none"/"" yield the zero value (no compression).
func mapCompression(c string) kafka.Compression {
	switch c {
	case "gzip":
		return kafka.Gzip
	case "snappy":
		return kafka.Snappy
	case "lz4":
		return kafka.Lz4
	case "zstd":
		return kafka.Zstd
	default:
		return kafka.Compression(0)
	}
}

// buildSASLMechanism returns the SASL mechanism for the configured settings, or
// (nil, nil) when no mechanism is configured.
func buildSASLMechanism(c config.KafkaSASLConfig) (sasl.Mechanism, error) {
	switch c.Mechanism {
	case "", "none":
		return nil, nil
	case "plain":
		return plain.Mechanism{Username: c.Username, Password: c.Password}, nil
	case "scram-sha-256":
		return scram.Mechanism(scram.SHA256, c.Username, c.Password)
	case "scram-sha-512":
		return scram.Mechanism(scram.SHA512, c.Username, c.Password)
	default:
		return nil, fmt.Errorf("unsupported kafka sasl mechanism %q", c.Mechanism)
	}
}

// buildKafkaTLSConfig assembles a *tls.Config from the configured options, or
// returns nil when TLS is not enabled. Mirrors buildTLSConfig in rabbitmq.go.
func buildKafkaTLSConfig(c config.KafkaTLSConfig) (*tls.Config, error) {
	if !c.Enabled {
		return nil, nil
	}
	cfg := &tls.Config{MinVersion: tls.VersionTLS12}
	if c.ServerName != "" {
		cfg.ServerName = c.ServerName
	}
	if c.InsecureSkipVerify {
		cfg.InsecureSkipVerify = true
	}
	if c.CACert != "" {
		pem, err := os.ReadFile(c.CACert)
		if err != nil {
			return nil, fmt.Errorf("read kafka tls ca_cert: %w", err)
		}
		pool := x509.NewCertPool()
		if !pool.AppendCertsFromPEM(pem) {
			return nil, fmt.Errorf("kafka tls ca_cert %q contained no valid certificates", c.CACert)
		}
		cfg.RootCAs = pool
	}
	if c.ClientCert != "" || c.ClientKey != "" {
		if c.ClientCert == "" || c.ClientKey == "" {
			return nil, errors.New("kafka tls client_cert and client_key must both be set for mutual TLS")
		}
		cert, err := tls.LoadX509KeyPair(c.ClientCert, c.ClientKey)
		if err != nil {
			return nil, fmt.Errorf("load kafka tls client cert/key: %w", err)
		}
		cfg.Certificates = []tls.Certificate{cert}
	}
	return cfg, nil
}

// kafkaOps is the minimal Kafka client surface the publisher depends on, so
// tests can inject a fake. The concrete implementation wraps a kafka-go Writer
// (Write) and Client (Ready).
type kafkaOps interface {
	Write(ctx context.Context, topic string, key, value []byte) error
	Ready(ctx context.Context) error
	Close() error
}

// kafkaGoOps is the concrete kafkaOps backed by a kafka-go Writer (for
// publishing) and Client (for readiness metadata checks).
type kafkaGoOps struct {
	writer *kafka.Writer
	client *kafka.Client
}

func (o *kafkaGoOps) Write(ctx context.Context, topic string, key, value []byte) error {
	return o.writer.WriteMessages(ctx, kafka.Message{Topic: topic, Key: key, Value: value})
}

func (o *kafkaGoOps) Ready(ctx context.Context) error {
	_, err := o.client.Metadata(ctx, &kafka.MetadataRequest{})
	return err
}

func (o *kafkaGoOps) Close() error { return o.writer.Close() }

// newKafkaOps builds the kafka-go Writer + Client from config, applying retry
// and timeout defaults, then pre-creates every create:true target topic via
// the admin API so the first publish never races broker-side auto-creation.
// It logs a warning when neither TLS nor SASL is configured (plaintext,
// unauthenticated connection).
func newKafkaOps(ctx context.Context, destinationID string, cfg config.KafkaConfig, targets []KafkaTarget, logger *slog.Logger) (*kafkaGoOps, error) {
	if logger == nil {
		logger = slog.Default()
	}
	tlsCfg, err := buildKafkaTLSConfig(cfg.TLS)
	if err != nil {
		return nil, err
	}
	mechanism, err := buildSASLMechanism(cfg.SASL)
	if err != nil {
		return nil, err
	}
	if mechanism == nil && tlsCfg == nil {
		logger.Warn("kafka destination connecting with no TLS or SASL; credentials and payloads will traverse the network in plaintext",
			slog.String("destination", destinationID))
	}
	timeout := cfg.PublishTimeout
	if timeout <= 0 {
		timeout = 5 * time.Second
	}
	maxAttempts := cfg.Retry.MaxAttempts
	if maxAttempts <= 0 {
		maxAttempts = 3
	}
	minBackoff := cfg.Retry.InitialBackoff
	if minBackoff <= 0 {
		minBackoff = 100 * time.Millisecond
	}
	maxBackoff := cfg.Retry.MaxBackoff
	if maxBackoff <= 0 {
		maxBackoff = 2 * time.Second
	}
	transport := &kafka.Transport{TLS: tlsCfg, SASL: mechanism}
	writer := &kafka.Writer{
		Addr:            kafka.TCP(cfg.Brokers...),
		Balancer:        &keyBalancer{},
		RequiredAcks:    mapRequiredAcks(cfg.Acks),
		Compression:     mapCompression(cfg.Compression),
		MaxAttempts:     maxAttempts,
		WriteBackoffMin: minBackoff,
		WriteBackoffMax: maxBackoff,
		WriteTimeout:    timeout,
		// Topics are pre-created below for create:true routes, so the Writer
		// does not rely on broker-side auto-creation (which races on first publish).
		AllowAutoTopicCreation: false,
		Transport:              transport,
	}
	client := &kafka.Client{Addr: kafka.TCP(cfg.Brokers...), Timeout: timeout, Transport: transport}
	ops := &kafkaGoOps{writer: writer, client: client}
	if err := ops.ensureTopics(ctx, destinationID, timeout, targets, logger); err != nil {
		return nil, err
	}
	return ops, nil
}

// ensureTopics creates every create:true target topic up front so the first
// publish never races broker-side auto-creation. TopicAlreadyExists is treated
// as success (idempotent). NumPartitions/ReplicationFactor of 0 map to -1
// (kafka-go "unset" → broker default).
func (o *kafkaGoOps) ensureTopics(ctx context.Context, destinationID string, timeout time.Duration, targets []KafkaTarget, logger *slog.Logger) error {
	var specs []kafka.TopicConfig
	for _, t := range targets {
		if !t.Create {
			continue
		}
		parts := t.Partitions
		if parts <= 0 {
			parts = -1
		}
		rf := t.ReplicationFactor
		if rf <= 0 {
			rf = -1
		}
		specs = append(specs, kafka.TopicConfig{Topic: t.Topic, NumPartitions: parts, ReplicationFactor: rf})
	}
	if len(specs) == 0 {
		return nil
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	resp, err := o.client.CreateTopics(ctx, &kafka.CreateTopicsRequest{Topics: specs})
	if err != nil {
		return fmt.Errorf("kafka destination %q: create topics: %w", destinationID, err)
	}
	for topic, topicErr := range resp.Errors {
		if topicErr != nil && !errors.Is(topicErr, kafka.TopicAlreadyExists) {
			return fmt.Errorf("kafka destination %q: create topic %q: %w", destinationID, topic, topicErr)
		}
		logger.Info("ensured kafka topic", slog.String("destination", destinationID), slog.String("topic", topic))
	}
	return nil
}
