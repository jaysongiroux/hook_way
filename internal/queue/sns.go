package queue

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/sns"
)

type snsAPI interface {
	CreateTopic(ctx context.Context, in *sns.CreateTopicInput, optFns ...func(*sns.Options)) (*sns.CreateTopicOutput, error)
	GetTopicAttributes(ctx context.Context, in *sns.GetTopicAttributesInput, optFns ...func(*sns.Options)) (*sns.GetTopicAttributesOutput, error)
	ListTopics(ctx context.Context, in *sns.ListTopicsInput, optFns ...func(*sns.Options)) (*sns.ListTopicsOutput, error)
	Publish(ctx context.Context, in *sns.PublishInput, optFns ...func(*sns.Options)) (*sns.PublishOutput, error)
}

type SNSTarget struct {
	Name    string
	Create  bool
	Subject string
	FIFO    *FIFOSettings
}

type SNSConfig struct {
	Region          string
	Endpoint        string
	AccessKeyID     string
	SecretAccessKey string
	Targets         []SNSTarget
}

type snsTargetState struct {
	arn     string
	subject string
	fifo    *FIFOSettings
}

type SNSPublisher struct {
	client  snsAPI
	targets map[string]snsTargetState
	probes  []string // resolved topic ARNs used by Ready
}

// NewSNSPublisher resolves (and optionally creates) each topic ARN up front.
// The targets map is written once here and is read-only in Publish, making
// concurrent fan-out publishes safe without locking.
func NewSNSPublisher(ctx context.Context, client snsAPI, cfg SNSConfig) (*SNSPublisher, error) {
	p := &SNSPublisher{client: client, targets: map[string]snsTargetState{}}

	// Collect which targets need create vs. lookup.
	var needLookup []SNSTarget
	for _, target := range cfg.Targets {
		if target.Create {
			out, err := client.CreateTopic(ctx, &sns.CreateTopicInput{
				Name:       aws.String(target.Name),
				Attributes: snsCreateAttributes(target),
			})
			if err != nil {
				return nil, fmt.Errorf("sns target %q: %w", target.Name, err)
			}
			arn := aws.ToString(out.TopicArn)
			p.targets[target.Name] = snsTargetState{arn: arn, subject: target.Subject, fifo: target.FIFO}
			p.probes = append(p.probes, arn)
		} else {
			needLookup = append(needLookup, target)
		}
	}

	// If there are any create:false targets, page ListTopics once and build a map.
	if len(needLookup) > 0 {
		arnByName := map[string]string{} // topic name (after last ":") -> full ARN
		var token *string
		for {
			out, err := client.ListTopics(ctx, &sns.ListTopicsInput{NextToken: token})
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
				break
			}
			token = out.NextToken
		}
		for _, target := range needLookup {
			arn, ok := arnByName[target.Name]
			if !ok {
				return nil, fmt.Errorf("sns target %q: topic %q not found", target.Name, target.Name)
			}
			if strings.HasSuffix(target.Name, ".fifo") {
				if err := p.verifyTopicContentBasedDedup(ctx, arn); err != nil {
					return nil, fmt.Errorf("sns target %q: %w", target.Name, err)
				}
			}
			p.targets[target.Name] = snsTargetState{arn: arn, subject: target.Subject, fifo: target.FIFO}
			p.probes = append(p.probes, arn)
		}
	}

	return p, nil
}

func (p *SNSPublisher) verifyTopicContentBasedDedup(ctx context.Context, topicARN string) error {
	out, err := p.client.GetTopicAttributes(ctx, &sns.GetTopicAttributesInput{TopicArn: aws.String(topicARN)})
	if err != nil {
		return fmt.Errorf("GetTopicAttributes: %w", err)
	}
	if out.Attributes["ContentBasedDeduplication"] != "true" {
		return fmt.Errorf("topic does not have ContentBasedDeduplication enabled; set it on the topic or use create:true")
	}
	return nil
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
	// targets is written only in NewSNSPublisher (single goroutine); this read
	// is safe to perform concurrently across the server's publish fan-out.
	state, ok := p.targets[target]
	if !ok {
		return fmt.Errorf("sns target %q is not configured", target)
	}
	body, err := json.Marshal(msg)
	if err != nil {
		return err
	}
	in := &sns.PublishInput{
		TopicArn: aws.String(state.arn),
		Message:  aws.String(string(body)),
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
