package httpapi

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
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
	verifiers        map[string]auth.Verifier
	trustedProxies   []*net.IPNet
	inflight         chan struct{}
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
		verifiers:        map[string]auth.Verifier{},
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
		// Build the per-endpoint signature verifier once at startup. An empty
		// scheme defaults to Paddle so configs predating the scheme field keep
		// working; an unknown scheme fails closed (every request rejected).
		verifier, err := auth.NewVerifier(auth.Spec{
			Scheme:             defaultScheme(endpoint.Auth.Scheme),
			Secret:             endpoint.Auth.Secret,
			TimestampTolerance: endpoint.Auth.TimestampTolerance,
		})
		if err != nil {
			logger.Error("signature scheme unavailable; endpoint will reject all requests", "endpoint", name, "scheme", endpoint.Auth.Scheme, "error", err.Error())
			verifier = auth.RejectAll{}
		}
		s.verifiers[name] = verifier
	}
	s.trustedProxies = parseCIDRs(cfg.Security.TrustedProxies)
	if cfg.Server.MaxInflightRequests > 0 {
		s.inflight = make(chan struct{}, cfg.Server.MaxInflightRequests)
	}
	s.metrics.RegisterConfig(cfg)
	s.routes()
	return s
}

// parseCIDRs accepts bare IPs and CIDR ranges, normalising bare IPs to a
// single-host network. Invalid entries are dropped (config validation rejects
// them before serving).
func parseCIDRs(entries []string) []*net.IPNet {
	var nets []*net.IPNet
	for _, entry := range entries {
		if _, network, err := net.ParseCIDR(entry); err == nil {
			nets = append(nets, network)
			continue
		}
		if ip := net.ParseIP(entry); ip != nil {
			bits := 32
			if ip.To4() == nil {
				bits = 128
			}
			nets = append(nets, &net.IPNet{IP: ip, Mask: net.CIDRMask(bits, bits)})
		}
	}
	return nets
}

func (s *Server) routes() {
	s.mux.HandleFunc("/health", s.handleReady)
	s.mux.HandleFunc("/healthz", s.handleLive)
	s.mux.HandleFunc("/ready", s.handleReady)
	s.mux.HandleFunc("/readyz", s.handleReady)
	// Mount metrics on the public mux only when no dedicated metrics listener is
	// configured. With metrics.addr set, /metrics is served on its own port (see
	// MetricsServer) and kept off the internet-facing webhook port.
	if s.cfg.Metrics.Enabled && s.cfg.Metrics.Addr == "" {
		s.mux.Handle(s.cfg.Metrics.Path, s.metricsHandler())
	}
	s.mux.HandleFunc("/", s.handleWebhook)
}

func (s *Server) metricsHandler() http.Handler {
	return promhttp.HandlerFor(s.metrics.Registry(), promhttp.HandlerOpts{})
}

