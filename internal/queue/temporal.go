package queue

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/jasongiroux/hookway/internal/vars"
)

// WorkflowIDSettings is the queue-package view of a route's workflow_id block.
// Source is static | endpoint | header | body; empty or unresolved falls back
// to the hookway event id. Prefix is prepended to the resolved value.
type WorkflowIDSettings struct {
	Source string
	Value  string
	Path   string
	Prefix string
	// prefix is Prefix parsed once by NewTemporalPublisher; nil (or no
	// placeholders) renders Prefix verbatim. A pointer keeps the struct
	// comparable for the factory's target dedup and tests.
	prefix *vars.Template
}

// withParsedPrefix returns s with Prefix parsed once so Publish only renders.
func (s WorkflowIDSettings) withParsedPrefix() (WorkflowIDSettings, error) {
	tpl, err := vars.Parse(s.Prefix)
	if err != nil {
		return s, err
	}
	s.prefix = &tpl
	return s, nil
}

// TemporalTarget describes one task queue this destination routes to, with
// the per-route workflow settings the factory copied from temporal_options.
type TemporalTarget struct {
	TaskQueue        string
	WorkflowType     string
	Input            string // "" | envelope | body
	WorkflowID       WorkflowIDSettings
	IDReusePolicy    string
	ExecutionTimeout time.Duration
	RunTimeout       time.Duration
	TaskTimeout      time.Duration
	// Template is set for dynamic routes (task queue contains {variable}
	// placeholders); rendered task queues the matcher accepts start this
	// target's workflow type with these settings.
	Template vars.Matcher
}

type TemporalPublisherConfig struct {
	DestinationID string
	StartTimeout  time.Duration
	Logger        *slog.Logger
	Targets       []TemporalTarget
}

// TemporalPublisher starts one workflow execution per webhook event. The
// targets map is written only in NewTemporalPublisher and is read-only during
// Publish, so concurrent fan-out reads are safe without locking.
type TemporalPublisher struct {
	ops          temporalOps
	destID       string
	startTimeout time.Duration
	logger       *slog.Logger
	targets      *targetResolver[TemporalTarget]
}

func NewTemporalPublisher(ops temporalOps, cfg TemporalPublisherConfig) *TemporalPublisher {
	logger := cfg.Logger
	if logger == nil {
		logger = slog.Default()
	}
	timeout := cfg.StartTimeout
	if timeout <= 0 {
		timeout = 5 * time.Second
	}
	p := &TemporalPublisher{
		ops:          ops,
		destID:       cfg.DestinationID,
		startTimeout: timeout,
		logger:       logger,
		targets:      newTargetResolver[TemporalTarget](0, logger),
	}
	for _, t := range cfg.Targets {
		var err error
		if t.WorkflowID, err = t.WorkflowID.withParsedPrefix(); err != nil {
			// Validation rejects a malformed prefix; a hand-built config keeps
			// the raw literal rather than failing every publish.
			logger.Error("temporal workflow_id prefix template is malformed; using it literally", "destination", cfg.DestinationID, "task_queue", t.TaskQueue, "error", err.Error())
		}
		if t.Template.IsTemplate() {
			p.targets.addTemplate(t.Template, t)
		} else {
			p.targets.addStatic(t.TaskQueue, t)
		}
	}
	return p
}

// provisionTaskQueue clones a dynamic route's settings for a rendered task queue.
func provisionTaskQueue(_ context.Context, base TemporalTarget, rendered string) (TemporalTarget, error) {
	base.TaskQueue = rendered
	return base, nil
}

// Publish starts the route's workflow type on the target task queue. The
// workflow id is derived from the route's workflow_id settings (defaulting to
// the event id) so sender retries with a deterministic id are idempotent: an
// already-started execution is logged and treated as delivered.
func (p *TemporalPublisher) Publish(ctx context.Context, destinationQueueID, target string, msg Message) error {
	t, err := p.targets.resolve(ctx, target, provisionTaskQueue)
	if err != nil {
		return fmt.Errorf("temporal %q: target %q is not configured", p.destID, target)
	}
	workflowID, err := resolveWorkflowID(msg, t.WorkflowID)
	if err != nil {
		return fmt.Errorf("temporal %q: workflow id for task queue %q: %w", p.destID, t.TaskQueue, err)
	}
	input := p.workflowInput(t, msg)

	ctx, cancel := context.WithTimeout(ctx, p.startTimeout)
	defer cancel()
	runID, err := p.ops.StartWorkflow(ctx, temporalStartOptions{
		WorkflowID:       workflowID,
		TaskQueue:        t.TaskQueue,
		WorkflowType:     t.WorkflowType,
		IDReusePolicy:    t.IDReusePolicy,
		ExecutionTimeout: t.ExecutionTimeout,
		RunTimeout:       t.RunTimeout,
		TaskTimeout:      t.TaskTimeout,
	}, input)
	if err != nil {
		if errors.Is(err, errTemporalAlreadyStarted) {
			p.logger.Debug("skipped duplicate workflow start",
				slog.String("destination", p.destID),
				slog.String("target", target),
				slog.String("event_id", msg.ID),
				slog.String("workflow_id", workflowID))
			return nil
		}
		return fmt.Errorf("temporal %q: start workflow %q on task queue %q: %w", p.destID, t.WorkflowType, t.TaskQueue, err)
	}
	p.logger.Debug("started workflow",
		slog.String("destination", p.destID),
		slog.String("target", target),
		slog.String("event_id", msg.ID),
		slog.String("workflow_id", workflowID),
		slog.String("run_id", runID))
	return nil
}

// workflowInput selects the single workflow argument. envelope (default) is
// the whole Message, which the SDK's JSON converter serialises exactly as the
// other destinations do. body is the decoded webhook body; when the body was
// not JSON (Body nil, RawBodyBase64 set) the envelope is sent instead so a
// JSON-typed worker still receives an object it can decode.
func (p *TemporalPublisher) workflowInput(t TemporalTarget, msg Message) any {
	if t.Input != "body" {
		return msg
	}
	if msg.Body == nil && msg.RawBodyBase64 != nil {
		p.logger.Warn("temporal body input unavailable for non-JSON body; sending envelope",
			slog.String("destination", p.destID),
			slog.String("target", t.TaskQueue),
			slog.String("event_id", msg.ID))
		return msg
	}
	return msg.Body
}

// resolveWorkflowID derives the workflow id: Prefix + the value resolved from
// Source, falling back to the event id when Source is unset or unresolved. A
// Prefix may contain {variable} placeholders (endpoint routing variables),
// rendered from msg.Variables; an unbound placeholder is an error because the
// same envelope can never succeed on retry.
func resolveWorkflowID(msg Message, s WorkflowIDSettings) (string, error) {
	value := resolveSourceValue(msg, s.Source, s.Value, s.Path)
	if value == "" {
		value = msg.ID
	}
	prefix := s.Prefix
	if s.prefix != nil && s.prefix.HasPlaceholders() {
		var err error
		if prefix, err = s.prefix.Render(msg.Variables); err != nil {
			return "", fmt.Errorf("prefix template: %w", err)
		}
	}
	return prefix + value, nil
}

func (p *TemporalPublisher) Ready(ctx context.Context) error { return p.ops.CheckHealth(ctx) }

func (p *TemporalPublisher) Close() error { return p.ops.Close() }
