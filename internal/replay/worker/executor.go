// Package worker executes retry runs: it claims queued runs, sends each to
// core's control listener exactly as the spec's outcome rules allow, and
// finalizes the result. Executor owns the at-most-once classification;
// Worker owns claiming, concurrency, lease expiry, and shutdown.
package worker

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"net/http/httptrace"
	"regexp"
	"slices"
	"strings"
	"sync/atomic"
	"time"

	"github.com/jaysongiroux/hook_way/internal/replay/metrics"
	"github.com/jaysongiroux/hook_way/internal/replay/store"
	"github.com/jaysongiroux/hook_way/internal/replaywire"
	"github.com/jaysongiroux/hook_way/internal/tlsfiles"
)

const (
	// coreMargin is kept back from execution_timeout: core's budget ends
	// before the worker gives up (spec: 30s → 25s).
	coreMargin      = 5 * time.Second
	maxAttempts     = 3
	maxProblemBytes = 64 << 10
	maxDetailLen    = 4096
)

// Reasons recorded on runs that did not succeed. A blocked run's reason is
// core's block code.
const (
	ReasonExecutorUnavailable = "executor_unavailable"
	ReasonExecutorAuth        = "executor_auth"
	ReasonExecutorRejected    = "executor_rejected"
	ReasonAmbiguous           = "ambiguous"
	ReasonNodeFailed          = "node_failed"
	ReasonSnapshotUnavailable = "snapshot_unavailable"
	reasonUnknownBlock        = "unknown_block_reason"
)

var blockCodePattern = regexp.MustCompile(`^[a-z][a-z0-9_]{0,63}$`)

// Outcome is how one run ended.
type Outcome struct {
	Status   string                    // store.RunSucceeded, RunFailed, RunBlocked, or RunUnknown
	Reason   string                    // why the run did not succeed
	Call     string                    // the last executor_calls_total outcome ("" when core was never called)
	Detail   string                    // error text for the run's result
	Result   *replaywire.DeliverResult // core's report, when it sent a valid one
	Children []store.NewFailure        // downstream failures of a succeeded run
}

// ExecutorOptions configures the client to core's control listener.
type ExecutorOptions struct {
	ControlURL       string
	Token            string
	TLS              tlsfiles.Files
	ExecutionTimeout time.Duration
	MaxRecordBytes   int64
	Metrics          *metrics.Metrics
}

// Executor calls POST /internal/replay/v1/deliver.
type Executor struct {
	client         *http.Client
	url            string
	token          string
	timeout        time.Duration
	budget         time.Duration
	maxRecordBytes int64
	metrics        *metrics.Metrics
	backoff        time.Duration
}

func NewExecutor(o ExecutorOptions) (*Executor, error) {
	base, ok := http.DefaultTransport.(*http.Transport)
	if !ok {
		return nil, errors.New("worker: http.DefaultTransport is not an *http.Transport")
	}
	transport := base.Clone()
	// HTTP/1.1 only. Go's HTTP/2 transport resends a request by itself after
	// some stream resets (REFUSED_STREAM, GOAWAY, a peer's PROTOCOL_ERROR),
	// even one core has already read and started — a resend the executor
	// cannot see, which would break the never-resend-after-write rule.
	transport.ForceAttemptHTTP2 = false
	transport.TLSNextProto = map[string]func(string, *tls.Conn) http.RoundTripper{}
	transport.IdleConnTimeout = replaywire.ControlClientIdleTimeout
	if o.TLS.Configured() {
		cfg, err := o.TLS.Build("core tls")
		if err != nil {
			return nil, err
		}
		transport.TLSClientConfig = cfg
	}
	return &Executor{
		client: &http.Client{
			Transport: transport,
			// A redirect means core never saw the request; report it
			// instead of re-sending the body elsewhere.
			CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
		},
		url:   strings.TrimRight(o.ControlURL, "/") + replaywire.DeliverPath,
		token: o.Token, timeout: o.ExecutionTimeout, budget: o.ExecutionTimeout - coreMargin,
		maxRecordBytes: o.MaxRecordBytes, metrics: o.Metrics, backoff: 250 * time.Millisecond,
	}, nil
}

func detail(s string) string {
	if len(s) > maxDetailLen {
		return s[:maxDetailLen]
	}
	return s
}

func unavailable(err error) Outcome {
	return Outcome{Status: store.RunFailed, Reason: ReasonExecutorUnavailable, Call: metrics.CallUnavailable, Detail: detail(err.Error())}
}

