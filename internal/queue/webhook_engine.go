package queue

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"runtime/debug"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/jasongiroux/hookway/internal/config"
)

var errWebhookQueueFull = errors.New("webhook: delivery queue full")
var errWebhookEngineClosing = errors.New("webhook: engine closing")

// webhookDelivery is the resolved per-destination delivery config (built by the publisher).
// (WebhookMetrics interface is defined in publisher.go)
type webhookDelivery struct {
	destID string
	target string
	url    string // resolved base+target
	mode   string // "envelope" | "proxy"
	client *http.Client
	retry  RetryPolicy
	auth   webhookAuth
	logger *slog.Logger
	// Dead-letter routing for terminal async failures. Empty => log-and-drop.
	dlqDestination string
	dlqTarget      string
}

type webhookJob struct {
	delivery *webhookDelivery
	msg      Message
}

// deadLetterFunc redirects a terminally-failed delivery to another destination.
type deadLetterFunc func(ctx context.Context, destID, target string, msg Message) error

type webhookDeliveryEngine struct {
	destID       string
	jobs         chan webhookJob
	wg           sync.WaitGroup
	mu           sync.RWMutex
	closed       bool
	closeOnce    sync.Once
	drainTimeout time.Duration
	metrics      WebhookMetrics
	logger       *slog.Logger
	deadLetter   deadLetterFunc // set post-build under mu via setDeadLetter
}

func newWebhookDeliveryEngine(destID string, workers, queueSize int, drain time.Duration, m WebhookMetrics, logger *slog.Logger) *webhookDeliveryEngine {
	if workers <= 0 {
		workers = 8
	}
	if queueSize <= 0 {
		queueSize = 1000
	}
	if drain <= 0 {
		drain = 10 * time.Second
	}
	if logger == nil {
		logger = slog.Default()
	}
	e := &webhookDeliveryEngine{
		destID: destID, jobs: make(chan webhookJob, queueSize), drainTimeout: drain, metrics: m, logger: logger,
	}
	for i := 0; i < workers; i++ {
		e.wg.Add(1)
		go e.worker()
	}
	return e
}

// setDeadLetter wires the resolver used to redirect terminal failures. It is set
// once during factory wiring (phase two), after all publishers are built.
func (e *webhookDeliveryEngine) setDeadLetter(fn deadLetterFunc) {
	e.mu.Lock()
	e.deadLetter = fn
	e.mu.Unlock()
}

// capacity is the fixed job-buffer capacity (used to publish the capacity gauge).
func (e *webhookDeliveryEngine) capacity() int { return cap(e.jobs) }

// saturated reports current depth/capacity and whether depth is at or above 90%
// of capacity. This is the readiness signal (plan item D4).
func (e *webhookDeliveryEngine) saturated() (depth, capacity int, isSaturated bool) {
	depth = len(e.jobs)
	capacity = cap(e.jobs)
	return depth, capacity, depth*10 >= capacity*9
}

func (e *webhookDeliveryEngine) worker() {
	defer e.wg.Done()
	for job := range e.jobs {
		e.deliverRecovered(job)
	}
}

// deliverRecovered runs deliver with a panic guard so one panicking delivery
// (e.g. a bug triggered by a malformed message or a misbehaving transport)
// logs and counts instead of killing this worker goroutine — which would
// silently shrink the engine's worker pool for every job still queued behind
// the panicking one.
func (e *webhookDeliveryEngine) deliverRecovered(job webhookJob) {
	defer func() {
		if r := recover(); r != nil {
			e.logger.Error("panic recovered in webhook delivery worker",
				"location", "webhook_worker",
				"destination", e.destID, "target", job.delivery.target,
				"event_id", job.msg.ID,
				"panic", fmt.Sprint(r), "stack", string(debug.Stack()))
			if e.metrics != nil {
				e.metrics.Panic("webhook_worker")
			}
		}
	}()
	e.deliver(job)
}

