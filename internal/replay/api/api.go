// Package api serves the replay service's HTTP API: ingest from core, and
// inspection, payload download, retry, and dismiss for operators. Every
// error is RFC 9457 problem+json with a stable code.
package api

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
	"strings"
	"time"

	"github.com/jaysongiroux/hook_way/internal/problem"
	"github.com/jaysongiroux/hook_way/internal/replay/auth"
	"github.com/jaysongiroux/hook_way/internal/replay/metrics"
	"github.com/jaysongiroux/hook_way/internal/replay/store"
	"github.com/jaysongiroux/hook_way/internal/uicontract"
)

// Store is the persistence the API needs; *store.Store implements it.
type Store interface {
	Ingest(ctx context.Context, f store.NewFailure) (store.IngestResult, error)
	List(ctx context.Context, f store.Filter) (store.Page, error)
	Get(ctx context.Context, id string) (store.Detail, error)
	Payload(ctx context.Context, id, actor string) (store.Payload, error)
	RequestRetry(ctx context.Context, req store.RetryRequest) (store.RetryResult, error)
	GetRun(ctx context.Context, runID string) (store.Run, error)
	Dismiss(ctx context.Context, id, actor string) (store.Failure, error)
	ResolveUnknown(ctx context.Context, req store.ResolveRequest) (store.ResolveResult, error)
	Ready(ctx context.Context) error
}

// Options configures New.
type Options struct {
	Store          Store
	Auth           *auth.Authenticator
	Metrics        *metrics.Metrics
	Logger         *slog.Logger
	MaxRecordBytes int64
	// MetricsPath and MetricsHandler, when both set, serve the Prometheus
	// endpoint on this handler (metrics.addr empty).
	MetricsPath    string
	MetricsHandler http.Handler
	AllowedOrigins []string
}

const (
	readyTimeout     = 2 * time.Second
	maxMetadataBytes = 1 << 20
	maxJSONBodyBytes = 64 << 10
	maxBatchSize     = uicontract.MaxBatchSize
	maxKeyLen        = uicontract.MaxIdempotencyKeyLength
)

type api struct {
	store          Store
	metrics        *metrics.Metrics
	logger         *slog.Logger
	maxRecordBytes int64
}

// New returns the API handler.
func New(opts Options) http.Handler {
	a := &api{store: opts.Store, metrics: opts.Metrics, logger: opts.Logger, maxRecordBytes: opts.MaxRecordBytes}
	if a.logger == nil {
		a.logger = slog.Default()
	}
	mux := http.NewServeMux()
	operatorRoutes := http.NewServeMux()
	handlers := map[string]http.HandlerFunc{"validateAction": a.validateAction, "whoAmI": a.me, "resolveFailure": a.resolve, "captureFailure": a.ingest, "listFailures": a.list, "getFailure": a.get, "downloadPayload": a.payload, "retryFailure": a.retry, "dismissFailure": a.dismiss, "retryFailures": a.retryBatch, "getRetry": a.getRun, "health": a.healthz, "ready": a.readyz}
	for _, route := range Routes() {
		handler := handlers[route.OperationID]
		if handler == nil {
			panic("replay route has no handler: " + route.OperationID)
		}
		if route.Scope != "" && route.Scope != string(auth.ScopeCapture) {
			operatorRoutes.Handle(route.Pattern(), handler)
		}
		if route.Scope != "" {
			mux.Handle(route.Pattern(), opts.Auth.Require(auth.Scope(route.Scope), handler))
		} else {
			mux.Handle(route.Pattern(), handler)
		}
	}
	if opts.MetricsPath != "" && opts.MetricsHandler != nil {
		mux.Handle(opts.MetricsPath, opts.Auth.Require(auth.ScopeMetrics, opts.MetricsHandler))
	}
	return a.instrument(operatorCORS(mux, operatorRoutes, opts.AllowedOrigins))
}

var knownMethods = map[string]bool{
	http.MethodGet: true, http.MethodHead: true, http.MethodPost: true, http.MethodPut: true,
	http.MethodPatch: true, http.MethodDelete: true, http.MethodOptions: true,
}

// routeLabel is the matched pattern's path, or "unmatched". ServeMux sets
// r.Pattern on the request it routes, which is this one.
func routeLabel(pattern string) string {
	if pattern == "" {
		return "unmatched"
	}
	if _, path, ok := strings.Cut(pattern, " "); ok {
		return path
	}
	return pattern
}

type statusWriter struct {
	http.ResponseWriter
	status int
	wrote  bool
}

func (w *statusWriter) WriteHeader(code int) {
	if !w.wrote {
		w.status, w.wrote = code, true
	}
	w.ResponseWriter.WriteHeader(code)
}

func (w *statusWriter) Write(b []byte) (int, error) {
	if !w.wrote {
		w.status, w.wrote = http.StatusOK, true
	}
	return w.ResponseWriter.Write(b)
}

