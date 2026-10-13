// Package control is core's private control listener. The replay service
// redrives captured failures through POST /internal/replay/v1/deliver; this
// package owns the HTTP side — authentication, concurrency, draining, the
// bounded body read, the snapshot checks, the response and its limits — and
// hands execution to a Redriver (internal/httpapi.FlowServer).
package control

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/jaysongiroux/hook_way/internal/capture"
	"github.com/jaysongiroux/hook_way/internal/deliverysnapshot"
	"github.com/jaysongiroux/hook_way/internal/logging"
	"github.com/jaysongiroux/hook_way/internal/replaywire"
)

// Block refuses a redrive for a configuration reason: the listener answers
// 422 with Code. Nothing was sent.
type Block struct {
	Code   string
	Detail string
}

func (b *Block) Error() string { return "redrive blocked (" + b.Code + "): " + b.Detail }

// Blockf builds a Block.
func Blockf(code, format string, args ...any) *Block {
	return &Block{Code: code, Detail: fmt.Sprintf(format, args...)}
}

// ErrMalformedSnapshot is returned (wrapped) when a snapshot decodes but
// cannot be turned back into a payload. Such a record can never be redriven.
var ErrMalformedSnapshot = errors.New("the snapshot cannot be reconstructed")

// Result is a redrive's outcome. Children are complete records; the listener
// applies the response limits to them.
type Result struct {
	Nodes           []replaywire.NodeOutcome
	Children        []capture.Record
	NodeConfigHash  string
	DestinationHash string
}

// Redriver runs one checked deliver request. It returns *Block when the
// current configuration refuses the record, ErrMalformedSnapshot when the
// snapshot cannot be used, and any other error when core cannot say what ran.
type Redriver interface {
	Redrive(ctx context.Context, req replaywire.DeliverRequest, snap deliverysnapshot.Snapshot) (Result, error)
}

// Metrics is the listener's instrumentation (internal/metrics implements it).
type Metrics interface {
	ControlRun(flow, outcome string)
	SetControlInflight(n int64)
}

// Run outcomes (hookway_control_runs_total).
const (
	OutcomeSucceeded  = "succeeded"
	OutcomeFailed     = "failed"
	OutcomeBlocked    = "blocked"
	OutcomeNotStarted = "not_started"
	OutcomeRejected   = "rejected"
	OutcomeError      = "error"
)

const (
	defaultMaxConcurrent = 8
	unknownFlow          = "unknown"
)

// Options configures the listener.
type Options struct {
	Addr           string
	Token          string
	MaxConcurrent  int
	MaxRecordBytes int64    // core's replay.max_record_bytes: the largest snapshot core captures
	Flows          []string // configured flow names, the metric's label set
	Redriver       Redriver
	Metrics        Metrics
	Logger         *slog.Logger
	CoreVersion    string
	CoreInstance   string
}

// Server is the control listener.
type Server struct {
	opts     Options
	token    [sha256.Size]byte
	flows    map[string]bool
	slots    chan struct{}
	inflight atomic.Int64
	running  sync.WaitGroup // requests holding a slot, redrive or not
	draining atomic.Bool
	budget   int64 // child artifact bytes one response may carry; tests lower it
	srv      *http.Server
}

type nopMetrics struct{}

func (nopMetrics) ControlRun(string, string) {}
func (nopMetrics) SetControlInflight(int64)  {}

func New(o Options) *Server {
	if o.Logger == nil {
		o.Logger = slog.Default()
	}
	if o.Metrics == nil {
		o.Metrics = nopMetrics{}
	}
	if o.MaxConcurrent <= 0 {
		o.MaxConcurrent = defaultMaxConcurrent
	}
	s := &Server{
		opts: o, token: sha256.Sum256([]byte(o.Token)), flows: make(map[string]bool, len(o.Flows)),
		slots: make(chan struct{}, o.MaxConcurrent), budget: replaywire.MaxDeliverResponseBytes - replaywire.MaxResultBytes,
	}
	for _, f := range o.Flows {
		s.flows[f] = true
	}
	mux := http.NewServeMux()
	mux.HandleFunc(replaywire.DeliverPath, s.deliver)
	s.srv = &http.Server{
		Addr: o.Addr, Handler: mux, ReadHeaderTimeout: 10 * time.Second, ReadTimeout: time.Minute,
		IdleTimeout: replaywire.ControlServerIdleTimeout,
	}
	s.srv.RegisterOnShutdown(s.Drain)
	return s
}

// HTTPServer is the listener to serve. Stop it with Server.Shutdown, not its
// own Shutdown, so in-flight redrives are waited for.
func (s *Server) HTTPServer() *http.Server { return s.srv }

// Shutdown drains the listener — requests that still arrive, or finish
// arriving, answer 503 not_started — then waits for in-flight redrives even
// past ctx. They run on detached contexts bounded by their own deadlines, and
// the publishers they deliver through must not close under them.
func (s *Server) Shutdown(ctx context.Context) error {
	s.Drain()
	err := s.srv.Shutdown(ctx)
	s.running.Wait()
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
		// Every redrive finished; only idle connections outlived ctx.
		return nil
	}
	return err
}

