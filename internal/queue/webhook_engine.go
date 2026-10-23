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
	"sync"
	"time"

	"github.com/jaysongiroux/hook_way/internal/config"
	"github.com/jaysongiroux/hook_way/internal/logging"
	"github.com/jaysongiroux/hook_way/internal/retry"
)

// ErrWebhookQueueFull is wrapped by Publish when an async webhook
// destination's delivery queue is full.
var ErrWebhookQueueFull = errors.New("webhook: delivery queue full")

var errWebhookEngineClosing = errors.New("webhook: engine closing")

// ErrWebhookDrainTimeout means workers still own dependent publishers/capture.
// Shutdown callers must leave those resources open and exit unsuccessfully.
var ErrWebhookDrainTimeout = errors.New("webhook: drain timed out with deliveries pending")

// Responses are ignored: drain only a small prefix for connection reuse, never
// spend the complete attempt budget consuming an arbitrarily large body.
const maxWebhookResponseDrainBytes = 64 << 10

// webhookDelivery is the resolved per-destination delivery config (built by the publisher).
// (WebhookMetrics interface is defined in publisher.go)
type webhookDelivery struct {
	destID string
	target string
	// metricTarget is the label used for per-target metrics: the authored
	// template for dynamic targets (bounded cardinality), else the target.
	metricTarget string
	url          string // resolved base+target
	mode         string // "envelope" | "proxy"
	client       *http.Client
	retry        RetryPolicy
	auth         webhookAuth
	logger       *slog.Logger
	// Dead-letter routing for terminal async failures. Empty => log-and-drop.
	dlqDestination string
	dlqTarget      string
}

type webhookJob struct {
	delivery       *webhookDelivery
	msg            Message
	origin         Origin
	accountedBytes int64
}

// deadLetterFunc redirects a terminally-failed delivery to another destination.
type deadLetterFunc func(ctx context.Context, destID, target string, msg Message) error

type webhookDeliveryEngine struct {
	destID       string
	jobs         chan webhookJob
	wg           sync.WaitGroup
	mu           sync.RWMutex
	closed       bool
	maxBytes     int64
	pendingBytes int64
	closeOnce    sync.Once
	closeErr     error
	drainTimeout time.Duration
	metrics      WebhookMetrics
	logger       *slog.Logger
	deadLetter   deadLetterFunc      // set post-build under mu via setDeadLetter
	onTerminal   TerminalFailureFunc // set post-build under mu via setTerminalFailure
}

func newWebhookDeliveryEngine(destID string, workers, queueSize int, drain time.Duration, m WebhookMetrics, logger *slog.Logger, byteBudget ...int64) *webhookDeliveryEngine {
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
		maxBytes: 64 << 20,
	}
	if len(byteBudget) != 0 && byteBudget[0] > 0 {
		e.maxBytes = byteBudget[0]
	}
	e.reportBytes()
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

// setTerminalFailure wires the callback for deliveries that fail for good and
// are not dead-lettered. It is set once during factory wiring.
func (e *webhookDeliveryEngine) setTerminalFailure(fn TerminalFailureFunc) {
	e.mu.Lock()
	e.onTerminal = fn
	e.mu.Unlock()
}

// reportTerminal hands a failed-for-good delivery to the terminal callback.
func (e *webhookDeliveryEngine) reportTerminal(job webhookJob, outcome string, attempts int, err error) {
	e.mu.RLock()
	fn := e.onTerminal
	e.mu.RUnlock()
	if fn == nil {
		return
	}
	d := job.delivery
	fn(TerminalFailure{
		DestinationID: e.destID, Target: d.metricTarget, RenderedTarget: d.target, Message: job.msg,
		Outcome: outcome, Attempts: attempts, Err: err, FailedAt: time.Now().UTC(), Origin: job.origin,
	})
}

// capacity is the fixed job-buffer capacity (used to publish the capacity gauge).
func (e *webhookDeliveryEngine) capacity() int { return cap(e.jobs) }

