package queue

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/sns"

	"github.com/jasongiroux/hookway/internal/vars"
)

type snsAPI interface {
	CreateTopic(ctx context.Context, in *sns.CreateTopicInput, optFns ...func(*sns.Options)) (*sns.CreateTopicOutput, error)
	GetTopicAttributes(ctx context.Context, in *sns.GetTopicAttributesInput, optFns ...func(*sns.Options)) (*sns.GetTopicAttributesOutput, error)
	SetTopicAttributes(ctx context.Context, in *sns.SetTopicAttributesInput, optFns ...func(*sns.Options)) (*sns.SetTopicAttributesOutput, error)
	ListTopics(ctx context.Context, in *sns.ListTopicsInput, optFns ...func(*sns.Options)) (*sns.ListTopicsOutput, error)
	Publish(ctx context.Context, in *sns.PublishInput, optFns ...func(*sns.Options)) (*sns.PublishOutput, error)
}

type SNSTarget struct {
	Name    string
	Create  bool
	Subject string
	FIFO    *FIFOSettings
	// Template is set for dynamic routes (Name contains {variable}
	// placeholders). Rendered topic names the matcher accepts are looked up
	// (and created when Create is set) on first sight with these settings.
	Template vars.Matcher
}

type SNSConfig struct {
	Region          string
	Endpoint        string
	AccessKeyID     string
	SecretAccessKey string
	DestinationID   string
	Policy          ConflictPolicy
	Logger          *slog.Logger
	Targets         []SNSTarget
}

// snsMutableAttrs is the set of SNS topic attributes managed by hook_way.
// true = mutable (can be changed on an existing topic), false = immutable.
var snsMutableAttrs = map[string]bool{
	"ContentBasedDeduplication": true,
	"FifoTopic":                 false,
}

type snsTargetState struct {
	arn     string
	subject string
	fifo    *FIFOSettings
	tmpl    SNSTarget // authored target for dynamic routes
}

type SNSPublisher struct {
	client  snsAPI
	destID  string
	policy  ConflictPolicy
	logger  *slog.Logger
	targets *targetResolver[snsTargetState]
	probes  []string // resolved static topic ARNs used by Ready
}

// NewSNSPublisher resolves (and optionally creates) each topic ARN up front.
// The targets map is written once here and is read-only in Publish, making
// concurrent fan-out publishes safe without locking.
func NewSNSPublisher(ctx context.Context, client snsAPI, cfg SNSConfig) (*SNSPublisher, error) {
	logger := cfg.Logger
	if logger == nil {
		logger = slog.Default()
	}
	p := &SNSPublisher{client: client, destID: cfg.DestinationID, policy: cfg.Policy, logger: logger, targets: newTargetResolver[snsTargetState](0, logger)}

	arnByName, err := p.listTopicARNs(ctx)
	if err != nil {
		return nil, err
	}

	for _, target := range cfg.Targets {
		if target.Template.IsTemplate() {
			p.targets.addTemplate(target.Template, snsTargetState{tmpl: target})
			continue
		}
		state, err := p.provisionTopic(ctx, target, arnByName)
		if err != nil {
			return nil, err
		}
		p.targets.addStatic(target.Name, state)
		p.probes = append(p.probes, state.arn)
	}
	return p, nil
}

// provisionTopic finds the topic in arnByName (reconciling it), creates it
// when Create is set, or fails. Used at startup and on first sight of a
// rendered dynamic target.
func (p *SNSPublisher) provisionTopic(ctx context.Context, target SNSTarget, arnByName map[string]string) (snsTargetState, error) {
	arn, existed := arnByName[target.Name]
	switch {
	case existed:
		p.logger.Debug("topic exists", slog.String("destination", p.destID), slog.String("target", target.Name))
		if err := p.reconcileTopic(ctx, target, arn); err != nil {
			return snsTargetState{}, err
		}
	case target.Create:
		out, err := p.client.CreateTopic(ctx, &sns.CreateTopicInput{
			Name:       aws.String(target.Name),
			Attributes: snsCreateAttributes(target),
		})
		if err != nil {
			return snsTargetState{}, fmt.Errorf("sns target %q: %w", target.Name, err)
		}
		arn = aws.ToString(out.TopicArn)
		p.logger.Info("created topic", slog.String("destination", p.destID), slog.String("target", target.Name))
	default:
		return snsTargetState{}, fmt.Errorf("sns target %q: topic %q not found", target.Name, target.Name)
	}
	return snsTargetState{arn: arn, subject: target.Subject, fifo: target.FIFO}, nil
}