// Drain makes every new deliver request answer 503 not_started.
func (s *Server) Drain() { s.draining.Store(true) }

func (s *Server) authorized(r *http.Request) bool {
	h := r.Header.Get("Authorization")
	token, ok := strings.CutPrefix(h, "Bearer ")
	sum := sha256.Sum256([]byte(token))
	// Compare digests: constant time, and independent of the token's length.
	return ok && len(r.Header.Values("Authorization")) == 1 && subtle.ConstantTimeCompare(sum[:], s.token[:]) == 1
}

func (s *Server) deliver(w http.ResponseWriter, r *http.Request) {
	start := time.Now()
	outcome := OutcomeRejected
	var req replaywire.DeliverRequest
	defer func() { s.finish(req, outcome, time.Since(start)) }()
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		writeProblem(w, http.StatusMethodNotAllowed, "method_not_allowed", "the control API accepts POST only")
		return
	}
	if !s.authorized(r) {
		writeProblem(w, http.StatusUnauthorized, "unauthorized", "a valid control token is required")
		return
	}
	if s.draining.Load() {
		outcome = OutcomeNotStarted
		writeNotStarted(w, "core is shutting down")
		return
	}
	select {
	case s.slots <- struct{}{}:
		s.running.Add(1)
		defer func() {
			<-s.slots
			s.running.Done()
		}()
	default:
		outcome = OutcomeNotStarted
		writeNotStarted(w, "core is already running replay.control.max_concurrent redrives")
		return
	}
	s.opts.Metrics.SetControlInflight(s.inflight.Add(1))
	defer func() { s.opts.Metrics.SetControlInflight(s.inflight.Add(-1)) }()

	// Everything is read and checked before anything runs: a partial or
	// malformed request never executes.
	var snapshot []byte
	var err error
	if req, snapshot, err = s.readRequest(w, r); err != nil {
		outcome = s.refuse(w, req, err)
		return
	}
	if s.draining.Load() { // draining began while the body arrived
		outcome = OutcomeNotStarted
		writeNotStarted(w, "core is shutting down")
		return
	}
	snap, err := checkSnapshot(req, snapshot)
	if err != nil {
		outcome = s.refuse(w, req, err)
		return
	}
	// Detached like a first delivery's publish (I10): once started, a redrive
	// runs to its deadline even if the caller goes away.
	ctx, cancel := context.WithTimeout(context.WithoutCancel(r.Context()), time.Duration(req.DeadlineMS)*time.Millisecond)
	defer cancel()
	res, err := s.opts.Redriver.Redrive(ctx, req, snap)
	if err != nil {
		outcome = s.refuse(w, req, err)
		return
	}
	body, contentType, resume, err := s.encode(req, res)
	if err != nil {
		outcome = OutcomeError
		s.opts.Logger.Error("replay redrive produced a result core cannot report", "run_id", req.RunID,
			"record_id", req.RecordID, "error", logging.ErrorText(err))
		writeProblem(w, http.StatusInternalServerError, "internal", "core could not report the redrive's outcome")
		return
	}
	outcome = OutcomeSucceeded
	if resume == replaywire.NodeFailed {
		outcome = OutcomeFailed
	}
	w.Header().Set("Content-Type", contentType)
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(body) // #nosec G705 -- multipart/form-data this package built for replay's executor, never rendered as HTML
}

// refuse answers an error from any stage and returns the run outcome.
func (s *Server) refuse(w http.ResponseWriter, req replaywire.DeliverRequest, err error) string {
	var block *Block
	var bad *requestError
	switch {
	case errors.As(err, &block):
		writeProblem(w, http.StatusUnprocessableEntity, block.Code, block.Detail)
		return OutcomeBlocked
	case errors.As(err, &bad):
		writeProblem(w, bad.status, bad.code, bad.detail)
		return OutcomeRejected
	case errors.Is(err, ErrMalformedSnapshot):
		writeProblem(w, http.StatusBadRequest, "malformed_snapshot", err.Error())
		return OutcomeRejected
	}
	s.opts.Logger.Error("replay redrive failed inside core", "run_id", req.RunID, "record_id", req.RecordID, "error", logging.ErrorText(err))
	writeProblem(w, http.StatusInternalServerError, "internal", "core could not complete the redrive")
	return OutcomeError
}

func (s *Server) finish(req replaywire.DeliverRequest, outcome string, took time.Duration) {
	flow := unknownFlow
	if s.flows[req.Flow] {
		flow = req.Flow
	}
	s.opts.Metrics.ControlRun(flow, outcome)
	s.opts.Logger.Info("replay redrive", "run_id", req.RunID, "record_id", req.RecordID, "flow", req.Flow,
		"node_id", req.NodeID, "kind", req.Kind, "replay_mode", req.ReplayMode, "outcome", outcome, "duration_ms", took.Milliseconds())
}
