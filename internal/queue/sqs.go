package queue

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/sqs"
	sqstypes "github.com/aws/aws-sdk-go-v2/service/sqs/types"
)

type sqsAPI interface {
	GetQueueUrl(ctx context.Context, in *sqs.GetQueueUrlInput, optFns ...func(*sqs.Options)) (*sqs.GetQueueUrlOutput, error)
	CreateQueue(ctx context.Context, in *sqs.CreateQueueInput, optFns ...func(*sqs.Options)) (*sqs.CreateQueueOutput, error)
	GetQueueAttributes(ctx context.Context, in *sqs.GetQueueAttributesInput, optFns ...func(*sqs.Options)) (*sqs.GetQueueAttributesOutput, error)
	SetQueueAttributes(ctx context.Context, in *sqs.SetQueueAttributesInput, optFns ...func(*sqs.Options)) (*sqs.SetQueueAttributesOutput, error)
	SendMessage(ctx context.Context, in *sqs.SendMessageInput, optFns ...func(*sqs.Options)) (*sqs.SendMessageOutput, error)
}

type SQSTarget struct {
	Name              string
	Create            bool
	VisibilityTimeout time.Duration
	MessageRetention  time.Duration
	FIFO              *FIFOSettings
}

type SQSConfig struct {
	Region          string
	Endpoint        string
	AccessKeyID     string
	SecretAccessKey string
	DestinationID   string
	Policy          ConflictPolicy
	Logger          *slog.Logger
	Targets         []SQSTarget
}

var sqsMutableAttrs = map[string]bool{
	"VisibilityTimeout":         true,
	"MessageRetentionPeriod":    true,
	"ContentBasedDeduplication": true,
	"FifoQueue":                 false,
}

type sqsTargetState struct {
	url  string
	fifo *FIFOSettings
}

type SQSPublisher struct {
	client  sqsAPI
	destID  string
	logger  *slog.Logger
	targets map[string]sqsTargetState
	probes  []string // resolved queue URLs used by Ready
}

// NewSQSPublisher resolves (and optionally creates) every target queue up
// front, caching the queue URLs so publishing does no extra lookups.
func NewSQSPublisher(ctx context.Context, client sqsAPI, cfg SQSConfig) (*SQSPublisher, error) {
	logger := cfg.Logger
	if logger == nil {
		logger = slog.Default()
	}
	p := &SQSPublisher{client: client, destID: cfg.DestinationID, logger: logger, targets: map[string]sqsTargetState{}}
	for _, target := range cfg.Targets {
		url, existed, err := p.resolveQueueURL(ctx, target)
		if err != nil {
			return nil, fmt.Errorf("sqs target %q: %w", target.Name, err)
		}
		if existed {
			if err := p.reconcileQueue(ctx, cfg, target, url); err != nil {
				return nil, err
			}
		}
		p.targets[target.Name] = sqsTargetState{url: url, fifo: target.FIFO}
		p.probes = append(p.probes, url)
	}
	return p, nil
}

// resolveQueueURL returns the queue URL and whether it already existed. When the
// queue is absent and Create is false it errors (fail-fast on misconfiguration).
func (p *SQSPublisher) resolveQueueURL(ctx context.Context, target SQSTarget) (url string, existed bool, err error) {
	out, err := p.client.GetQueueUrl(ctx, &sqs.GetQueueUrlInput{QueueName: aws.String(target.Name)})
	if err == nil {
		p.logger.Debug("queue exists", slog.String("destination", p.destID), slog.String("target", target.Name))
		return aws.ToString(out.QueueUrl), true, nil
	}
	var notFound *sqstypes.QueueDoesNotExist
	if !errors.As(err, &notFound) || !target.Create {
		return "", false, err
	}
	created, err := p.client.CreateQueue(ctx, &sqs.CreateQueueInput{
		QueueName:  aws.String(target.Name),
		Attributes: sqsCreateAttributes(target),
	})
	if err != nil {
		return "", false, err
	}
	p.logger.Info("created queue", slog.String("destination", p.destID), slog.String("target", target.Name))
	return aws.ToString(created.QueueUrl), false, nil
}

