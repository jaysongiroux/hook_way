package queue

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
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
	Targets         []SQSTarget
}

type sqsTargetState struct {
	url  string
	fifo *FIFOSettings
}

type SQSPublisher struct {
	client  sqsAPI
	targets map[string]sqsTargetState
	probes  []string // resolved queue URLs used by Ready
}

// NewSQSPublisher resolves (and optionally creates) every target queue up
// front, caching the queue URLs so publishing does no extra lookups.
func NewSQSPublisher(ctx context.Context, client sqsAPI, cfg SQSConfig) (*SQSPublisher, error) {
	p := &SQSPublisher{client: client, targets: map[string]sqsTargetState{}}
	for _, target := range cfg.Targets {
		url, err := p.resolveQueueURL(ctx, target)
		if err != nil {
			return nil, fmt.Errorf("sqs target %q: %w", target.Name, err)
		}
		if !target.Create && strings.HasSuffix(target.Name, ".fifo") {
			if err := p.verifyContentBasedDedup(ctx, url); err != nil {
				return nil, fmt.Errorf("sqs target %q: %w", target.Name, err)
			}
		}
		p.targets[target.Name] = sqsTargetState{url: url, fifo: target.FIFO}
		p.probes = append(p.probes, url)
	}
	return p, nil
}

func (p *SQSPublisher) verifyContentBasedDedup(ctx context.Context, queueURL string) error {
	out, err := p.client.GetQueueAttributes(ctx, &sqs.GetQueueAttributesInput{
		QueueUrl:       aws.String(queueURL),
		AttributeNames: []sqstypes.QueueAttributeName{sqstypes.QueueAttributeNameContentBasedDeduplication},
	})
	if err != nil {
		return fmt.Errorf("GetQueueAttributes: %w", err)
	}
	if out.Attributes["ContentBasedDeduplication"] != "true" {
		return fmt.Errorf("queue does not have ContentBasedDeduplication enabled; set it on the queue or use create:true")
	}
	return nil
}

// resolveQueueURL looks up the queue URL, creating the queue when it does not
// exist and Create is true. When the queue does not exist and Create is false
// it returns the error so NewSQSPublisher fails fast on a misconfigured target
// rather than deferring the failure to the first publish.
func (p *SQSPublisher) resolveQueueURL(ctx context.Context, target SQSTarget) (string, error) {
	out, err := p.client.GetQueueUrl(ctx, &sqs.GetQueueUrlInput{QueueName: aws.String(target.Name)})
	if err == nil {
		return aws.ToString(out.QueueUrl), nil
	}
	var notFound *sqstypes.QueueDoesNotExist
	if !errors.As(err, &notFound) || !target.Create {
		return "", err
	}
	created, err := p.client.CreateQueue(ctx, &sqs.CreateQueueInput{
		QueueName:  aws.String(target.Name),
		Attributes: sqsCreateAttributes(target),
	})
	if err != nil {
		return "", err
	}
	return aws.ToString(created.QueueUrl), nil
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