func ambiguous(format string, args ...any) Outcome {
	return Outcome{Status: store.RunUnknown, Reason: ReasonAmbiguous, Call: metrics.CallAmbiguous, Detail: detail(fmt.Sprintf(format, args...))}
}

func rejected(format string, args ...any) Outcome {
	return Outcome{Status: store.RunFailed, Reason: ReasonExecutorRejected, Call: metrics.CallRejected, Detail: detail(fmt.Sprintf(format, args...))}
}

// Deliver sends one run to core within execution_timeout and classifies the
// answer. It resends only when core provably did not start the delivery.
func (e *Executor) Deliver(ctx context.Context, run store.ClaimedRun, snapshot []byte) Outcome {
	ctx, cancel := context.WithTimeout(ctx, e.timeout)
	defer cancel()
	body, contentType, err := e.requestBody(run, snapshot)
	if err != nil {
		return rejected("build the deliver request: %v", err)
	}
	var out Outcome
	for attempt := 1; attempt <= maxAttempts; attempt++ {
		var retry bool
		out, retry = e.attempt(ctx, body, contentType, run)
		if e.metrics != nil && out.Call != "" {
			e.metrics.ExecutorCall(out.Call)
		}
		if !retry || attempt == maxAttempts {
			return out
		}
		select {
		case <-ctx.Done():
			return out
		case <-time.After(e.backoff * time.Duration(attempt)):
		}
	}
	return out
}

func (e *Executor) requestBody(run store.ClaimedRun, snapshot []byte) ([]byte, string, error) {
	f := run.Failure
	req := replaywire.DeliverRequest{
		RunID: run.Run.RunID, FailureID: f.ID, Kind: f.Kind, Flow: f.Flow, NodeID: f.NodeID, NodeType: f.NodeType,
		DestinationID: f.DestinationID, DestinationType: f.DestinationType, RenderedTarget: f.RenderedTarget,
		NodeConfigHash: f.NodeConfigHash, DestinationHash: f.DestinationHash, DeadlineMS: e.budget.Milliseconds(),
		InboundStatus: f.InboundStatus, MaxRecordBytes: e.maxRecordBytes,
	}
	if f.Snapshot != nil {
		req.SnapshotSHA256, req.SnapshotVersion = f.Snapshot.SHA256, f.Snapshot.Version
	}
	meta, err := json.Marshal(req)
	if err != nil {
		return nil, "", err
	}
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	for _, p := range []struct {
		name string
		data []byte
	}{{replaywire.PartRequest, meta}, {replaywire.PartSnapshot, snapshot}} {
		pw, err := w.CreateFormField(p.name)
		if err != nil {
			return nil, "", err
		}
		if _, err := pw.Write(p.data); err != nil {
			return nil, "", err
		}
	}
	if err := w.Close(); err != nil {
		return nil, "", err
	}
	return buf.Bytes(), w.FormDataContentType(), nil
}

// attempt makes one call. retry reports that core provably did not start:
// the request was never completely written (core reads the whole body
// before it starts), or core answered 503 not_started.
func (e *Executor) attempt(ctx context.Context, body []byte, contentType string, run store.ClaimedRun) (Outcome, bool) {
	var written atomic.Bool
	trace := &httptrace.ClientTrace{WroteRequest: func(info httptrace.WroteRequestInfo) {
		if info.Err == nil {
			written.Store(true)
		}
	}}
	req, err := http.NewRequestWithContext(httptrace.WithClientTrace(ctx, trace), http.MethodPost, e.url, bytes.NewReader(body))
	if err != nil {
		return rejected("build the http request: %v", err), false
	}
	req.Header.Set("Content-Type", contentType)
	req.Header.Set("Authorization", "Bearer "+e.token)
	resp, err := e.client.Do(req)
	if err != nil {
		if !written.Load() {
			return unavailable(err), true
		}
		return ambiguous("no complete response after the request was sent: %v", err), false
	}
	defer func() { _ = resp.Body.Close() }()
	switch code := resp.StatusCode; {
	case code == http.StatusOK:
		return e.readResult(resp, run), false
	case code == http.StatusUnauthorized || code == http.StatusForbidden:
		return Outcome{Status: store.RunFailed, Reason: ReasonExecutorAuth, Call: metrics.CallAuth,
			Detail: fmt.Sprintf("core rejected the control token (%d)", code)}, false
	case code == http.StatusUnprocessableEntity:
		reason := problemCode(resp.Body)
		if !blockCodePattern.MatchString(reason) {
			reason = reasonUnknownBlock
		}
		return Outcome{Status: store.RunBlocked, Reason: reason, Call: metrics.CallBlocked, Detail: "core blocked the redrive: " + reason}, false
	case code == http.StatusServiceUnavailable && problemCode(resp.Body) == replaywire.NotStartedCode:
		return unavailable(errors.New("core is busy or draining (503 not_started)")), true
	case code >= 300 && code < 500:
		return rejected("core refused the request before executing it (%d)", code), false
	default:
		return ambiguous("core answered %d", code), false
	}
}