// Enqueue is non-blocking. Returns errWebhookQueueFull when the buffer is full,
// or errWebhookEngineClosing if the engine is closing. The RLock/Lock pairing
// with Close prevents a send on a closed channel.
func (e *webhookDeliveryEngine) Enqueue(job webhookJob) error {
	e.mu.RLock()
	defer e.mu.RUnlock()
	if e.closed {
		return errWebhookEngineClosing
	}
	select {
	case e.jobs <- job:
		if e.metrics != nil {
			e.metrics.WebhookQueueDepth(e.destID, len(e.jobs))
		}
		return nil
	default:
		return errWebhookQueueFull
	}
}

// Close is idempotent: it stops intake, closes the queue so workers drain
// remaining jobs, and waits up to drainTimeout for in-flight deliveries.
func (e *webhookDeliveryEngine) Close() error {
	e.closeOnce.Do(func() {
		e.mu.Lock()
		e.closed = true
		close(e.jobs)
		e.mu.Unlock()

		done := make(chan struct{})
		go func() { e.wg.Wait(); close(done) }()
		select {
		case <-done:
		case <-time.After(e.drainTimeout):
			e.logger.Warn("webhook engine drain timed out with deliveries still pending")
		}
	})
	return nil
}

// deliver runs one job to completion on a worker: attempt the delivery (with
// retry/backoff), report the resulting queue depth, and — on terminal failure —
// dispatch to the dead-letter destination (or log-and-drop when none is set).
func (e *webhookDeliveryEngine) deliver(job webhookJob) {
	if e.metrics != nil {
		e.metrics.WebhookQueueDepth(e.destID, len(e.jobs))
	}
	outcome, attempts, err := job.delivery.run(context.Background(), job.msg, e.metrics)
	if err == nil {
		return
	}
	e.dispatchDeadLetter(job, outcome, attempts)
}

