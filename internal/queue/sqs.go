package queue

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"maps"
	"strconv"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/sqs"
	sqstypes "github.com/aws/aws-sdk-go-v2/service/sqs/types"

	"github.com/jaysongiroux/hook_way/internal/config"
	"github.com/jaysongiroux/hook_way/internal/vars"
)

type sqsAPI interface {
	GetQueueUrl(ctx context.Context, in *sqs.GetQueueUrlInput, optFns ...func(*sqs.Options)) (*sqs.GetQueueUrlOutput, error)
	CreateQueue(ctx context.Context, in *sqs.CreateQueueInput, optFns ...func(*sqs.Options)) (*sqs.CreateQueueOutput, error)
	GetQueueAttributes(ctx context.Context, in *sqs.GetQueueAttributesInput, optFns ...func(*sqs.Options)) (*sqs.GetQueueAttributesOutput, error)
	SetQueueAttributes(ctx context.Context, in *sqs.SetQueueAttributesInput, optFns ...func(*sqs.Options)) (*sqs.SetQueueAttributesOutput, error)
	SendMessage(ctx context.Context, in *sqs.SendMessageInput, optFns ...func(*sqs.Options)) (*sqs.SendMessageOutput, error)
}

type SQSTarget struct {
	QueueOwnerAccountID string
	Attributes          map[string]string
	Name                string
	Create              bool
	VisibilityTimeout   time.Duration
	MessageRetention    time.Duration
	FIFO                *FIFOSettings
	// Template is set for dynamic routes (Name contains {variable}
	// placeholders). Rendered queue names the matcher accepts are resolved
	// (and created when Create is set) on first sight with these settings.
	Template vars.Matcher
}

type SQSConfig struct {
	// AdmissionBudget tunes the native connector's idle pool; injected APIs
	// remain caller-owned. This is derived serving metadata, not authored YAML.
	AdmissionBudget int
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
	// tmpl carries the authored target for dynamic routes so provisioning a
	// rendered name can reuse its create flag, attributes, and FIFO settings.
	tmpl SQSTarget
}

type SQSPublisher struct {
	client  sqsAPI
	destID  string
	policy  ConflictPolicy
	logger  *slog.Logger
	targets *targetResolver[sqsTargetState]
	probes  []string // resolved static queue URLs used by Ready
}

// NewSQSPublisher resolves (and optionally creates) every target queue up
// front, caching the queue URLs so publishing does no extra lookups.
func NewSQSPublisher(ctx context.Context, client sqsAPI, cfg SQSConfig) (*SQSPublisher, error) {
	logger := cfg.Logger
	if logger == nil {
		logger = slog.Default()
	}
	p := &SQSPublisher{client: client, destID: cfg.DestinationID, policy: cfg.Policy, logger: logger, targets: newTargetResolver[sqsTargetState](0, logger)}
	for _, target := range cfg.Targets {
		if target.Template.IsTemplate() {
			// Dynamic route: nothing to resolve until a rendered name arrives.
			p.targets.addTemplate(target.Template, sqsTargetState{tmpl: target})
			continue
		}
		state, err := p.provisionQueue(ctx, target)
		if err != nil {
			return nil, err
		}
		p.targets.addStatic(target.Name, state)
		p.probes = append(p.probes, state.url)
	}
	return p, nil
}

// provisionQueue resolves (creating when Create is set) and reconciles one
// queue, returning its publish state. Used at startup for static targets and
// on first sight for rendered dynamic targets.
func (p *SQSPublisher) provisionQueue(ctx context.Context, target SQSTarget) (sqsTargetState, error) {
	if err := config.ValidateBrokerAttributes("sqs", strings.HasSuffix(target.Name, ".fifo"), target.Attributes); err != nil {
		return sqsTargetState{}, err
	}
	url, existed, err := p.resolveQueueURL(ctx, target)
	if err != nil {
		return sqsTargetState{}, fmt.Errorf("sqs target %q: %w", target.Name, err)
	}
	if existed {
		if err := p.reconcileQueue(ctx, target, url); err != nil {
			return sqsTargetState{}, err
		}
	}
	return sqsTargetState{url: url, fifo: target.FIFO}, nil
}