func problemCode(r io.Reader) string {
	var p struct {
		Code string `json:"code"`
	}
	if err := json.NewDecoder(io.LimitReader(r, maxProblemBytes)).Decode(&p); err != nil {
		return ""
	}
	return p.Code
}

// readResult parses a 200 response. Anything malformed is ambiguous: core
// may have delivered, so the run must not look like a clean failure.
func (e *Executor) readResult(resp *http.Response, run store.ClaimedRun) Outcome {
	mediaType, params, err := mime.ParseMediaType(resp.Header.Get("Content-Type"))
	if err != nil || !strings.HasPrefix(mediaType, "multipart/") || params["boundary"] == "" {
		return ambiguous("the 200 response is not multipart (%q)", resp.Header.Get("Content-Type"))
	}
	limited := &io.LimitedReader{R: resp.Body, N: replaywire.MaxDeliverResponseBytes + 1}
	mr := multipart.NewReader(limited, params["boundary"])
	parts := map[string][]byte{}
	for {
		part, err := mr.NextPart()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return ambiguous("read the response: %v", err)
		}
		name := part.FormName()
		if _, dup := parts[name]; dup {
			return ambiguous("the response repeats part %q", name)
		}
		limit := e.maxRecordBytes
		if name == replaywire.PartResult {
			limit = replaywire.MaxResultBytes
		}
		data, err := io.ReadAll(io.LimitReader(part, limit+1))
		if err != nil {
			return ambiguous("read part %q: %v", name, err)
		}
		if int64(len(data)) > limit {
			return ambiguous("part %q exceeds %d bytes", name, limit)
		}
		parts[name] = data
	}
	if limited.N <= 0 {
		return ambiguous("the response exceeds %d bytes", replaywire.MaxDeliverResponseBytes)
	}
	raw, ok := parts[replaywire.PartResult]
	if !ok {
		return ambiguous("the response has no %s part", replaywire.PartResult)
	}
	delete(parts, replaywire.PartResult)
	var result replaywire.DeliverResult
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&result); err != nil {
		return ambiguous("decode the result: %v", err)
	}
	f := run.Failure
	if err := result.Validate(f.NodeID); err != nil {
		return ambiguous("%v", err)
	}
	if f.Kind == replaywire.KindPublishOnly && len(result.Children) > 0 {
		return ambiguous("a publish_only run cannot have children")
	}
	out := Outcome{Status: store.RunSucceeded, Call: metrics.CallOK, Result: &result}
	if resume, _ := result.ResumeOutcome(f.NodeID); resume.Outcome == replaywire.NodeFailed {
		out.Status, out.Reason, out.Detail = store.RunFailed, ReasonNodeFailed, detail(resume.ErrorMessage)
	}
	for i, child := range result.Children {
		if child.ID == f.ID {
			return ambiguous("children[%d] reuses the record's own id", i)
		}
		if child.Flow != f.Flow || child.EventID != f.EventID {
			return ambiguous("children[%d] does not belong to this record's flow and event", i)
		}
		snapName, payName := replaywire.ChildSnapshotPart(i), replaywire.ChildPayloadPart(i)
		snap, hasSnap := parts[snapName]
		pay, hasPay := parts[payName]
		if err := replaywire.VerifyArtifact(snapName, child.Snapshot, hasSnap, snap); err != nil {
			return ambiguous("%v", err)
		}
		if err := replaywire.VerifyArtifact(payName, child.Payload, hasPay, pay); err != nil {
			return ambiguous("%v", err)
		}
		delete(parts, snapName)
		delete(parts, payName)
		out.Children = append(out.Children, store.NewFailure{Metadata: child, Snapshot: snap, Payload: pay})
	}
	if len(parts) > 0 {
		extra := make([]string, 0, len(parts))
		for name := range parts {
			extra = append(extra, name)
		}
		slices.Sort(extra)
		return ambiguous("unexpected response parts %v", extra)
	}
	return out
}