// dispatchDeadLetter redirects a terminally-failed delivery to the configured
// dead-letter destination. When no dead-letter destination is configured this is
// a no-op (run already logged the terminal failure — log-and-drop). The
// dead-letter destination is guaranteed non-webhook by config validation, so no
// recursion is possible.
func (e *webhookDeliveryEngine) dispatchDeadLetter(job webhookJob, outcome string, attempts int) {
	d := job.delivery
	if d.dlqDestination == "" {
		return // log-and-drop; run() already emitted the terminal Error log
	}
	e.mu.RLock()
	resolver := e.deadLetter
	e.mu.RUnlock()

	msg := job.msg
	msg.DeadLetter = &DeadLetterMeta{
		Destination: e.destID, Target: d.target, Outcome: outcome, Attempts: attempts, FailedAt: time.Now().UTC(),
	}
	if resolver == nil {
		e.logger.Error("webhook dead-letter dropped: no resolver configured",
			slog.String("destination", e.destID), slog.String("target", d.target),
			slog.String("event_id", job.msg.ID), slog.String("dlq_destination", d.dlqDestination))
		if e.metrics != nil {
			e.metrics.WebhookDeadLetterFailure(e.destID, d.target)
		}
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := resolver(ctx, d.dlqDestination, d.dlqTarget, msg); err != nil {
		e.logger.Error("webhook dead-letter dispatch failed",
			slog.String("destination", e.destID), slog.String("target", d.target),
			slog.String("event_id", job.msg.ID), slog.String("dlq_destination", d.dlqDestination),
			slog.String("dlq_target", d.dlqTarget), slog.String("error", err.Error()))
		if e.metrics != nil {
			e.metrics.WebhookDeadLetterFailure(e.destID, d.target)
		}
		return
	}
	e.logger.Warn("webhook delivery dead-lettered",
		slog.String("destination", e.destID), slog.String("target", d.target),
		slog.String("event_id", job.msg.ID), slog.String("outcome", outcome),
		slog.Int("attempts", attempts), slog.String("dlq_destination", d.dlqDestination),
		slog.String("dlq_target", d.dlqTarget))
	if e.metrics != nil {
		e.metrics.WebhookDeadLetter(e.destID, d.target, outcome)
	}
}

// run performs the HTTP delivery for one message with retry/backoff. It honors
// ctx (used by sync delivery to enforce the caller's deadline). It returns nil
// err on a 2xx; on terminal failure (terminal outcome, exhausted attempts, or a
// cancelled ctx) it returns the final outcome, the number of attempts made, and
// a non-nil error.
// logTerminalFailure logs a terminal/exhausted delivery failure. When a
// dead-letter destination is configured, this is demoted from Error to Warn:
// dispatchDeadLetter (called next, by the engine, with the error returned
// from run) will emit its own Warn on a successful dead-letter or Error on a
// failed/unconfigured one. Without the demotion, a successfully dead-lettered
// event would still trip Error-rate alerting even though the message was not
// lost. A delivery with no DLQ configured has nowhere else to be logged, so it
// keeps the Error level here (log-and-drop is the only record of the loss).
func (d *webhookDelivery) logTerminalFailure(msg string, args ...any) {
	if d.dlqDestination != "" {
		d.logger.Warn(msg, args...)
		return
	}
	d.logger.Error(msg, args...)
}

func (d *webhookDelivery) run(ctx context.Context, msg Message, m WebhookMetrics) (outcome string, attempts int, err error) {
	maxAttempts := d.retry.MaxAttempts
	if maxAttempts <= 0 {
		maxAttempts = 1
	}
	delay := d.retry.InitialBackoff

	for attempt := 1; attempt <= maxAttempts; attempt++ {
		attempts = attempt
		if attempt > 1 {
			if m != nil {
				m.WebhookRetry(d.destID, d.target)
			}
			d.logger.Warn("webhook delivery retry",
				slog.String("destination", d.destID), slog.String("target", d.target),
				slog.String("event_id", msg.ID), slog.Int("attempt", attempt))
		}
		var retryable bool
		var retryAfter time.Duration
		outcome, retryable, retryAfter = d.attempt(ctx, msg)
		if m != nil {
			m.WebhookDelivery(d.destID, d.target, outcome)
		}
		if !retryable {
			if outcome == "2xx" {
				d.logger.Debug("webhook delivered",
					slog.String("destination", d.destID), slog.String("target", d.target),
					slog.String("event_id", msg.ID), slog.String("outcome", outcome))
				return outcome, attempts, nil
			}
			d.logTerminalFailure("webhook delivery failed (terminal)",
				slog.String("destination", d.destID), slog.String("target", d.target),
				slog.String("event_id", msg.ID), slog.String("outcome", outcome))
			return outcome, attempts, fmt.Errorf("webhook %q target %q: delivery failed (%s)", d.destID, d.target, outcome)
		}
		if attempt == maxAttempts {
			d.logTerminalFailure("webhook delivery failed (attempts exhausted)",
				slog.String("destination", d.destID), slog.String("target", d.target),
				slog.String("event_id", msg.ID), slog.String("outcome", outcome), slog.Int("attempts", maxAttempts))
			return outcome, attempts, fmt.Errorf("webhook %q target %q: delivery failed after %d attempts (%s)", d.destID, d.target, attempts, outcome)
		}
		// A 429 with a valid Retry-After overrides the computed exponential
		// backoff for this wait (capped by max_backoff); an absent or
		// unparseable header falls back to the computed delay.
		wait := delay
		if outcome == "429" && retryAfter > 0 {
			wait = retryAfter
			if d.retry.MaxBackoff > 0 && wait > d.retry.MaxBackoff {
				wait = d.retry.MaxBackoff
			}
		}
		if serr := sleepContext(ctx, wait); serr != nil {
			return outcome, attempts, fmt.Errorf("webhook %q target %q: delivery aborted before retry (%s): %w", d.destID, d.target, outcome, serr)
		}
		delay = nextBackoff(delay, d.retry.MaxBackoff)
	}
	return outcome, attempts, nil // unreachable: loop returns on every path
}

// attempt sends one request and classifies the result. retryable=true for
// connection errors, timeouts, 429, and 5xx. outcome ∈ 2xx|4xx|5xx|429|timeout|error.
// retryAfter is the parsed Retry-After delay for a 429 response (0 when
// absent, unparseable, or the outcome is not 429).
func (d *webhookDelivery) attempt(ctx context.Context, msg Message) (outcome string, retryable bool, retryAfter time.Duration) {
	req, err := buildWebhookRequest(ctx, d, msg)
	if err != nil {
		return "error", false, 0 // a request we can't even build is terminal
	}
	resp, err := d.client.Do(req)
	if err != nil {
		return "timeout", true, 0 // network error / timeout -> retry
	}
	defer func() { _, _ = io.Copy(io.Discard, resp.Body); resp.Body.Close() }()
	switch {
	case resp.StatusCode >= 200 && resp.StatusCode < 300:
		return "2xx", false, 0
	case resp.StatusCode == 429 || resp.StatusCode >= 500:
		if resp.StatusCode >= 500 {
			return "5xx", true, 0
		}
		return "429", true, parseRetryAfter(resp.Header.Get("Retry-After"), time.Now())
	default:
		return "4xx", false, 0 // terminal
	}
}

// parseRetryAfter parses a Retry-After header per RFC 9110 §10.2.3: either an
// integer number of seconds, or an HTTP-date. Returns 0 — meaning "fall back
// to the computed backoff" — when the header is absent, malformed, or
// resolves to a non-positive delay.
func parseRetryAfter(header string, now time.Time) time.Duration {
	header = strings.TrimSpace(header)
	if header == "" {
		return 0
	}
	if secs, err := strconv.Atoi(header); err == nil {
		if secs <= 0 {
			return 0
		}
		return time.Duration(secs) * time.Second
	}
	if when, err := http.ParseTime(header); err == nil {
		if d := when.Sub(now); d > 0 {
			return d
		}
	}
	return 0
}

// buildWebhookRequest constructs the outbound request for the delivery mode.
func buildWebhookRequest(ctx context.Context, d *webhookDelivery, msg Message) (*http.Request, error) {
	if d.mode == "proxy" {
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, d.url, bytes.NewReader(msg.RawBody))
		if err != nil {
			return nil, err
		}
		copyProxyHeaders(req.Header, msg.OrigHeaders)
		stampLoopHops(req, msg)    // overwrites any inbound hop header copied above
		stampTraceparent(req, msg) // traceparent is not hop-by-hop; overwrites any copied inbound value
		return req, nil            // proxy: no hook_way auth
	}
	body, err := json.Marshal(msg)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, d.url, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	stampLoopHops(req, msg)
	stampTraceparent(req, msg)
	applyWebhookAuth(req, d.auth, body, msg.ID, time.Now())
	return req, nil
}

