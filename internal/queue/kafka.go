package queue

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/jasongiroux/hookway/internal/vars"
)

// KafkaTarget describes one topic this destination routes to. PartitionKey is
// nil when no key is configured (messages are then round-robined).
type KafkaTarget struct {
	Topic             string
	Create            bool
	PartitionKey      *FIFOSettings
	Partitions        int
	ReplicationFactor int
	// Template is set for dynamic routes (Topic contains {variable}
	// placeholders). Rendered topics the matcher accepts get these settings
	// and, when Create is set, are created on first sight.
	Template vars.Matcher
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
	targets        *targetResolver[KafkaTarget]
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
		targets:        newTargetResolver[KafkaTarget](0, logger),
	}
	for _, t := range cfg.Targets {
		if t.Template.IsTemplate() {
			p.targets.addTemplate(t.Template, t)
		} else {
			p.targets.addStatic(t.Topic, t)
		}
	}
	return p
}

// provisionTopic is the resolver hook for a rendered dynamic topic: the
// template's settings with the rendered name, created when Create is set.
func (p *KafkaPublisher) provisionTopic(ctx context.Context, base KafkaTarget, rendered string) (KafkaTarget, error) {
	t := base
	t.Topic = rendered
	if t.Create {
		ctx, cancel := context.WithTimeout(ctx, p.publishTimeout)
		defer cancel()
		if err := p.ops.EnsureTopics(ctx, []KafkaTarget{t}); err != nil {
			return KafkaTarget{}, err
		}
	}
	return t, nil
}

// Publish marshals msg to JSON and writes it to the target topic. When the
// target configures a partition key it is derived via resolveGroupID and set as
// the Kafka message key; otherwise the key is empty (round-robin).
func (p *KafkaPublisher) Publish(ctx context.Context, destinationQueueID, target string, msg Message) error {
	t, err := p.targets.resolve(ctx, target, p.provisionTopic)
	if err != nil {
		if errors.Is(err, errTargetNotConfigured) {
			return fmt.Errorf("kafka %q: target %q is not configured", p.destID, target)
		}
		return fmt.Errorf("kafka %q: provision topic %q: %w", p.destID, target, err)
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
