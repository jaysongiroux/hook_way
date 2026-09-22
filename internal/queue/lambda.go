package queue

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/lambda"
	lambdatypes "github.com/aws/aws-sdk-go-v2/service/lambda/types"

	"github.com/jaysongiroux/hook_way/internal/vars"
)

// AWS Invoke payload limits (synchronous payload for RequestResponse is 6 MiB;
// async Event invoke accepts up to 256 KiB).
const (
	lambdaEventPayloadMaxBytes           = 256 * 1024
	lambdaRequestResponsePayloadMaxBytes = 6 * 1024 * 1024
)

type lambdaAPI interface {
	Invoke(ctx context.Context, in *lambda.InvokeInput, optFns ...func(*lambda.Options)) (*lambda.InvokeOutput, error)
	GetFunction(ctx context.Context, in *lambda.GetFunctionInput, optFns ...func(*lambda.Options)) (*lambda.GetFunctionOutput, error)
}

// LambdaTarget is one function this destination may invoke. Name is the
// FunctionName passed to Invoke (name, name:qualifier, or ARN).
type LambdaTarget struct {
	Name string
	// Template is set for dynamic routes (Name contains {variable}
	// placeholders). Rendered names the matcher accepts are invoked on first
	// sight; Hookway never creates functions.
	Template vars.Matcher
}

// LambdaConfig is the runtime config for a Lambda destination publisher.
type LambdaConfig struct {
	Region          string
	Endpoint        string
	AccessKeyID     string
	SecretAccessKey string
	InvocationType  string // "" | Event | RequestResponse; empty means Event
	DestinationID   string
	Logger          *slog.Logger
	Targets         []LambdaTarget
}

type lambdaTargetState struct {
	name string
	tmpl LambdaTarget
}

// LambdaPublisher invokes AWS Lambda functions with the JSON Message envelope.
type LambdaPublisher struct {
	client         lambdaAPI
	destID         string
	invocationType lambdatypes.InvocationType
	logger         *slog.Logger
	targets        *targetResolver[lambdaTargetState]
	probes         []string // static FunctionNames used by Ready
}

// NewLambdaPublisher registers static and dynamic targets. create is ignored:
// Lambda functions must already exist.
func NewLambdaPublisher(client lambdaAPI, cfg LambdaConfig) *LambdaPublisher {
	logger := cfg.Logger
	if logger == nil {
		logger = slog.Default()
	}
	invType := lambdatypes.InvocationTypeEvent
	if cfg.InvocationType == "RequestResponse" {
		invType = lambdatypes.InvocationTypeRequestResponse
	}
	p := &LambdaPublisher{
		client:         client,
		destID:         cfg.DestinationID,
		invocationType: invType,
		logger:         logger,
		targets:        newTargetResolver[lambdaTargetState](0, logger),
	}
	for _, target := range cfg.Targets {
		if target.Template.IsTemplate() {
			p.targets.addTemplate(target.Template, lambdaTargetState{tmpl: target})
			continue
		}
		p.targets.addStatic(target.Name, lambdaTargetState{name: target.Name, tmpl: target})
		p.probes = append(p.probes, target.Name)
	}
	return p
}

func provisionLambdaTarget(_ context.Context, base lambdaTargetState, rendered string) (lambdaTargetState, error) {
	base.name = rendered
	return base, nil
}

func (p *LambdaPublisher) Publish(ctx context.Context, destinationID, target string, msg Message) error {
	state, err := p.targets.resolve(ctx, target, provisionLambdaTarget)
	if err != nil {
		if errors.Is(err, errTargetNotConfigured) {
			return fmt.Errorf("lambda target %q is not configured", target)
		}
		return err
	}
	body, err := json.Marshal(msg)
	if err != nil {
		return err
	}
	limit := lambdaEventPayloadMaxBytes
	if p.invocationType == lambdatypes.InvocationTypeRequestResponse {
		limit = lambdaRequestResponsePayloadMaxBytes
	}
	if len(body) > limit {
		return fmt.Errorf("lambda payload is %d bytes; %s invoke limit is %d bytes",
			len(body), p.invocationType, limit)
	}
	out, err := p.client.Invoke(ctx, &lambda.InvokeInput{
		FunctionName:   aws.String(state.name),
		InvocationType: p.invocationType,
		Payload:        body,
	})
	if err != nil {
		return fmt.Errorf("lambda invoke %q: %w", state.name, err)
	}
	// Event mode returns StatusCode 202 with an empty payload; FunctionError
	// is only set for RequestResponse when the function itself failed.
	if out.FunctionError != nil && *out.FunctionError != "" {
		return fmt.Errorf("lambda invoke %q: function error %s: %s",
			state.name, *out.FunctionError, string(out.Payload))
	}
	return nil
}

// Ready confirms static functions exist via GetFunction. Dynamic targets are
// not probed (they are unknown until first render).
func (p *LambdaPublisher) Ready(ctx context.Context) error {
	var errs []error
	for _, name := range p.probes {
		if _, err := p.client.GetFunction(ctx, &lambda.GetFunctionInput{
			FunctionName: aws.String(name),
		}); err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", name, err))
		}
	}
	return errors.Join(errs...)
}

func (p *LambdaPublisher) Close() error { return nil }