// provisionDynamic is the resolver hook for a rendered dynamic target. ARNs
// cannot be derived from a name without the account id, so it re-lists topics
// (once per new rendered name; the result is cached by the resolver).
func (p *SNSPublisher) provisionDynamic(ctx context.Context, base snsTargetState, rendered string) (snsTargetState, error) {
	arnByName, err := p.listTopicARNs(ctx)
	if err != nil {
		return snsTargetState{}, err
	}
	target := base.tmpl
	target.Name = rendered
	return p.provisionTopic(ctx, target, arnByName)
}

// listTopicARNs pages ListTopics once and maps topic name (the part after the
// final ":") to its full ARN.
func (p *SNSPublisher) listTopicARNs(ctx context.Context) (map[string]string, error) {
	arnByName := map[string]string{}
	var token *string
	for {
		out, err := p.client.ListTopics(ctx, &sns.ListTopicsInput{NextToken: token})
		if err != nil {
			return nil, fmt.Errorf("sns ListTopics: %w", err)
		}
		for _, topic := range out.Topics {
			arn := aws.ToString(topic.TopicArn)
			if idx := strings.LastIndex(arn, ":"); idx >= 0 {
				arnByName[arn[idx+1:]] = arn
			}
		}
		if out.NextToken == nil {
			return arnByName, nil
		}
		token = out.NextToken
	}
}

// reconcileTopic fetches the existing topic's attributes and applies the
// configured conflict policy. SNS SetTopicAttributes sets one attribute per
// call, so the setAttrs closure loops over the mutable differences.
func (p *SNSPublisher) reconcileTopic(ctx context.Context, target SNSTarget, arn string) error {
	out, err := p.client.GetTopicAttributes(ctx, &sns.GetTopicAttributesInput{TopicArn: aws.String(arn)})
	if err != nil {
		return fmt.Errorf("sns target %q: GetTopicAttributes: %w", target.Name, err)
	}
	return applyConflictPolicy(ctx, reconcileParams{
		policy:  p.policy,
		logger:  p.logger,
		destID:  p.destID,
		target:  target.Name,
		want:    snsCreateAttributes(target),
		got:     out.Attributes,
		mutable: snsMutableAttrs,
		setAttrs: func(ctx context.Context, mutableWant map[string]string) error {
			for name, value := range mutableWant {
				if _, err := p.client.SetTopicAttributes(ctx, &sns.SetTopicAttributesInput{
					TopicArn:       aws.String(arn),
					AttributeName:  aws.String(name),
					AttributeValue: aws.String(value),
				}); err != nil {
					return err
				}
			}
			return nil
		},
	})
}

func snsCreateAttributes(target SNSTarget) map[string]string {
	attrs := map[string]string{}
	if strings.HasSuffix(target.Name, ".fifo") {
		attrs["FifoTopic"] = "true"
		if target.FIFO != nil && target.FIFO.DedupContentBasedFallback {
			attrs["ContentBasedDeduplication"] = "true"
		}
	}
	return attrs
}

func (p *SNSPublisher) Publish(ctx context.Context, destinationID, target string, msg Message) error {
	state, err := p.targets.resolve(ctx, target, p.provisionDynamic)
	if err != nil {
		if errors.Is(err, errTargetNotConfigured) {
			return fmt.Errorf("sns target %q is not configured", target)
		}
		return err
	}
	body, err := json.Marshal(msg)
	if err != nil {
		return err
	}
	attrs, err := snsMessageAttributes(msg.MessageAttributes)
	if err != nil {
		return err
	}
	in := &sns.PublishInput{
		TopicArn:          aws.String(state.arn),
		Message:           aws.String(string(body)),
		MessageAttributes: attrs,
	}
	if state.subject != "" && state.fifo == nil {
		in.Subject = aws.String(state.subject)
	}
	if state.fifo != nil {
		groupID, dedupID, _ := resolveFIFO(msg, *state.fifo)
		in.MessageGroupId = aws.String(groupID)
		if dedupID != "" {
			in.MessageDeduplicationId = aws.String(dedupID)
		}
	}
	_, err = p.client.Publish(ctx, in)
	return err
}

// Ready confirms all resolved topics are reachable. With no targets it is a no-op.
func (p *SNSPublisher) Ready(ctx context.Context) error {
	var errs []error
	for _, arn := range p.probes {
		if _, err := p.client.GetTopicAttributes(ctx, &sns.GetTopicAttributesInput{TopicArn: aws.String(arn)}); err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", arn, err))
		}
	}
	return errors.Join(errs...)
}

func (p *SNSPublisher) Close() error { return nil }