// reconcileQueue compares the existing queue's managed attributes against the
// configured ones and applies cfg.Policy.
func (p *SQSPublisher) reconcileQueue(ctx context.Context, cfg SQSConfig, target SQSTarget, url string) error {
	out, err := p.client.GetQueueAttributes(ctx, &sqs.GetQueueAttributesInput{
		QueueUrl: aws.String(url),
		AttributeNames: []sqstypes.QueueAttributeName{
			sqstypes.QueueAttributeNameVisibilityTimeout,
			sqstypes.QueueAttributeNameMessageRetentionPeriod,
			sqstypes.QueueAttributeNameFifoQueue,
			sqstypes.QueueAttributeNameContentBasedDeduplication,
		},
	})
	if err != nil {
		return fmt.Errorf("sqs target %q: GetQueueAttributes: %w", target.Name, err)
	}
	return applyConflictPolicy(ctx, reconcileParams{
		policy:  cfg.Policy,
		logger:  p.logger,
		destID:  cfg.DestinationID,
		target:  target.Name,
		want:    sqsCreateAttributes(target),
		got:     out.Attributes,
		mutable: sqsMutableAttrs,
		setAttrs: func(ctx context.Context, mutableWant map[string]string) error {
			_, err := p.client.SetQueueAttributes(ctx, &sqs.SetQueueAttributesInput{
				QueueUrl:   aws.String(url),
				Attributes: mutableWant,
			})
			return err
		},
	})
}

func sqsCreateAttributes(target SQSTarget) map[string]string {
	attrs := map[string]string{}
	if target.VisibilityTimeout > 0 {
		attrs["VisibilityTimeout"] = strconv.Itoa(int(target.VisibilityTimeout.Seconds()))
	}
	if target.MessageRetention > 0 {
		attrs["MessageRetentionPeriod"] = strconv.Itoa(int(target.MessageRetention.Seconds()))
	}
	if strings.HasSuffix(target.Name, ".fifo") {
		attrs["FifoQueue"] = "true"
		if target.FIFO != nil && target.FIFO.DedupContentBasedFallback {
			attrs["ContentBasedDeduplication"] = "true"
		}
	}
	return attrs
}

func (p *SQSPublisher) Publish(ctx context.Context, destinationID, target string, msg Message) error {
	// targets is written only in NewSQSPublisher (single goroutine); this read
	// is safe to perform concurrently across the server's publish fan-out.
	state, ok := p.targets[target]
	if !ok {
		return fmt.Errorf("sqs target %q is not configured", target)
	}
	body, err := json.Marshal(msg)
	if err != nil {
		return err
	}
	in := &sqs.SendMessageInput{
		QueueUrl:    aws.String(state.url),
		MessageBody: aws.String(string(body)),
	}
	if state.fifo != nil {
		groupID, dedupID, _ := resolveFIFO(msg, *state.fifo)
		in.MessageGroupId = aws.String(groupID)
		if dedupID != "" {
			in.MessageDeduplicationId = aws.String(dedupID)
		}
		// When dedupID is empty, resolveFIFO is signaling content-based
		// deduplication. For queues we created we set ContentBasedDeduplication
		// at creation time; for pre-existing queues (Create=false) we assume the
		// operator has already enabled it on the queue.
	}
	_, err = p.client.SendMessage(ctx, in)
	return err
}

// Ready confirms all resolved queues are reachable. With no targets it is a no-op.
func (p *SQSPublisher) Ready(ctx context.Context) error {
	var errs []error
	for _, url := range p.probes {
		if _, err := p.client.GetQueueAttributes(ctx, &sqs.GetQueueAttributesInput{
			QueueUrl:       aws.String(url),
			AttributeNames: []sqstypes.QueueAttributeName{sqstypes.QueueAttributeNameQueueArn},
		}); err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", url, err))
		}
	}
	return errors.Join(errs...)
}

func (p *SQSPublisher) Close() error { return nil }