// saturated reports current depth/capacity and whether depth is at or above 90%
// of capacity. This is the readiness signal (plan item D4).
func (e *webhookDeliveryEngine) saturated() (depth, capacity int, isSaturated bool) {
	depth = len(e.jobs)
	capacity = cap(e.jobs)
	e.mu.RLock()
	bytesFull := e.maxBytes > 0 && e.pendingBytes >= e.maxBytes-e.maxBytes/10
	e.mu.RUnlock()
	return depth, capacity, depth*10 >= capacity*9 || bytesFull
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
	defer func() { e.mu.Lock(); e.pendingBytes -= job.accountedBytes; e.reportBytes(); e.mu.Unlock() }()
	defer func() {
		if r := recover(); r != nil {
			e.logger.Error("panic recovered in webhook delivery worker",
				"location", "webhook_worker",
				"destination", e.destID, "target", job.delivery.target,
				"event_id", job.msg.ID,
				"panic_type", fmt.Sprintf("%T", r), "stack", string(debug.Stack()))
			if e.metrics != nil {
				e.metrics.Panic("webhook_worker")
			}
		}
	}()
	e.deliver(job)
}

// Enqueue is non-blocking. Returns ErrWebhookQueueFull when the buffer is full,
// or errWebhookEngineClosing if the engine is closing. The RLock/Lock pairing
// with Close prevents a send on a closed channel.
func (e *webhookDeliveryEngine) Enqueue(job webhookJob) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.closed {
		return errWebhookEngineClosing
	}
	if len(e.jobs) == cap(e.jobs) {
		return ErrWebhookQueueFull
	}
	limit := e.maxBytes
	if limit == 0 {
		limit = 64 << 20
	}
	n, ok := webhookMessageCharge(job.msg, limit-e.pendingBytes)
	if !ok {
		return fmt.Errorf("%w: accounted bytes exceed max_queue_bytes (%d); raise the finite destination budget for larger deliveries", ErrWebhookQueueFull, limit)
	}
	job.accountedBytes = n
	select {
	case e.jobs <- job:
		e.pendingBytes += n
		e.reportBytes()
		if e.metrics != nil {
			e.metrics.WebhookQueueDepth(e.destID, len(e.jobs))
		}
		return nil
	default:
		return ErrWebhookQueueFull
	}
}

// Optional extension keeps existing metrics implementations source-compatible.
// Call while holding mu after startup to preserve accounting/metric ordering.
func (e *webhookDeliveryEngine) reportBytes() {
	if m, ok := e.metrics.(interface{ WebhookQueueBytes(string, int64, int64) }); ok {
		capacity := e.maxBytes
		if capacity == 0 {
			capacity = 64 << 20
		}
		m.WebhookQueueBytes(e.destID, e.pendingBytes, capacity)
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
			e.closeErr = ErrWebhookDrainTimeout
			e.logger.Warn("webhook engine drain timed out with deliveries still pending")
		}
	})
	return e.closeErr
}

// deliver runs one job to completion on a worker: attempt the delivery (with
// retry/backoff), report the resulting queue depth, and — on terminal failure —
// dispatch to the dead-letter destination (or log-and-drop when none is set).
func (e *webhookDeliveryEngine) deliver(job webhookJob) {
	if e.metrics != nil {
		e.metrics.WebhookQueueDepth(e.destID, len(e.jobs))
	}
	outcome, attempts, err := job.delivery.run(context.Background(), job.msg, e.metrics)
	if err == nil || e.dispatchDeadLetter(job, outcome, attempts) {
		return
	}
	e.reportTerminal(job, outcome, attempts, err)
}