// stampLoopHops sets the loop-protection hop header to one more than the inbound
// count, so a downstream Hookway can detect a delivery loop. It always fires;
// inbound enforcement is what each Hookway toggles via config.
func stampLoopHops(req *http.Request, msg Message) {
	req.Header.Set(config.LoopHopsHeader, strconv.Itoa(msg.Hops+1))
}

// stampTraceparent sets the outbound W3C traceparent header from msg.Traceparent
// (populated by the httpapi layer: the inbound header verbatim when valid, or a
// synthesized one otherwise). It only sets the header when non-empty, and — in
// proxy mode, where copyProxyHeaders may have already copied an inbound
// Traceparent — this call overwrites it: traceparent is not hop-by-hop, and
// msg.Traceparent is the value Hookway actually derived for this delivery.
func stampTraceparent(req *http.Request, msg Message) {
	if msg.Traceparent == "" {
		return
	}
	req.Header.Set("Traceparent", msg.Traceparent)
}

// hopByHop headers are managed by the transport and must not be forwarded.
var hopByHopHeaders = map[string]bool{
	"Connection": true, "Keep-Alive": true, "Proxy-Authenticate": true,
	"Proxy-Authorization": true, "Te": true, "Trailer": true,
	"Transfer-Encoding": true, "Upgrade": true, "Host": true, "Content-Length": true,
}

func copyProxyHeaders(dst, src http.Header) {
	for k, vs := range src {
		if hopByHopHeaders[http.CanonicalHeaderKey(k)] {
			continue
		}
		for _, v := range vs {
			dst.Add(k, v)
		}
	}
}