// MetricsServer returns a dedicated *http.Server exposing only the metrics
// endpoint, or nil when metrics are disabled or share the main port.
func (s *Server) MetricsServer() *http.Server {
	if !s.cfg.Metrics.Enabled || s.cfg.Metrics.Addr == "" {
		return nil
	}
	mux := http.NewServeMux()
	mux.Handle(s.cfg.Metrics.Path, s.metricsHandler())
	return &http.Server{
		Addr:         s.cfg.Metrics.Addr,
		Handler:      mux,
		ReadTimeout:  s.cfg.Server.ReadTimeout,
		WriteTimeout: s.cfg.Server.WriteTimeout,
		IdleTimeout:  s.cfg.Server.IdleTimeout,
	}
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
	endpointLabel, destinationLabel, queueLabel := "", "", ""
	defer func() {
		s.metrics.HTTP(endpointLabel, destinationLabel, queueLabel, status)
		if endpointLabel != "" {
			s.metrics.RequestDuration(endpointLabel, destinationLabel, queueLabel, status, time.Since(start))
		}
		hooklogging.Request(s.logger, hooklogging.RequestAttrs{
			Method: r.Method, Path: r.URL.Path, Endpoint: endpointLabel, DestinationQueueID: destinationLabel, Queue: queueLabel,
			Status: status, Duration: time.Since(start), RequestID: requestIDFromContext(r.Context()),
			TraceID: traceIDFromContext(r.Context()), Headers: r.Header,
			HeaderDenylist:  s.cfg.Security.RedactedHeaderDenylist,
			HeaderAllowlist: s.cfg.Security.ForwardedHeaderAllowlist,
		})
	}()
	endpointName, endpoint, ok := s.cfg.EndpointForPath(r.URL.Path)
	if !ok {
		status = http.StatusNotFound
		writeJSON(w, status, map[string]string{"error": "not found"})
		return
	}
	endpointLabel, destinationLabel, queueLabel = endpointName, endpoint.DestinationQueueID, endpoint.Queue
	if r.Method != http.MethodPost {
		status = http.StatusMethodNotAllowed
		w.Header().Set("Allow", http.MethodPost)
		writeJSON(w, status, map[string]string{"error": "method not allowed"})
		return
	}
	// Backpressure: bound concurrent webhook processing so a flood cannot
	// exhaust memory by buffering unbounded bodies. Acquired before reading the
	// body and released when handling completes.
	if s.inflight != nil {
		select {
		case s.inflight <- struct{}{}:
			defer func() { <-s.inflight }()
		default:
			status = http.StatusServiceUnavailable
			w.Header().Set("Retry-After", "1")
			s.metrics.Saturated(endpointName, endpoint.DestinationQueueID, endpoint.Queue)
			writeJSON(w, status, map[string]string{"error": "server busy"})
			return
		}
	}
	sourceIP := s.sourceIP(r)
	if !s.ipAllowed(sourceIP, endpoint.IPAllowlist) {
		status = http.StatusForbidden
		s.metrics.Forbidden(endpointName, endpoint.DestinationQueueID, endpoint.Queue)
		writeJSON(w, status, map[string]string{"error": "forbidden"})
		return
	}
	if !s.limiterForEndpoint(endpointName).Allow(sourceIP, time.Now()) {
		status = http.StatusTooManyRequests
		s.metrics.RateLimited(endpointName, endpoint.DestinationQueueID, endpoint.Queue)
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
	s.metrics.BodyBytes(endpointName, endpoint.DestinationQueueID, endpoint.Queue, len(rawBody))
	if err := s.verifiers[endpointName].Verify(r.Header.Get(endpoint.Auth.SignatureHeader), rawBody, time.Now()); err != nil {
		status = http.StatusUnauthorized
		s.metrics.AuthFailure(endpointName, "invalid_signature")
		writeJSON(w, status, map[string]string{"error": "unauthorized"})
		return
	}
	// Reserve the signature against replays. The reservation is released on any
	// failure before a successful publish so a transient outage does not turn a
	// sender's legitimate retry into a permanent "replay detected" rejection.
	signature := r.Header.Get(endpoint.Auth.SignatureHeader)
	replayCache := s.replayCaches[endpointName]
	if replayCache != nil {
		if !replayCache.Accept(endpointName, signature, time.Now()) {
			status = http.StatusConflict
			s.metrics.ReplayRejected(endpointName, endpoint.DestinationQueueID, endpoint.Queue)
			writeJSON(w, status, map[string]string{"error": "replay detected"})
			return
		}
	}
	releaseReplay := func() {
		if replayCache != nil {
			replayCache.Release(endpointName, signature)
		}
	}
	env, err := webhook.BuildEnvelopeWithOptions(r, webhook.EnvelopeOptions{
		Endpoint: endpointName, DestinationQueueID: endpoint.DestinationQueueID, Queue: endpoint.Queue, RawBody: rawBody, ReceivedAt: time.Now(), SourceIP: sourceIP,
		RequestID: requestIDFromContext(r.Context()), TraceID: traceIDFromContext(r.Context()),
		AuthType: endpoint.Auth.Type, AuthScheme: defaultScheme(endpoint.Auth.Scheme),
		IdempotencyKey:  s.idempotencyKey(r, endpoint),
		HeaderDenylist:  s.cfg.Security.RedactedHeaderDenylist,
		HeaderAllowlist: s.cfg.Security.ForwardedHeaderAllowlist,
	})
	if err != nil {
		releaseReplay()
		status = http.StatusInternalServerError
		writeJSON(w, status, map[string]string{"error": "internal error"})
		return
	}
	msg := queue.Message{
		SchemaVersion: env.SchemaVersion, ID: env.ID, IdempotencyKey: env.IdempotencyKey, RequestID: env.RequestID, TraceID: env.TraceID,
		Endpoint: env.Endpoint, DestinationQueueID: env.DestinationQueueID, Queue: env.Queue, ReceivedAt: env.ReceivedAt,
		Method: env.Method, Path: env.Path, Headers: env.Headers, Query: env.Query,
		Body: env.Body, RawBodyBase64: env.RawBodyBase64, SourceIP: env.SourceIP, Auth: env.Auth,
	}
	publishStart := time.Now()
	if err := s.publisher.Publish(r.Context(), endpoint.DestinationQueueID, endpoint.Queue, msg); err != nil {
		releaseReplay()
		status = http.StatusServiceUnavailable
		s.metrics.Publish(endpointName, endpoint.DestinationQueueID, endpoint.Queue, "failure", time.Since(publishStart))
		writeJSON(w, status, map[string]string{"error": "service unavailable"})
		return
	}
	s.metrics.Publish(endpointName, endpoint.DestinationQueueID, endpoint.Queue, "success", time.Since(publishStart))
	s.metrics.MessagePublished(endpointName, endpoint.DestinationQueueID, endpoint.Queue)
	status = http.StatusAccepted
	writeJSON(w, status, map[string]string{
		"status": "accepted", "endpoint": endpointName, "destination_queue_id": endpoint.DestinationQueueID, "queue": endpoint.Queue, "event_id": env.ID, "request_id": env.RequestID,
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
	peer := peerHost(r.RemoteAddr)
	// Only honour forwarding headers when the immediate peer is a trusted
	// proxy. Otherwise a client could spoof X-Forwarded-For to bypass the IP
	// allowlist and per-IP rate limiting.
	if s.trustsPeer(peer) {
		if forwarded := r.Header.Get("X-Forwarded-For"); forwarded != "" {
			return strings.TrimSpace(strings.Split(forwarded, ",")[0])
		}
		if realIP := strings.TrimSpace(r.Header.Get("X-Real-IP")); realIP != "" {
			return realIP
		}
	}
	return peer
}

// trustsPeer reports whether forwarding headers from this immediate peer should
// be honoured. Trust requires the trusted_proxy_headers flag; if trusted_proxies
// is configured the peer must also fall inside one of those ranges.
func (s *Server) trustsPeer(peer string) bool {
	if !s.cfg.Security.TrustedProxyHeaders {
		return false
	}
	if len(s.trustedProxies) == 0 {
		return true
	}
	ip := net.ParseIP(peer)
	if ip == nil {
		return false
	}
	for _, network := range s.trustedProxies {
		if network.Contains(ip) {
			return true
		}
	}
	return false
}

func peerHost(remoteAddr string) string {
	host, _, err := net.SplitHostPort(remoteAddr)
	if err != nil {
		return remoteAddr
	}
	return host
}

// idempotencyKey returns a stable per-event key consumers can dedupe on. It
// prefers a configured provider delivery-id header and otherwise falls back to
// a hash of the verified signature, which is stable across provider retries of
// the same event.
func (s *Server) idempotencyKey(r *http.Request, endpoint config.EndpointConfig) string {
	if header := endpoint.Auth.IdempotencyHeader; header != "" {
		if v := strings.TrimSpace(r.Header.Get(header)); v != "" {
			return v
		}
	}
	sig := r.Header.Get(endpoint.Auth.SignatureHeader)
	if sig == "" {
		return ""
	}
	sum := sha256.Sum256([]byte(endpoint.Auth.SignatureHeader + ":" + sig))
	return hex.EncodeToString(sum[:])
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

func defaultScheme(scheme string) string {
	if scheme == "" {
		return auth.SchemePaddle
	}
	return scheme
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