// dispatchDeadLetter redirects a terminally-failed delivery to the configured
// dead-letter destination and reports whether the dead-letter destination
// accepted it. When no dead-letter destination is configured this is
// a no-op (run already logged the terminal failure — log-and-drop). The
// dead-letter destination is guaranteed non-webhook by config validation, so no
// recursion is possible.
func (e *webhookDeliveryEngine) dispatchDeadLetter(job webhookJob, outcome string, attempts int) bool {
	d := job.delivery
	if d.dlqDestination == "" {
		return false // log-and-drop; run() already emitted the terminal Error log
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
			e.metrics.WebhookDeadLetterFailure(e.destID, d.metricTarget)
		}
		return false
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := resolver(ctx, d.dlqDestination, d.dlqTarget, msg); err != nil {
		e.logger.Error("webhook dead-letter dispatch failed",
			slog.String("destination", e.destID), slog.String("target", d.target),
			slog.String("event_id", job.msg.ID), slog.String("dlq_destination", d.dlqDestination),
			slog.String("dlq_target", d.dlqTarget), slog.String("error", logging.ErrorText(err)))
		if e.metrics != nil {
			e.metrics.WebhookDeadLetterFailure(e.destID, d.metricTarget)
		}
		return false
	}
	e.logger.Warn("webhook delivery dead-lettered",
		slog.String("destination", e.destID), slog.String("target", d.target),
		slog.String("event_id", job.msg.ID), slog.String("outcome", outcome),
		slog.Int("attempts", attempts), slog.String("dlq_destination", d.dlqDestination),
		slog.String("dlq_target", d.dlqTarget))
	if e.metrics != nil {
		e.metrics.WebhookDeadLetter(e.destID, d.metricTarget, outcome)
	}
	return true
}

// logTerminalFailure logs a terminal/exhausted delivery failure. When a
// dead-letter destination is configured and the delivery is async, this is
// demoted from Error to Warn: the dead letter that follows carries the
// failure. An inline delivery (a redrive) never dead-letters — its failure
// goes back to the caller — so it is never demoted.
func (d *webhookDelivery) logTerminalFailure(ctx context.Context, msg string, args ...any) {
	if d.dlqDestination != "" && !InlineDelivery(ctx) {
		d.logger.Warn(msg, args...)
		return
	}
	d.logger.Error(msg, args...)
}

// run performs the HTTP delivery for one message with retry/backoff. It honors
// ctx (used by sync delivery to enforce the caller's deadline). It returns nil
// err on a 2xx; on terminal failure (terminal outcome, exhausted attempts, or a
// cancelled ctx) it returns the final outcome, the number of attempts made, and
// a non-nil error.
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
				m.WebhookRetry(d.destID, d.metricTarget)
			}
			d.logger.Warn("webhook delivery retry",
				slog.String("destination", d.destID), slog.String("target", d.target),
				slog.String("event_id", msg.ID), slog.Int("attempt", attempt))
		}
		var retryable bool
		var retryAfter time.Duration
		outcome, retryable, retryAfter = d.attempt(ctx, msg)
		if m != nil {
			m.WebhookDelivery(d.destID, d.metricTarget, outcome)
		}
		if !retryable {
			if outcome == "2xx" {
				d.logger.Debug("webhook delivered",
					slog.String("destination", d.destID), slog.String("target", d.target),
					slog.String("event_id", msg.ID), slog.String("outcome", outcome))
				return outcome, attempts, nil
			}
			d.logTerminalFailure(ctx, "webhook delivery failed (terminal)",
				slog.String("destination", d.destID), slog.String("target", d.target),
				slog.String("event_id", msg.ID), slog.String("outcome", outcome))
			return outcome, attempts, fmt.Errorf("webhook %q target %q: delivery failed (%s)", d.destID, d.target, outcome)
		}
		if attempt == maxAttempts {
			d.logTerminalFailure(ctx, "webhook delivery failed (attempts exhausted)",
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
		if serr := retry.Sleep(ctx, wait); serr != nil {
			return outcome, attempts, fmt.Errorf("webhook %q target %q: delivery aborted before retry (%s): %w", d.destID, d.target, outcome, serr)
		}
		delay = retry.NextBackoff(delay, d.retry.MaxBackoff)
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
	defer func() {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, maxWebhookResponseDrainBytes))
		_ = resp.Body.Close()
	}()
	switch {
	case resp.StatusCode >= 200 && resp.StatusCode < 300:
		return "2xx", false, 0
	case resp.StatusCode == 429 || resp.StatusCode >= 500:
		if resp.StatusCode >= 500 {
			return "5xx", true, 0
		}
		return "429", true, retry.ParseRetryAfter(resp.Header.Get("Retry-After"), time.Now())
	default:
		return "4xx", false, 0 // terminal
	}
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
	var body []byte
	var err error
	if msg.Publish != nil && msg.Publish.WireBody == "json" {
		body, err = BrokerBody(msg)
	} else {
		body, err = json.Marshal(msg)
	}
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