// provisionDynamic is the resolver hook for a rendered dynamic target: the
// template's settings with the rendered queue name.
func (p *SQSPublisher) provisionDynamic(ctx context.Context, base sqsTargetState, rendered string) (sqsTargetState, error) {
	target := base.tmpl
	target.Name = rendered
	return p.provisionQueue(ctx, target)
}

// resolveQueueURL returns the queue URL and whether it already existed. When the
// queue is absent and Create is false it errors (fail-fast on misconfiguration).
func (p *SQSPublisher) resolveQueueURL(ctx context.Context, target SQSTarget) (url string, existed bool, err error) {
	input := &sqs.GetQueueUrlInput{QueueName: aws.String(target.Name)}
	if target.QueueOwnerAccountID != "" {
		input.QueueOwnerAWSAccountId = aws.String(target.QueueOwnerAccountID)
	}
	out, err := p.client.GetQueueUrl(ctx, input)
	if err == nil {
		p.logger.Debug("queue exists", slog.String("destination", p.destID), slog.String("target", target.Name))
		return aws.ToString(out.QueueUrl), true, nil
	}
	var notFound *sqstypes.QueueDoesNotExist
	if !errors.As(err, &notFound) || !target.Create || target.QueueOwnerAccountID != "" {
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
// configured ones and applies the destination's conflict policy.
func (p *SQSPublisher) reconcileQueue(ctx context.Context, target SQSTarget, url string) error {
	out, err := p.client.GetQueueAttributes(ctx, &sqs.GetQueueAttributesInput{
		QueueUrl:       aws.String(url),
		AttributeNames: []sqstypes.QueueAttributeName{sqstypes.QueueAttributeNameAll},
	})
	if err != nil {
		return fmt.Errorf("sqs target %q: GetQueueAttributes: %w", target.Name, err)
	}
	return applyConflictPolicy(ctx, reconcileParams{
		policy:  p.policy,
		logger:  p.logger,
		destID:  p.destID,
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
	attrs := maps.Clone(target.Attributes)
	if attrs == nil {
		attrs = map[string]string{}
	}
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
	state, err := p.targets.resolve(ctx, target, p.provisionDynamic)
	if err != nil {
		if errors.Is(err, errTargetNotConfigured) {
			return fmt.Errorf("sqs target %q is not configured", target)
		}
		return err
	}
	body, err := BrokerBody(msg)
	if err != nil {
		return err
	}
	attrs, err := sqsMessageAttributes(msg.MessageAttributes)
	if err != nil {
		return err
	}
	in := &sqs.SendMessageInput{
		QueueUrl:          aws.String(state.url),
		MessageBody:       aws.String(string(body)),
		MessageAttributes: attrs,
	}
	if msg.Publish != nil && msg.Publish.DelaySeconds != nil {
		if strings.HasSuffix(target, ".fifo") || *msg.Publish.DelaySeconds < 0 || *msg.Publish.DelaySeconds > 900 {
			return fmt.Errorf("invalid per-message delay for SQS target")
		}
		in.DelaySeconds = *msg.Publish.DelaySeconds
	}
	if err := validateSQSBody(body); err != nil {
		return err
	}
	if err := validateBrokerSize(body, msg.MessageAttributes, 1048576); err != nil {
		return err
	}
	groupID, dedupID, err := brokerFIFO(msg, target, state.fifo)
	if err != nil {
		return err
	}
	if groupID != "" {
		in.MessageGroupId = aws.String(groupID)
	}
	if dedupID != "" {
		in.MessageDeduplicationId = aws.String(dedupID)
	}

	return brokerRetry(ctx, msg, func(explicit bool) error {
		var options []func(*sqs.Options)
		if explicit {
			options = append(options, func(o *sqs.Options) { o.Retryer = aws.NopRetryer{} })
		}
		_, err := p.client.SendMessage(ctx, in, options...)
		return err
	})
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

func init() {
	for key, mutable := range config.BrokerMutableAttributes("sqs") {
		sqsMutableAttrs[key] = mutable
	}
}
