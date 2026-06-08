package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/prometheus/client_golang/prometheus/promhttp"

	"github.com/jasongiroux/hookway/internal/auth"
	"github.com/jasongiroux/hookway/internal/config"
	hooklogging "github.com/jasongiroux/hookway/internal/logging"
	"github.com/jasongiroux/hookway/internal/metrics"
	"github.com/jasongiroux/hookway/internal/queue"
	"github.com/jasongiroux/hookway/internal/ratelimit"
	"github.com/jasongiroux/hookway/internal/replay"
	"github.com/jasongiroux/hookway/internal/webhook"
)

type Server struct {
	cfg              config.Config
	publisher        queue.Publisher
	logger           *slog.Logger
	limiter          *ratelimit.Limiter
	endpointLimiters map[string]*ratelimit.Limiter
	replayCaches     map[string]*replay.Cache
	metrics          *metrics.Metrics
	mux              *http.ServeMux
}

func NewServer(cfg config.Config, publisher queue.Publisher, logger *slog.Logger) *Server {
	return NewServerWithMetrics(cfg, publisher, logger, metrics.New())
}

func NewServerWithMetrics(cfg config.Config, publisher queue.Publisher, logger *slog.Logger, appMetrics *metrics.Metrics) *Server {
	if logger == nil {
		logger = slog.Default()
	}
	if appMetrics == nil {
		appMetrics = metrics.New()
	}
	s := &Server{
		cfg:              cfg,
		publisher:        publisher,
		logger:           logger,
		limiter:          ratelimit.New(cfg.RateLimit.Enabled, cfg.RateLimit.RequestsPerMinute, cfg.RateLimit.Burst),
		endpointLimiters: map[string]*ratelimit.Limiter{},
		replayCaches:     map[string]*replay.Cache{},
		metrics:          appMetrics,
		mux:              http.NewServeMux(),
	}
	for name, endpoint := range cfg.Endpoints {
		if endpoint.RateLimit.Enabled != nil {
			s.endpointLimiters[name] = ratelimit.New(*endpoint.RateLimit.Enabled, endpoint.RateLimit.RequestsPerMinute, endpoint.RateLimit.Burst)
		}
		if endpoint.ReplayProtection.Enabled {
			// In-memory replay cache is process-local. Multi-node deployments need shared storage.
			s.replayCaches[name] = replay.NewCache(endpoint.ReplayProtection.TTL, endpoint.ReplayProtection.MaxEntries)
		}
	}
	s.routes()
	return s
}

func (s *Server) routes() {
	s.mux.HandleFunc("/health", s.handleReady)
	s.mux.HandleFunc("/healthz", s.handleLive)
	s.mux.HandleFunc("/ready", s.handleReady)
	s.mux.HandleFunc("/readyz", s.handleReady)
	if s.cfg.Metrics.Enabled {
		s.mux.Handle(s.cfg.Metrics.Path, promhttp.HandlerFor(s.metrics.Registry(), promhttp.HandlerOpts{}))
	}
	s.mux.HandleFunc("/", s.handleWebhook)
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Cache-Control", "no-store")
	requestID := s.requestID(r)
	traceID := traceIDFromRequest(r)
	w.Header().Set("X-Request-ID", requestID)
	ctx := context.WithValue(r.Context(), requestIDKey{}, requestID)
	ctx = context.WithValue(ctx, traceIDKey{}, traceID)
	r = r.WithContext(ctx)
	s.metrics.Inflight(1)
	defer s.metrics.Inflight(-1)
	s.mux.ServeHTTP(w, r)
}

func (s *Server) HTTPServer() *http.Server {
	return &http.Server{
		Addr:         s.cfg.Server.Addr,
		Handler:      s,
		ReadTimeout:  s.cfg.Server.ReadTimeout,
		WriteTimeout: s.cfg.Server.WriteTimeout,
		IdleTimeout:  s.cfg.Server.IdleTimeout,
	}
}

