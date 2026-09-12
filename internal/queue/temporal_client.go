package queue

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"log/slog"
	"time"

	enumspb "go.temporal.io/api/enums/v1"
	"go.temporal.io/api/serviceerror"
	"go.temporal.io/sdk/client"
	sdklog "go.temporal.io/sdk/log"

	"github.com/jasongiroux/hookway/internal/config"
)

// errTemporalAlreadyStarted is the sentinel sdkTemporalOps.StartWorkflow
// returns when the server reports WorkflowExecutionAlreadyStarted. The
// publisher treats it as a no-op so at-least-once redelivery of an event with
// a deterministic workflow id does not fail the sender (first-start-wins).
var errTemporalAlreadyStarted = errors.New("temporal: workflow execution already started")

// temporalStartOptions carries everything the publisher resolved for one start
// call, expressed in config vocabulary so the publisher stays SDK-free.
type temporalStartOptions struct {
	WorkflowID       string
	TaskQueue        string
	WorkflowType     string
	IDReusePolicy    string // "" | allow_duplicate | allow_duplicate_failed_only | reject_duplicate | terminate_if_running
	ExecutionTimeout time.Duration
	RunTimeout       time.Duration
	TaskTimeout      time.Duration
}

// temporalOps is the thin, mockable interface the Temporal publisher depends
// on (mongoOps pattern). This file is the only place the Temporal SDK is
// imported so internal/config, and therefore the WASM validator, never pull
// gRPC/protobuf into their build graph.
type temporalOps interface {
	// StartWorkflow starts a workflow execution and returns its run id. A
	// WorkflowExecutionAlreadyStarted error is returned as
	// errTemporalAlreadyStarted so the caller can treat it as a no-op.
	StartWorkflow(ctx context.Context, opts temporalStartOptions, input any) (runID string, err error)
	// CheckHealth probes the frontend's gRPC health service.
	CheckHealth(ctx context.Context) error
	// Close releases the gRPC connection.
	Close() error
}

// sdkTemporalOps wraps a client.Client and implements temporalOps.
type sdkTemporalOps struct {
	client client.Client
}

func (o *sdkTemporalOps) StartWorkflow(ctx context.Context, opts temporalStartOptions, input any) (string, error) {
	policy, err := mapWorkflowIDReusePolicy(opts.IDReusePolicy)
	if err != nil {
		return "", err
	}
	run, err := o.client.ExecuteWorkflow(ctx, client.StartWorkflowOptions{
		ID:                       opts.WorkflowID,
		TaskQueue:                opts.TaskQueue,
		WorkflowExecutionTimeout: opts.ExecutionTimeout,
		WorkflowRunTimeout:       opts.RunTimeout,
		WorkflowTaskTimeout:      opts.TaskTimeout,
		WorkflowIDReusePolicy:    policy,
	}, opts.WorkflowType, input)
	if err != nil {
		return "", mapTemporalStartError(err)
	}
	return run.GetRunID(), nil
}

func (o *sdkTemporalOps) CheckHealth(ctx context.Context) error {
	_, err := o.client.CheckHealth(ctx, &client.CheckHealthRequest{})
	return err
}

func (o *sdkTemporalOps) Close() error {
	o.client.Close()
	return nil
}

// newTemporalOps dials the Temporal frontend from the destination's config.
// The Dial is bounded by ConnectTimeout and performs a system-info RPC, so a
// returned client is known to be reachable (fail-fast at startup, matching the
// mongo/postgres destinations). It logs a warning when neither TLS nor an API
// key is configured (plaintext, unauthenticated connection).
func newTemporalOps(ctx context.Context, destinationID string, cfg config.TemporalConfig, logger *slog.Logger) (*sdkTemporalOps, error) {
	if logger == nil {
		logger = slog.Default()
	}
	tlsCfg, err := buildTemporalTLSConfig(cfg.TLS)
	if err != nil {
		return nil, err
	}
	if tlsCfg == nil && cfg.APIKey == "" {
		logger.Warn("temporal destination connecting with no TLS or API key; payloads will traverse the network in plaintext",
			slog.String("destination", destinationID))
	}
	opts := client.Options{
		HostPort:  cfg.HostPort,
		Namespace: cfg.Namespace,
		Logger:    sdklog.NewStructuredLogger(logger.With(slog.String("destination", destinationID), slog.String("component", "temporal-sdk"))),
		ConnectionOptions: client.ConnectionOptions{
			TLS: tlsCfg,
		},
	}
	if cfg.APIKey != "" {
		opts.Credentials = client.NewAPIKeyStaticCredentials(cfg.APIKey)
	}
	connectTimeout := cfg.ConnectTimeout
	if connectTimeout <= 0 {
		connectTimeout = 10 * time.Second
	}
	dialCtx, cancel := context.WithTimeout(ctx, connectTimeout)
	defer cancel()
	c, err := client.DialContext(dialCtx, opts)
	if err != nil {
		return nil, fmt.Errorf("temporal: dial %s: %w", cfg.HostPort, err)
	}
	return &sdkTemporalOps{client: c}, nil
}

// buildTemporalTLSConfig assembles a *tls.Config from the configured options, or
// returns nil when TLS is not enabled.
func buildTemporalTLSConfig(c config.TemporalTLSConfig) (*tls.Config, error) {
	if !c.Enabled {
		return nil, nil
	}
	return newTLSConfig("temporal", tlsMaterial{CACert: c.CACert, ClientCert: c.ClientCert, ClientKey: c.ClientKey, ServerName: c.ServerName, InsecureSkipVerify: c.InsecureSkipVerify})
}

// mapWorkflowIDReusePolicy maps the config vocabulary onto the SDK enum. Empty
// yields UNSPECIFIED so the server default applies.
func mapWorkflowIDReusePolicy(s string) (enumspb.WorkflowIdReusePolicy, error) {
	switch s {
	case "":
		return enumspb.WORKFLOW_ID_REUSE_POLICY_UNSPECIFIED, nil
	case "allow_duplicate":
		return enumspb.WORKFLOW_ID_REUSE_POLICY_ALLOW_DUPLICATE, nil
	case "allow_duplicate_failed_only":
		return enumspb.WORKFLOW_ID_REUSE_POLICY_ALLOW_DUPLICATE_FAILED_ONLY, nil
	case "reject_duplicate":
		return enumspb.WORKFLOW_ID_REUSE_POLICY_REJECT_DUPLICATE, nil
	case "terminate_if_running":
		return enumspb.WORKFLOW_ID_REUSE_POLICY_TERMINATE_IF_RUNNING, nil
	default:
		return enumspb.WORKFLOW_ID_REUSE_POLICY_UNSPECIFIED, fmt.Errorf("unsupported temporal id_reuse_policy %q", s)
	}
}

// mapTemporalStartError translates the SDK's already-started service error
// into the package sentinel; every other error passes through unchanged.
func mapTemporalStartError(err error) error {
	if err == nil {
		return nil
	}
	var already *serviceerror.WorkflowExecutionAlreadyStarted
	if errors.As(err, &already) {
		return fmt.Errorf("%w: %s", errTemporalAlreadyStarted, already.Message)
	}
	return err
}
