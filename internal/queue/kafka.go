package queue

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"
)

// KafkaTarget describes one topic this destination routes to. PartitionKey is
// nil when no key is configured (messages are then round-robined).
type KafkaTarget struct {
	Topic             string
	Create            bool
	PartitionKey      *FIFOSettings
	Partitions        int
	ReplicationFactor int
}

type KafkaPublisherConfig struct {
	DestinationID  string
	PublishTimeout time.Duration
	Logger         *slog.Logger
	Targets        []KafkaTarget
}

// KafkaPublisher publishes webhook envelopes to Kafka topics. The targets map
// is written only in NewKafkaPublisher and is read-only during Publish, so
// concurrent fan-out reads are safe without locking.
type KafkaPublisher struct {
	ops            kafkaOps
	destID         string
	publishTimeout time.Duration
	logger         *slog.Logger
	targets        map[string]KafkaTarget
}

func NewKafkaPublisher(ops kafkaOps, cfg KafkaPublisherConfig) *KafkaPublisher {
	logger := cfg.Logger
	if logger == nil {
		logger = slog.Default()
	}
	timeout := cfg.PublishTimeout
	if timeout <= 0 {
		timeout = 5 * time.Second
	}
	p := &KafkaPublisher{
		ops:            ops,
		destID:         cfg.DestinationID,
		publishTimeout: timeout,
		logger:         logger,
		targets:        make(map[string]KafkaTarget, len(cfg.Targets)),
	}
	for _, t := range cfg.Targets {
		p.targets[t.Topic] = t
	}
	return p
}

// Publish marshals msg to JSON and writes it to the target topic. When the
// target configures a partition key it is derived via resolveGroupID and set as
// the Kafka message key; otherwise the key is empty (round-robin).
func (p *KafkaPublisher) Publish(ctx context.Context, destinationQueueID, target string, msg Message) error {
	t, ok := p.targets[target]
	if !ok {
		return fmt.Errorf("kafka %q: target %q is not configured", p.destID, target)
	}
	body, err := json.Marshal(msg)
	if err != nil {
		return fmt.Errorf("kafka %q: marshal message: %w", p.destID, err)
	}
	var key []byte
	if t.PartitionKey != nil {
		if k := resolveGroupID(msg, *t.PartitionKey); k != "" {
			key = []byte(k)
		}
	}
	ctx, cancel := context.WithTimeout(ctx, p.publishTimeout)
	defer cancel()
	return p.ops.Write(ctx, target, key, body)
}

func (p *KafkaPublisher) Ready(ctx context.Context) error { return p.ops.Ready(ctx) }

func (p *KafkaPublisher) Close() error { return p.ops.Close() }