func (s *Server) handleLive(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *Server) handleReady(w http.ResponseWriter, r *http.Request) {
	if err := s.publisher.Ready(r.Context()); err != nil {
		s.logger.Error("readiness check failed", "error", err.Error(), "request_id", requestIDFromContext(r.Context()), "trace_id", traceIDFromContext(r.Context()))
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "not ready"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ready"})
}

func (s *Server) handleWebhook(w http.ResponseWriter, r *http.Request) {
	start := time.Now()
	status := http.StatusInternalServerError
	endpointLabel, queueLabel := "", ""
	defer func() {
		s.metrics.HTTP(endpointLabel, queueLabel, status)
		hooklogging.Request(s.logger, hooklogging.RequestAttrs{
			Method: r.Method, Path: r.URL.Path, Endpoint: endpointLabel, Queue: queueLabel,
			Status: status, Duration: time.Since(start), RequestID: requestIDFromContext(r.Context()),
			TraceID: traceIDFromContext(r.Context()), Headers: r.Header,
			HeaderDenylist: s.cfg.Security.RedactedHeaderDenylist,
		})
	}()
	endpointName, endpoint, ok := s.cfg.EndpointForPath(r.URL.Path)
	if !ok {
		status = http.StatusNotFound
		writeJSON(w, status, map[string]string{"error": "not found"})
		return
	}
	endpointLabel, queueLabel = endpointName, endpoint.Queue
	if r.Method != http.MethodPost {
		status = http.StatusMethodNotAllowed
		w.Header().Set("Allow", http.MethodPost)
		writeJSON(w, status, map[string]string{"error": "method not allowed"})
		return
	}
	sourceIP := s.sourceIP(r)
	if !s.ipAllowed(sourceIP, endpoint.IPAllowlist) {
		status = http.StatusForbidden
		writeJSON(w, status, map[string]string{"error": "forbidden"})
		return
	}
	if !s.limiterForEndpoint(endpointName).Allow(sourceIP, time.Now()) {
		status = http.StatusTooManyRequests
		s.metrics.RateLimited(endpointName, endpoint.Queue)
		writeJSON(w, status, map[string]string{"error": "rate limited"})
		return
	}
	rawBody, err := readBody(w, r, s.maxBodyBytes(endpoint))
	if err != nil {
		if errors.Is(err, errBodyTooLarge) {
			status = http.StatusRequestEntityTooLarge
			writeJSON(w, status, map[string]string{"error": "payload too large"})
			return
		}
		status = http.StatusBadRequest
		writeJSON(w, status, map[string]string{"error": "invalid body"})
		return
	}
	if len(rawBody) == 0 {
		status = http.StatusBadRequest
		writeJSON(w, status, map[string]string{"error": "empty body"})
		return
	}
	s.metrics.BodyBytes(endpointName, endpoint.Queue, len(rawBody))
	verifier := auth.NewHMACSHA256Verifier(endpoint.Auth.Secret, endpoint.Auth.TimestampTolerance)
	if err := verifier.Verify(r.Header.Get(endpoint.Auth.SignatureHeader), rawBody, time.Now()); err != nil {
		status = http.StatusUnauthorized
		s.metrics.AuthFailure(endpointName, "invalid_signature")
		writeJSON(w, status, map[string]string{"error": "unauthorized"})
		return
	}
	if cache := s.replayCaches[endpointName]; cache != nil {
		if !cache.Accept(endpointName, r.Header.Get(endpoint.Auth.SignatureHeader), time.Now()) {
			status = http.StatusConflict
			writeJSON(w, status, map[string]string{"error": "replay detected"})
			return
		}
	}
	env, err := webhook.BuildEnvelopeWithOptions(r, webhook.EnvelopeOptions{
		Endpoint: endpointName, Queue: endpoint.Queue, RawBody: rawBody, ReceivedAt: time.Now(), SourceIP: sourceIP,
		RequestID: requestIDFromContext(r.Context()), TraceID: traceIDFromContext(r.Context()),
		HeaderDenylist: s.cfg.Security.RedactedHeaderDenylist,
	})
	if err != nil {
		status = http.StatusInternalServerError
		writeJSON(w, status, map[string]string{"error": "internal error"})
		return
	}
	msg := queue.Message{
		SchemaVersion: env.SchemaVersion, ID: env.ID, RequestID: env.RequestID, TraceID: env.TraceID,
		Endpoint: env.Endpoint, Queue: env.Queue, ReceivedAt: env.ReceivedAt,
		Method: env.Method, Path: env.Path, Headers: env.Headers, Query: env.Query,
		Body: env.Body, RawBodyBase64: env.RawBodyBase64, SourceIP: env.SourceIP, Auth: env.Auth,
	}
	publishStart := time.Now()
	if err := s.publisher.Publish(r.Context(), endpoint.Queue, msg); err != nil {
		status = http.StatusServiceUnavailable
		s.metrics.Publish(endpointName, endpoint.Queue, "failure", time.Since(publishStart))
		writeJSON(w, status, map[string]string{"error": "service unavailable"})
		return
	}
	s.metrics.Publish(endpointName, endpoint.Queue, "success", time.Since(publishStart))
	status = http.StatusAccepted
	writeJSON(w, status, map[string]string{
		"status": "accepted", "endpoint": endpointName, "queue": endpoint.Queue, "event_id": env.ID, "request_id": env.RequestID,
	})
}

var errBodyTooLarge = errors.New("body too large")

func readBody(w http.ResponseWriter, r *http.Request, limit int64) ([]byte, error) {
	r.Body = http.MaxBytesReader(w, r.Body, limit)
	body, err := io.ReadAll(r.Body)
	if err != nil {
		var maxBytesErr *http.MaxBytesError
		if errors.As(err, &maxBytesErr) {
			return nil, errBodyTooLarge
		}
		return nil, err
	}
	return body, nil
}

func (s *Server) sourceIP(r *http.Request) string {
	if s.cfg.Security.TrustedProxyHeaders {
		if forwarded := r.Header.Get("X-Forwarded-For"); forwarded != "" {
			return strings.TrimSpace(strings.Split(forwarded, ",")[0])
		}
		if realIP := r.Header.Get("X-Real-IP"); realIP != "" {
			return realIP
		}
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

func (s *Server) requestID(r *http.Request) string {
	header := s.cfg.Security.RequestIDHeader
	if header == "" {
		header = "X-Request-ID"
	}
	if id := strings.TrimSpace(r.Header.Get(header)); id != "" {
		return id
	}
	return uuid.NewString()
}

func traceIDFromRequest(r *http.Request) string {
	if traceparent := r.Header.Get("Traceparent"); traceparent != "" {
		parts := strings.Split(traceparent, "-")
		if len(parts) >= 2 && len(parts[1]) == 32 {
			return parts[1]
		}
	}
	if id := strings.TrimSpace(r.Header.Get("X-Trace-ID")); id != "" {
		return id
	}
	return strings.ReplaceAll(uuid.NewString(), "-", "")
}

type requestIDKey struct{}
type traceIDKey struct{}

func requestIDFromContext(ctx context.Context) string {
	id, _ := ctx.Value(requestIDKey{}).(string)
	return id
}

func traceIDFromContext(ctx context.Context) string {
	id, _ := ctx.Value(traceIDKey{}).(string)
	return id
}

func (s *Server) limiterForEndpoint(name string) *ratelimit.Limiter {
	if limiter, ok := s.endpointLimiters[name]; ok {
		return limiter
	}
	return s.limiter
}

func (s *Server) maxBodyBytes(endpoint config.EndpointConfig) int64 {
	if endpoint.MaxBodyBytes > 0 {
		return endpoint.MaxBodyBytes
	}
	return s.cfg.Server.MaxBodyBytes
}

func (s *Server) ipAllowed(sourceIP string, allowlist []string) bool {
	if len(allowlist) == 0 {
		return true
	}
	ip := net.ParseIP(sourceIP)
	if ip == nil {
		return false
	}
	for _, entry := range allowlist {
		if allowed := net.ParseIP(entry); allowed != nil && allowed.Equal(ip) {
			return true
		}
		if _, network, err := net.ParseCIDR(entry); err == nil && network.Contains(ip) {
			return true
		}
	}
	return false
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

func Shutdown(ctx context.Context, srv *http.Server) error {
	return srv.Shutdown(ctx)
}
