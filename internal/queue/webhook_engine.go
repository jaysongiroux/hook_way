package queue

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/jasongiroux/hookway/internal/config"
)

var errWebhookQueueFull = errors.New("webhook: delivery queue full")

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
}

type webhookJob struct {
	delivery *webhookDelivery
	msg      Message
}

type webhookDeliveryEngine struct {
	jobs         chan webhookJob
	wg           sync.WaitGroup
	mu           sync.RWMutex
	closed       bool
	closeOnce    sync.Once
	drainTimeout time.Duration
	metrics      WebhookMetrics
	logger       *slog.Logger
}

func newWebhookDeliveryEngine(workers, queueSize int, drain time.Duration, m WebhookMetrics, logger *slog.Logger) *webhookDeliveryEngine {
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
		jobs: make(chan webhookJob, queueSize), drainTimeout: drain, metrics: m, logger: logger,
	}
	for i := 0; i < workers; i++ {
		e.wg.Add(1)
		go e.worker()
	}
	return e
}

func (e *webhookDeliveryEngine) worker() {
	defer e.wg.Done()
	for job := range e.jobs {
		e.deliver(job)
	}
}

// Enqueue is non-blocking. Returns errWebhookQueueFull when the buffer is full,
// or an error if the engine is closing. The RLock/Lock pairing with Close
// prevents a send on a closed channel.
func (e *webhookDeliveryEngine) Enqueue(job webhookJob) error {
	e.mu.RLock()
	defer e.mu.RUnlock()
	if e.closed {
		return errors.New("webhook: engine closing")
	}
	select {
	case e.jobs <- job:
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

// deliver performs the HTTP delivery for one job, with retry/backoff.
func (e *webhookDeliveryEngine) deliver(job webhookJob) {
	d := job.delivery
	attempts := d.retry.MaxAttempts
	if attempts <= 0 {
		attempts = 1
	}
	delay := d.retry.InitialBackoff
	ctx := context.Background()

	for attempt := 1; attempt <= attempts; attempt++ {
		if attempt > 1 {
			if e.metrics != nil {
				e.metrics.WebhookRetry(d.destID, d.target)
			}
			d.logger.Warn("webhook delivery retry",
				slog.String("destination", d.destID), slog.String("target", d.target),
				slog.String("event_id", job.msg.ID), slog.Int("attempt", attempt))
		}
		outcome, retryable := e.attempt(ctx, d, job.msg)
		if e.metrics != nil {
			e.metrics.WebhookDelivery(d.destID, d.target, outcome)
		}
		if !retryable {
			if outcome == "2xx" {
				d.logger.Debug("webhook delivered",
					slog.String("destination", d.destID), slog.String("target", d.target),
					slog.String("event_id", job.msg.ID), slog.String("outcome", outcome))
			} else {
				d.logger.Error("webhook delivery failed (terminal)",
					slog.String("destination", d.destID), slog.String("target", d.target),
					slog.String("event_id", job.msg.ID), slog.String("outcome", outcome))
			}
			return
		}
		if attempt == attempts {
			d.logger.Error("webhook delivery failed (attempts exhausted)",
				slog.String("destination", d.destID), slog.String("target", d.target),
				slog.String("event_id", job.msg.ID), slog.String("outcome", outcome), slog.Int("attempts", attempts))
			return
		}
		if err := sleepContext(ctx, delay); err != nil {
			return
		}
		delay = nextBackoff(delay, d.retry.MaxBackoff)
	}
}

// attempt sends one request and classifies the result. retryable=true for
// connection errors, timeouts, 429, and 5xx. outcome ∈ 2xx|4xx|5xx|429|timeout|error.
func (e *webhookDeliveryEngine) attempt(ctx context.Context, d *webhookDelivery, msg Message) (outcome string, retryable bool) {
	req, err := buildWebhookRequest(ctx, d, msg)
	if err != nil {
		return "error", false // a request we can't even build is terminal
	}
	resp, err := d.client.Do(req)
	if err != nil {
		return "timeout", true // network error / timeout -> retry
	}
	defer func() { _, _ = io.Copy(io.Discard, resp.Body); resp.Body.Close() }()
	switch {
	case resp.StatusCode >= 200 && resp.StatusCode < 300:
		return "2xx", false
	case resp.StatusCode == 429 || resp.StatusCode >= 500:
		if resp.StatusCode >= 500 {
			return "5xx", true
		}
		return "429", true
	default:
		return "4xx", false // terminal
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
		stampLoopHops(req, msg) // overwrites any inbound hop header copied above
		return req, nil         // proxy: no hook_way auth
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
	applyWebhookAuth(req, d.auth, body, msg.ID, time.Now())
	return req, nil
}

// stampLoopHops sets the loop-protection hop header to one more than the inbound
// count, so a downstream Hookway can detect a delivery loop. It always fires;
// inbound enforcement is what each Hookway toggles via config.
func stampLoopHops(req *http.Request, msg Message) {
	req.Header.Set(config.LoopHopsHeader, strconv.Itoa(msg.Hops+1))
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