// instrument records every request and turns a handler panic into a 500.
func (a *api) instrument(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		sw := &statusWriter{ResponseWriter: w, status: http.StatusOK}
		defer func() {
			if rec := recover(); rec != nil {
				a.metrics.Panic("handler")
				a.logger.Error("panic recovered in http handler", "route", routeLabel(r.Pattern),
					"panic", fmt.Sprint(rec), "stack", string(debug.Stack()))
				if !sw.wrote {
					problem.Write(sw, http.StatusInternalServerError, "internal", "internal error")
				}
			}
			method := r.Method
			if !knownMethods[method] {
				method = "other"
			}
			a.metrics.HTTPRequest(routeLabel(r.Pattern), method, sw.status, time.Since(start))
		}()
		next.ServeHTTP(sw, r)
	})
}

// apiError is a request problem with its response.
type apiError struct {
	status int
	code   string
	detail string
}

func (e *apiError) Error() string { return e.detail }

func newError(status int, code, format string, args ...any) *apiError {
	return &apiError{status: status, code: code, detail: fmt.Sprintf(format, args...)}
}

var storeErrors = []struct {
	err    error
	status int
	code   string
}{
	{store.ErrNotResolvable, http.StatusUnprocessableEntity, "not_resolvable"},
	{store.ErrResolutionConflict, http.StatusConflict, "resolution_conflict"},
	{store.ErrInvalidResolution, http.StatusBadRequest, "invalid_resolution"},
	{store.ErrExpired, http.StatusGone, "record_expired"},
	{store.ErrNotFound, http.StatusNotFound, "not_found"},
	{store.ErrConflict, http.StatusConflict, "conflict"},
	{store.ErrCapacity, http.StatusInsufficientStorage, "capacity_exceeded"},
	{store.ErrBadCursor, http.StatusBadRequest, "invalid_cursor"},
	{store.ErrNoPayload, http.StatusNotFound, "payload_not_captured"},
	{store.ErrGone, http.StatusGone, "payload_gone"},
	{store.ErrCorrupt, http.StatusInternalServerError, "payload_corrupt"},
	{store.ErrInvalidState, http.StatusUnprocessableEntity, "not_dismissable"},
	{store.ErrRunActive, http.StatusConflict, "run_active"},
	{store.ErrNotRetryable, http.StatusUnprocessableEntity, "not_retryable"},
	{store.ErrDuplicateRiskUnacknowledged, http.StatusUnprocessableEntity, "duplicate_risk_unacknowledged"},
	{store.ErrIdempotencyKeyMismatch, http.StatusUnprocessableEntity, "idempotency_key_mismatch"},
}

// classify maps request and store errors to their responses; nil means the
// error is unexpected.
func classify(err error) *apiError {
	var ae *apiError
	if errors.As(err, &ae) {
		return ae
	}
	for _, m := range storeErrors {
		if errors.Is(err, m.err) {
			return &apiError{status: m.status, code: m.code, detail: strings.TrimPrefix(m.err.Error(), "store: ")}
		}
	}
	return nil
}

// fail writes err's response. An unexpected error is logged and answered
// with a generic 500, so internal detail (hosts, SQL) never reaches clients.
func (a *api) fail(w http.ResponseWriter, r *http.Request, err error) {
	if ae := classify(err); ae != nil {
		problem.Write(w, ae.status, ae.code, ae.detail)
		return
	}
	a.logger.Error("replay api request failed", "route", routeLabel(r.Pattern), "error", err)
	problem.Write(w, http.StatusInternalServerError, "internal", "internal error")
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// decodeJSON reads a small JSON body strictly. An empty body is allowed only
// when allowEmpty is set, and then leaves v at its zero value.
func decodeJSON(w http.ResponseWriter, r *http.Request, v any, allowEmpty bool) error {
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxJSONBodyBytes))
	if err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			return newError(http.StatusRequestEntityTooLarge, "request_too_large", "the body exceeds %d bytes", maxJSONBodyBytes)
		}
		return newError(http.StatusBadRequest, "invalid_request", "read body: %v", err)
	}
	if len(bytes.TrimSpace(body)) == 0 {
		if allowEmpty {
			return nil
		}
		return newError(http.StatusBadRequest, "invalid_request", "a JSON body is required")
	}
	dec := json.NewDecoder(bytes.NewReader(body))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		return newError(http.StatusBadRequest, "invalid_request", "invalid JSON body: %v", err)
	}
	if err := dec.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return newError(http.StatusBadRequest, "invalid_request", "the JSON body has trailing data")
	}
	return nil
}

func (a *api) healthz(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	_, _ = w.Write([]byte("ok\n"))
}

// readyz reports whether the database answers and its schema is current. The
// cause is logged, not returned: the endpoint is unauthenticated.
func (a *api) readyz(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), readyTimeout)
	defer cancel()
	if err := a.store.Ready(ctx); err != nil {
		a.logger.Warn("readiness check failed", "error", err)
		problem.Write(w, http.StatusServiceUnavailable, "not_ready", "the database is unreachable or its schema is behind this binary")
		return
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	_, _ = w.Write([]byte("ready\n"))
}
