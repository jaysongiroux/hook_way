package httpapi

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"runtime/debug"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/prometheus/client_golang/prometheus/promhttp"

	"github.com/jasongiroux/hookway/internal/apikey"
	"github.com/jasongiroux/hookway/internal/auth"
	"github.com/jasongiroux/hookway/internal/config"
	"github.com/jasongiroux/hookway/internal/filter"
	hooklogging "github.com/jasongiroux/hookway/internal/logging"
	"github.com/jasongiroux/hookway/internal/lookup"
	"github.com/jasongiroux/hookway/internal/metrics"
	"github.com/jasongiroux/hookway/internal/queue"
	"github.com/jasongiroux/hookway/internal/ratelimit"
	"github.com/jasongiroux/hookway/internal/replay"
	"github.com/jasongiroux/hookway/internal/replaystore"
	"github.com/jasongiroux/hookway/internal/vars"
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
	endpointFilters  map[string]filter.Chain
	routeFilters     map[string][]filter.Chain
	endpointVars     map[string]endpointVariables
	literalPaths     map[string]string // exact endpoint path -> endpoint name
	patternPaths     []patternPath     // endpoints whose path has {segment} placeholders
	trustedProxies   []*net.IPNet
	inflight         chan struct{}
	metrics          *metrics.Metrics
	readiness        *readinessProber
	mux              *http.ServeMux
	// replay is the webhook-replay capture seam, nil when replay capture is
	// disabled (config.Replay.Enabled == false or not yet wired via
	// SetReplayCapturer). Every use of it in handleWebhook is best-effort: an
	// error or panic never changes the webhook response.
	replay ReplayCapturer
}

func NewServer(cfg config.Config, publisher queue.Publisher, logger *slog.Logger) *Server {
	return NewServerWithMetrics(cfg, publisher, logger, metrics.New())
}

// ServerOptions carries optional dependencies for NewServerWithOptions.
type ServerOptions struct {
	// LookupFactory builds lookup-source variable providers; nil means lookup.New.
	LookupFactory LookupFactory
}

func NewServerWithMetrics(cfg config.Config, publisher queue.Publisher, logger *slog.Logger, appMetrics *metrics.Metrics) *Server {
	return NewServerWithOptions(cfg, publisher, logger, appMetrics, ServerOptions{})
}

func NewServerWithOptions(cfg config.Config, publisher queue.Publisher, logger *slog.Logger, appMetrics *metrics.Metrics, opts ServerOptions) *Server {
	if logger == nil {
		logger = slog.Default()
	}
	if appMetrics == nil {
		appMetrics = metrics.New()
	}
	factory := opts.LookupFactory
	if factory == nil {
		factory = lookup.New
	}
	s := &Server{
		cfg:              cfg,
		publisher:        publisher,
		logger:           logger,
		limiter:          ratelimit.New(cfg.RateLimit.Enabled, cfg.RateLimit.RequestsPerMinute, cfg.RateLimit.Burst),
		endpointLimiters: map[string]*ratelimit.Limiter{},
		replayCaches:     map[string]*replay.Cache{},
		verifiers:        map[string]auth.Verifier{},
		endpointFilters:  map[string]filter.Chain{},
		routeFilters:     map[string][]filter.Chain{},
		endpointVars:     map[string]endpointVariables{},
		literalPaths:     map[string]string{},
		metrics:          appMetrics,
		mux:              http.NewServeMux(),
	}
	s.readiness = newReadinessProber(publisher, appMetrics, logger, cfg.Server.ReadinessInterval, cfg.Server.ReadinessTimeout)
	for name, endpoint := range cfg.Endpoints {
		if endpoint.RateLimit.Enabled != nil {
			s.endpointLimiters[name] = ratelimit.New(*endpoint.RateLimit.Enabled, endpoint.RateLimit.RequestsPerMinute, endpoint.RateLimit.Burst)
		}
		if endpoint.ReplayProtection.Enabled {
			// In-memory replay cache is process-local. Multi-node deployments need shared storage.
			s.replayCaches[name] = replay.NewCache(endpoint.ReplayProtection.TTL, endpoint.ReplayProtection.MaxEntries)
		}
		// Build the per-endpoint signature verifier once at startup. An empty
		// scheme defaults to the timestamped HMAC-SHA256 colon scheme so configs
		// predating the scheme field keep working; an unknown scheme fails closed
		// (every request rejected).
		verifier, err := auth.NewVerifier(auth.Spec{
			AuthType:           endpoint.Auth.Type,
			Scheme:             defaultScheme(endpoint.Auth.Scheme),
			Secret:             endpoint.Auth.Secret,
			TimestampTolerance: endpoint.Auth.TimestampTolerance,
			RemoveBearer:       endpoint.Auth.RemoveBearer,
			ExpectedIssuer:     endpoint.Auth.ExpectedIssuer,
			ExpectedAudience:   endpoint.Auth.ExpectedAudience,
			BodyHashClaim:      endpoint.Auth.BodyHashClaim,
		})
		if err != nil {
			logger.Error("signature scheme unavailable; endpoint will reject all requests", "endpoint", name, "scheme", endpoint.Auth.Scheme, "error", err.Error())
			verifier = auth.RejectAll{}
		}
		if endpoint.Auth.Type == auth.AuthTypeNoAuth {
			logger.Warn("endpoint has authentication disabled; this is not recommended and accepts every request unverified", "endpoint", name)
		}
		s.verifiers[name] = verifier

		// Compile filter chains once. Config validation already rejects bad
		// chains before serving, but tests may build a Config directly without
		// Load; if compilation fails here, fail closed with a match-nothing
		// chain (mirrors the auth RejectAll fallback above).
		s.endpointFilters[name] = compileFilterChain(logger, name, "endpoint", endpoint.Filters)
		if len(endpoint.Destinations) > 0 {
			chains := make([]filter.Chain, len(endpoint.Destinations))
			for i, route := range endpoint.Destinations {
				chains[i] = compileFilterChain(logger, name, "route", route.Filters)
			}
			s.routeFilters[name] = chains
		}
		// Compile routing variables (path pattern, specs, target and url
		// templates) once. Same fail-closed posture as filters above. A
		// disabled endpoint is never matched, so it gets no variables and,
		// in particular, opens no lookup provider connections.
		if !endpoint.Enabled {
			continue
		}
		ev := compileEndpointVariables(context.Background(), logger, name, endpoint, cfg.Destinations, factory, appMetrics)
		s.endpointVars[name] = ev
		// A broken endpoint still registers under its real (pattern) path so
		// matching requests reach the fail-closed 500 rather than a 404.
		if !ev.path.IsLiteral() {
			s.patternPaths = append(s.patternPaths, patternPath{name: name, pattern: ev.path})
		} else {
			s.literalPaths[endpoint.Path] = name
		}
	}
	sort.Slice(s.patternPaths, func(i, j int) bool { return s.patternPaths[i].name < s.patternPaths[j].name })
	s.trustedProxies = parseCIDRs(cfg.Security.TrustedProxies)
	if cfg.Server.MaxInflightRequests > 0 {
		s.inflight = make(chan struct{}, cfg.Server.MaxInflightRequests)
	}
	s.metrics.RegisterConfig(cfg)
	s.routes()
	return s
}

// filterLevelBodyUnparsed is the hookway_filtered_total "level" label used
// in place of "endpoint"/"route" when the failing condition was body-sourced
// against a request body that never parsed as JSON (see bodyUnparsedFilter).
const filterLevelBodyUnparsed = "body_unparsed"

// bodyUnparsedFilter reports whether a filter drop should be attributed to an
// unparsed (non-JSON) request body rather than a legitimate filter match: true
// only when the failing condition reads from the body AND the body never
// parsed as JSON (env.Body == nil). A body condition that legitimately fails
// against present JSON, or a header condition evaluated against a non-JSON
// body, is not this case.
func bodyUnparsedFilter(conditionSource string, body any) bool {
	return conditionSource == filter.SourceBody && body == nil
}

// compileFilterChain compiles a config filter chain into a filter.Chain,
// converting config conditions to filter specs. It fails closed (match nothing)
// if the chain unexpectedly fails to compile, so a misconfigured chain never
// silently passes everything through.
func compileFilterChain(logger *slog.Logger, endpoint, level string, filters []config.FilterConditionConfig) filter.Chain {
	if len(filters) == 0 {
		return filter.Chain{}
	}
	specs := make([]filter.Spec, len(filters))
	for i, f := range filters {
		specs[i] = filter.Spec{Source: f.Source, Path: f.Path, Operator: f.Operator, Value: f.Value}
	}
	chain, issues := filter.Compile(specs)
	if len(issues) > 0 {
		logger.Error("filter chain failed to compile; endpoint will drop all matching requests",
			"endpoint", endpoint, "filter_level", level, "issues", len(issues))
		return filter.MatchNothing()
	}
	return chain
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
	s.mux.HandleFunc("/health", s.handleLive)
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
	h := promhttp.HandlerFor(s.metrics.Registry(), promhttp.HandlerOpts{})
	return apikey.Middleware(s.cfg.Security.APIKey, h)
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
	traceparent := traceparentFromRequest(r, traceID)
	w.Header().Set("X-Request-ID", requestID)
	ctx := context.WithValue(r.Context(), requestIDKey{}, requestID)
	ctx = context.WithValue(ctx, traceIDKey{}, traceID)
	ctx = context.WithValue(ctx, traceparentKey{}, traceparent)
	r = r.WithContext(ctx)
	s.metrics.Inflight(1)
	defer s.metrics.Inflight(-1)
	rw := &recoveringResponseWriter{ResponseWriter: w}
	defer s.recoverHandlerPanic(rw, requestID, traceID)
	s.mux.ServeHTTP(rw, r)
}

// recoveringResponseWriter tracks whether a response has started (WriteHeader
// or Write called) so a panic recovered after some output was already flushed
// does not attempt a second, invalid WriteHeader call.
type recoveringResponseWriter struct {
	http.ResponseWriter
	wroteHeader bool
}

func (w *recoveringResponseWriter) WriteHeader(status int) {
	w.wroteHeader = true
	w.ResponseWriter.WriteHeader(status)
}

func (w *recoveringResponseWriter) Write(b []byte) (int, error) {
	w.wroteHeader = true
	return w.ResponseWriter.Write(b)
}

// recoverHandlerPanic is deferred around s.mux.ServeHTTP. On a panic it logs a
// structured Error with the stack trace, counts hookway_panics_total{location="handler"},
// and — only when nothing has been written to the response yet — writes a
// clean 500. A panic that occurs mid-write (after headers or a partial body
// were already flushed) cannot be un-written, so in that case this only
// logs and counts; the client sees a truncated/aborted response, which is the
// correct (if less clean) outcome given net/http has already committed bytes.
func (s *Server) recoverHandlerPanic(rw *recoveringResponseWriter, requestID, traceID string) {
	rec := recover()
	if rec == nil {
		return
	}
	s.logger.Error("panic recovered in http handler",
		"location", "handler",
		"panic", fmt.Sprint(rec),
		"stack", string(debug.Stack()),
		"request_id", requestID, "trace_id", traceID,
	)
	s.metrics.Panic("handler")
	if !rw.wroteHeader {
		writeJSON(rw, http.StatusInternalServerError, map[string]string{"error": "internal error"})
	}
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

// handleReady serves the cached readiness result maintained by the background
// prober. It never triggers a live destination fan-out, so a fleet of pods
// cannot stampede the brokers via kubelet probes, and one flaky destination
// cannot exceed a probe deadline. The prober logs the underlying reason on each
// failed probe; the public response stays generic so it cannot leak broker
// URLs or credentials.
func (s *Server) handleReady(w http.ResponseWriter, r *http.Request) {
	if _, ready := s.readiness.status(time.Now()); !ready {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "not ready"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ready"})
}

// StartReadinessProbe launches the background readiness prober and returns a
// channel closed when it stops (on ctx cancellation). Call it once, after the
// publisher is connected and before serving traffic; cancel ctx on shutdown to
// stop the goroutine.
func (s *Server) StartReadinessProbe(ctx context.Context) <-chan struct{} {
	return s.readiness.run(ctx)
}

func (s *Server) handleWebhook(w http.ResponseWriter, r *http.Request) {
	start := time.Now()
	status := http.StatusInternalServerError
	endpointLabel, destinationLabel, targetLabel := "", "", ""
	defer func() {
		s.metrics.HTTP(endpointLabel, destinationLabel, targetLabel, status)
		if endpointLabel != "" {
			s.metrics.RequestDuration(endpointLabel, destinationLabel, targetLabel, status, time.Since(start))
		}
		hooklogging.Request(s.logger, hooklogging.RequestAttrs{
			Method: r.Method, Path: r.URL.Path, Endpoint: endpointLabel, DestinationQueueID: destinationLabel, Queue: targetLabel,
			Status: status, Duration: time.Since(start), RequestID: requestIDFromContext(r.Context()),
			TraceID: traceIDFromContext(r.Context()), Headers: r.Header,
			HeaderDenylist:  s.cfg.Security.RedactedHeaderDenylist,
			HeaderAllowlist: s.cfg.Security.ForwardedHeaderAllowlist,
		})
	}()
	endpointName, endpoint, pathVars, ok := s.matchEndpoint(r.URL.Path)
	if !ok {
		status = http.StatusNotFound
		writeJSON(w, status, map[string]string{"error": "not found"})
		return
	}
	endpointLabel = endpointName
	destinationLabel = endpoint.DestinationLabel()
	targetLabel = endpoint.TargetLabel()
	// captureEnabled gates webhook-replay capture for this request: no store
	// wired (s.replay nil), or this is a no_auth endpoint and operators opted
	// to skip capturing events with no caller identity to attribute them to
	// (config.Replay.SkipUnauthenticated). Computed once so every capture
	// call site below stays consistent.
	captureEnabled := s.replay != nil && !(s.cfg.Replay.SkipUnauthenticated && endpoint.Auth.Type == auth.AuthTypeNoAuth)
	routes := endpoint.Destinations
	if r.Method != http.MethodPost {
		status = http.StatusMethodNotAllowed
		w.Header().Set("Allow", http.MethodPost)
		writeJSON(w, status, map[string]string{"error": "method not allowed"})
		return
	}
	// Loop protection: reject a request whose hop count has reached the limit,
	// which happens when a webhook destination points back at Hookway. Rejecting
	// with a terminal 4xx stops a well-behaved sender (including an upstream
	// Hookway, whose webhook engine treats 4xx as non-retryable) from retrying.
	inboundHops := parseHopCount(r.Header.Get(config.LoopHopsHeader))
	if s.cfg.Security.LoopProtectionEnabled() && inboundHops >= s.cfg.Security.LoopMaxHops() {
		status = http.StatusBadRequest
		for _, route := range routes {
			s.metrics.LoopRejected(endpointName, route.Destination, route.Target)
		}
		s.logger.Warn("webhook rejected: delivery loop detected",
			"endpoint", endpointName, "hops", inboundHops, "max_hops", s.cfg.Security.LoopMaxHops(),
			"request_id", requestIDFromContext(r.Context()), "trace_id", traceIDFromContext(r.Context()))
		writeJSON(w, status, map[string]string{"error": "loop detected"})
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
			for _, route := range routes {
				s.metrics.Saturated(endpointName, route.Destination, route.Target)
			}
			writeJSON(w, status, map[string]string{"error": "server busy"})
			return
		}
	}
	sourceIP := s.sourceIP(r)
	if !s.ipAllowed(sourceIP, endpoint.IPAllowlist) {
		status = http.StatusForbidden
		for _, route := range routes {
			s.metrics.Forbidden(endpointName, route.Destination, route.Target)
		}
		writeJSON(w, status, map[string]string{"error": "forbidden"})
		return
	}
	if !s.limiterForEndpoint(endpointName).Allow(sourceIP, time.Now()) {
		status = http.StatusTooManyRequests
		for _, route := range routes {
			s.metrics.RateLimited(endpointName, route.Destination, route.Target)
		}
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
	for _, route := range routes {
		s.metrics.BodyBytes(endpointName, route.Destination, route.Target, len(rawBody))
	}
	if err := s.verifiers[endpointName].Verify(r.Header.Get(endpoint.Auth.SignatureHeader), rawBody, time.Now()); err != nil {
		status = http.StatusUnauthorized
		s.metrics.AuthFailure(endpointName, authFailureReason(endpoint.Auth.Type))
		writeJSON(w, status, map[string]string{"error": "unauthorized"})
		return
	}
	// Reserve the replay key against replays. Release it whenever a retry of
	// this exact request could still make progress: full publish failure (no
	// destination succeeded), and partial failure under skip_failed (some
	// destinations still need the event). Full success keeps the reservation.
	// A retry after partial failure re-delivers to destinations that already
	// succeeded; that duplicate is the documented consumer-side dedup
	// contract via idempotency_key, identical to stop_on_failure's retry
	// semantics today.
	replayKey := s.replayReservationKey(endpoint, r, rawBody)
	replayCache := s.replayCaches[endpointName]
	if replayCache != nil {
		if !replayCache.Accept(endpointName, replayKey, time.Now()) {
			status = http.StatusConflict
			for _, route := range routes {
				s.metrics.ReplayRejected(endpointName, route.Destination, route.Target)
			}
			writeJSON(w, status, map[string]string{"error": "replay detected"})
			return
		}
	}
	releaseReplay := func() {
		if replayCache != nil {
			replayCache.Release(endpointName, replayKey)
		}
	}
	env, err := webhook.BuildEnvelopeWithOptions(r, webhook.EnvelopeOptions{
		Endpoint: endpointName, DestinationQueueID: destinationLabel, Queue: targetLabel, RawBody: rawBody, ReceivedAt: time.Now(), SourceIP: sourceIP,
		RequestID: requestIDFromContext(r.Context()), TraceID: traceIDFromContext(r.Context()), Traceparent: traceparentFromContext(r.Context()),
		AuthType: endpoint.Auth.Type, AuthScheme: defaultScheme(endpoint.Auth.Scheme),
		IdempotencyKey:  s.idempotencyKey(r, endpoint, rawBody),
		HeaderDenylist:  s.cfg.Security.RedactedHeaderDenylist,
		HeaderAllowlist: s.cfg.Security.ForwardedHeaderAllowlist,
		Hops:            inboundHops,
	})
	if err != nil {
		releaseReplay()
		status = http.StatusInternalServerError
		writeJSON(w, status, map[string]string{"error": "internal error"})
		return
	}
	// Endpoint-level filter chain gates every destination. Filters run against
	// the unfiltered headers (env.OrigHeaders) so behaviour does not change when
	// logging redaction lists change, and against the parsed JSON body.
	if chain := s.endpointFilters[endpointName]; !chain.Empty() {
		if ok, idx := chain.Match(env.OrigHeaders, env.Body); !ok {
			source, path, operator := chain.Describe(idx)
			level := "endpoint"
			if bodyUnparsedFilter(source, env.Body) {
				level = filterLevelBodyUnparsed
			}
			for _, route := range routes {
				s.metrics.Filtered(endpointName, route.Destination, route.Target, level)
			}
			logArgs := []any{
				"endpoint", endpointName, "filter_level", level, "filter_index", idx,
				"filter_source", source, "filter_path", path, "filter_operator", operator,
				"event_id", env.ID, "request_id", env.RequestID, "trace_id", env.TraceID,
			}
			if level == filterLevelBodyUnparsed {
				s.logger.Warn("body filter evaluated against unparsed body", logArgs...)
			} else {
				s.logger.Info("webhook filtered at endpoint", logArgs...)
			}
			status = endpoint.FilterResponseStatus()
			if status >= 200 && status < 300 {
				// Silent drop: keep the replay reservation (the delivery was
				// consumed) and return the normal accepted shape so senders
				// cannot probe the filter configuration.
				writeJSON(w, status, map[string]any{
					"status": "accepted", "endpoint": endpointName, "destinations": []map[string]string{},
					"event_id": env.ID, "request_id": env.RequestID,
				})
			} else {
				// Configured rejection: release the reservation so a sender
				// retry is filtered again rather than rejected as a replay.
				releaseReplay()
				writeJSON(w, status, map[string]string{"error": "filtered"})
			}
			return
		}
	}
	// Route-level filter chains decide which destinations receive the payload.
	// Filtered routes are never treated as delivery failures; dropping them from
	// the slice keeps the delivery_failure_policy semantics untouched.
	activeIdx := make([]int, 0, len(routes))
	for i := range routes {
		activeIdx = append(activeIdx, i)
	}
	if chains := s.routeFilters[endpointName]; len(chains) > 0 {
		activeIdx = activeIdx[:0]
		for i, route := range routes {
			if ok, idx := chains[i].Match(env.OrigHeaders, env.Body); !ok {
				source, path, operator := chains[i].Describe(idx)
				level := "route"
				if bodyUnparsedFilter(source, env.Body) {
					level = filterLevelBodyUnparsed
				}
				s.metrics.Filtered(endpointName, route.Destination, route.Target, level)
				logArgs := []any{
					"endpoint", endpointName, "destination_queue_id", route.Destination, "queue", route.Target,
					"filter_level", level, "filter_index", idx,
					"filter_source", source, "filter_path", path, "filter_operator", operator,
					"event_id", env.ID, "request_id", env.RequestID, "trace_id", env.TraceID,
				}
				if level == filterLevelBodyUnparsed {
					s.logger.Warn("body filter evaluated against unparsed body", logArgs...)
				} else {
					s.logger.Info("webhook filtered at route", logArgs...)
				}
				continue
			}
			activeIdx = append(activeIdx, i)
		}
	}
	// Routing variables: resolve from the request and render the active
	// routes' targets. Runs after filters (a filtered request is never a
	// variable error) and before replay capture (the stored envelope and the
	// recorded deliveries must carry the resolved values and rendered
	// targets). An unresolved variable that an active route needs either
	// redirects the event to the endpoint's fallback route or is rejected as
	// a bad request; it is never a delivery failure.
	ev := s.endpointVars[endpointName]
	if ev.broken {
		releaseReplay()
		status = http.StatusInternalServerError
		s.logger.Error("endpoint variables failed to compile; rejecting request", "endpoint", endpointName, "event_id", env.ID)
		writeJSON(w, status, map[string]string{"error": "internal error"})
		return
	}
	var activeRoutes []resolvedRoute
	var unresolved []string
	if len(ev.specs) == 0 {
		activeRoutes = staticRoutes(endpoint, activeIdx)
	} else if len(activeIdx) > 0 {
		var values map[string]string
		var lookupErrs map[string]error
		activeRoutes, values, unresolved, lookupErrs = ev.resolveRoutes(r.Context(), endpoint, activeIdx, vars.Input{
			Headers: env.OrigHeaders, Body: env.Body, Query: r.URL.Query(), PathVars: pathVars,
		})
		if len(values) > 0 {
			env.Variables = values
		}
		for name, lerr := range lookupErrs {
			s.logger.Warn("variable lookup failed; treating as unresolved",
				"endpoint", endpointName, "variable", name, "error", lerr.Error(),
				"event_id", env.ID, "request_id", env.RequestID, "trace_id", env.TraceID)
		}
	}
	if len(unresolved) > 0 {
		fb, hasFallback := endpoint.FallbackRoute()
		action := "rejected"
		if hasFallback {
			action = "fallback"
		}
		for _, v := range unresolved {
			s.metrics.VariableUnresolved(endpointName, v, action)
		}
		s.logger.Info("webhook routing variables unresolved",
			"endpoint", endpointName, "variables", unresolved, "action", action,
			"event_id", env.ID, "request_id", env.RequestID, "trace_id", env.TraceID,
		)
		if !hasFallback {
			releaseReplay()
			status = http.StatusBadRequest
			writeJSON(w, status, map[string]string{"error": "unresolved_variable", "variable": unresolved[0]})
			return
		}
		activeRoutes = []resolvedRoute{{EndpointDestinationConfig: fb, template: fb.Target}}
	}
	// Capture INSERT: only once auth has passed and the endpoint/route filters
	// have decided at least one destination will actually receive this event
	// -- an event with zero active routes has nothing to deliver or replay, so
	// skip it. Runs BEFORE pubCtx is created (below) and on its own
	// replayCaptureTimeout budget, independent of server.publish_timeout: a
	// slow-but-successful replay-store write (lock contention, pool
	// exhaustion, network blip) must add only its own bounded latency to the
	// request here, never eat into the publish deadline that publishRoutes is
	// about to get. Strictly best-effort: captureEvent recovers its own
	// errors/panics and never changes this response.
	if captureEnabled && len(activeRoutes) > 0 {
		s.captureEvent(r.Context(), replaystore.Event{
			ID: env.ID, Endpoint: endpointName, IdempotencyKey: env.IdempotencyKey,
			RequestID: env.RequestID, TraceID: env.TraceID, ReceivedAt: env.ReceivedAt,
			Envelope: envelopeToMessage(env),
		})
	}
	// Detach the publish from the inbound request context: a sender
	// disconnect or timeout after the body is read and the signature verified
	// must not cancel in-flight publishes (the work is already committed to
	// being delivered). context.WithoutCancel preserves request-scoped values
	// (request id, trace id) while dropping the parent's cancellation signal;
	// server.publish_timeout independently bounds how long the fan-out may
	// run. Created here, after the capture INSERT above (not before it), so
	// publishRoutes always gets the full PublishTimeout budget regardless of
	// how long that capture write took.
	pubCtx, cancel := context.WithTimeout(context.WithoutCancel(r.Context()), s.cfg.Server.PublishTimeout)
	defer cancel()
	published, failed, err := s.publishRoutes(pubCtx, endpointName, endpoint, env, activeRoutes)
	if err != nil {
		releaseReplay()
		status = http.StatusServiceUnavailable
		s.logger.Warn("request publish failed; returning 503",
			"endpoint", endpointName,
			"event_id", env.ID,
			"request_id", requestIDFromContext(r.Context()),
			"trace_id", traceIDFromContext(r.Context()),
		)
		// Outcome UPDATE: stop_on_failure returns any destinations that
		// already succeeded in published and the failed route plus remaining
		// unattempted routes in failed, so replay has a destination list even
		// when the inbound request is 503. Uses r.Context() (its own fresh
		// replayCaptureTimeout budget), not pubCtx: pubCtx may already be
		// fully exhausted after a full-timeout publish.
		if captureEnabled && len(activeRoutes) > 0 {
			s.recordOutcome(r.Context(), env.ID, deliveriesFromResults(published, failed))
		}
		writeJSON(w, status, map[string]string{"error": "service unavailable"})
		return
	}
	status = http.StatusAccepted
	body := map[string]any{
		"status": "accepted", "endpoint": endpointName, "destinations": published,
		"event_id": env.ID, "request_id": env.RequestID,
	}
	if len(unresolved) > 0 {
		body["unresolved_variables"] = unresolved
	}
	if len(failed) > 0 {
		// Partial failure under skip_failed: release the reservation so an
		// identical sender retry is accepted again (not rejected 409) and can
		// redeliver to the destinations that failed here. That retry also
		// re-publishes to destinations that already succeeded; consumers
		// dedupe that duplicate via idempotency_key, identical to the retry
		// contract stop_on_failure already relies on today.
		releaseReplay()
		body["failed_destinations"] = failed
	}
	// Uses r.Context() (its own fresh replayCaptureTimeout budget), not
	// pubCtx: pubCtx may already be near-exhausted after publishRoutes ran,
	// and a slow store write here must not race that near-empty deadline.
	if captureEnabled && len(activeRoutes) > 0 {
		s.recordOutcome(r.Context(), env.ID, deliveriesFromResults(published, failed))
	}
	writeJSON(w, status, body)
}

type publishRouteResult struct {
	index         int
	destinationID string
	queue         string
	duration      time.Duration
	err           error
}

func (s *Server) publishRoutes(ctx context.Context, endpointName string, endpoint config.EndpointConfig, env webhook.Envelope, routes []resolvedRoute) ([]map[string]string, []map[string]string, error) {
	switch endpoint.EffectiveDeliveryFailurePolicy() {
	case config.DeliveryFailurePolicySkipFailed:
		return s.publishRoutesSkipFailed(ctx, endpointName, env, routes)
	default:
		return s.publishRoutesStopOnFailure(ctx, endpointName, env, routes)
	}
}

func (s *Server) publishRoutesStopOnFailure(ctx context.Context, endpointName string, env webhook.Envelope, routes []resolvedRoute) ([]map[string]string, []map[string]string, error) {
	published := make([]map[string]string, 0, len(routes))
	for i, route := range routes {
		result := s.publishRoute(ctx, endpointName, env, route, 0)
		if result.err != nil {
			// Record the failed route and every not-yet-attempted route as
			// failed so the replay store has a destination list to re-deliver
			// against. Unattempted routes never received the event, so replay
			// must include them.
			failed := make([]map[string]string, 0, len(routes)-i)
			failed = append(failed, map[string]string{"destination_queue_id": result.destinationID, "queue": result.queue})
			for _, remaining := range routes[i+1:] {
				failed = append(failed, map[string]string{"destination_queue_id": remaining.Destination, "queue": remaining.Target})
			}
			return published, failed, result.err
		}
		published = append(published, map[string]string{"destination_queue_id": result.destinationID, "queue": result.queue})
	}
	return published, nil, nil
}

func (s *Server) publishRoutesSkipFailed(ctx context.Context, endpointName string, env webhook.Envelope, routes []resolvedRoute) ([]map[string]string, []map[string]string, error) {
	workerCount := s.cfg.Server.FanoutConcurrencyLimit()
	if workerCount > len(routes) {
		workerCount = len(routes)
	}
	jobs := make(chan int)
	results := make(chan publishRouteResult, len(routes))
	var wg sync.WaitGroup
	for range workerCount {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for index := range jobs {
				results <- s.publishRouteRecovered(ctx, endpointName, env, routes[index], index)
			}
		}()
	}
	for index := range routes {
		jobs <- index
	}
	close(jobs)
	wg.Wait()
	close(results)

	publishedByIndex := make([]map[string]string, len(routes))
	failedByIndex := make([]map[string]string, len(routes))
	var publishErr error
	for result := range results {
		if result.err != nil {
			if publishErr == nil || errors.Is(publishErr, context.Canceled) {
				publishErr = result.err
			}
			failedByIndex[result.index] = map[string]string{"destination_queue_id": result.destinationID, "queue": result.queue}
			continue
		}
		publishedByIndex[result.index] = map[string]string{"destination_queue_id": result.destinationID, "queue": result.queue}
	}
	published := compactRouteResults(publishedByIndex)
	failed := compactRouteResults(failedByIndex)
	if len(published) == 0 && publishErr != nil {
		return nil, failed, publishErr
	}
	return published, failed, nil
}

func compactRouteResults(results []map[string]string) []map[string]string {
	compacted := make([]map[string]string, 0, len(results))
	for _, result := range results {
		if result != nil {
			compacted = append(compacted, result)
		}
	}
	return compacted
}

// publishRouteRecovered wraps publishRoute with a per-job panic guard so a
// publisher that panics for one route (fan-out under skip_failed) is recorded
// as a failed route rather than crashing the process or leaving the
// WaitGroup/results channel wedged — the deferred recover still runs, wg.Done
// still fires, and a result is still sent.
func (s *Server) publishRouteRecovered(ctx context.Context, endpointName string, env webhook.Envelope, route resolvedRoute, index int) (result publishRouteResult) {
	defer func() {
		if rec := recover(); rec != nil {
			s.logger.Error("panic recovered in fan-out publish worker",
				"location", "fanout",
				"panic", fmt.Sprint(rec),
				"stack", string(debug.Stack()),
				"endpoint", endpointName, "destination", route.Destination, "queue", route.Target,
				"request_id", env.RequestID, "trace_id", env.TraceID,
			)
			s.metrics.Panic("fanout")
			result = publishRouteResult{
				index: index, destinationID: route.Destination, queue: route.Target,
				err: fmt.Errorf("panic: %v", rec),
			}
		}
	}()
	return s.publishRoute(ctx, endpointName, env, route, index)
}

// publishRoute delivers to one rendered route. The publisher, msg.Queue, and
// the result (which feeds the HTTP response and the replay store) all get the
// rendered target; metrics are labelled with the authored template so their
// cardinality is bounded by the config.
func (s *Server) publishRoute(ctx context.Context, endpointName string, env webhook.Envelope, route resolvedRoute, index int) publishRouteResult {
	msg := envelopeToMessage(env)
	msg.DestinationQueueID = route.Destination
	msg.Queue = route.Target
	start := time.Now()
	err := s.publisher.Publish(ctx, route.Destination, route.Target, msg)
	duration := time.Since(start)
	status := "success"
	if err != nil {
		status = "failure"
		s.logger.Error("publish route failed",
			"endpoint", endpointName,
			"destination", route.Destination,
			"target", route.Target,
			"target_template", route.template,
			"event_id", msg.ID,
			"request_id", env.RequestID,
			"trace_id", env.TraceID,
			"error", err.Error(),
		)
	} else {
		s.metrics.MessagePublished(endpointName, route.Destination, route.template)
	}
	s.metrics.Publish(endpointName, route.Destination, route.template, status, duration)
	return publishRouteResult{
		index: index, destinationID: route.Destination, queue: route.Target,
		duration: duration, err: err,
	}
}

// parseHopCount reads the loop-protection hop header. A missing, negative, or
// malformed value counts as 0 (a fresh delivery) so a garbled header can never
// bypass the limit — it only ever resets the count, and Hookway's own stamping
// always writes a valid integer.
func parseHopCount(v string) int {
	if v == "" {
		return 0
	}
	n, err := strconv.Atoi(v)
	if err != nil || n < 0 {
		return 0
	}
	return n
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
			if ip := s.rightmostUntrustedForwardedIP(forwarded); ip != "" {
				return ip
			}
		}
		if realIP := strings.TrimSpace(r.Header.Get("X-Real-IP")); realIP != "" {
			return realIP
		}
	}
	return peer
}

// rightmostUntrustedForwardedIP resolves the real client address from an
// X-Forwarded-For header value. Proxies append to the right as a request
// passes through them, so the header reads
// "client, proxy_a, proxy_b, ..., proxy_n" with the entry closest to Hookway
// on the right. The leftmost entry is client-controlled and cannot be
// trusted on its own: a client can send "X-Forwarded-For: <allowlisted-ip>"
// to spoof the IP allowlist or rotate fake addresses to dodge per-IP rate
// limiting.
//
// To find the real client, walk the list right to left, skipping any entry
// that is itself a trusted proxy, and return the first (i.e. rightmost)
// entry that is not. If every entry is a trusted proxy, there is no
// untrusted hop to report, so the leftmost entry is returned.
//
// A malformed/unparseable entry stops the walk immediately: the caller gets
// the last known-good (more rightward, closer-to-trusted) hop rather than
// continuing further left toward the client-controlled end, which fails
// toward the trusted side instead of toward attacker-controlled input.
// Returns "" only when no entry could be parsed at all (e.g. the header's
// first parsed entry is already malformed), letting the caller fall back to
// X-Real-IP or the peer address.
func (s *Server) rightmostUntrustedForwardedIP(forwarded string) string {
	parts := strings.Split(forwarded, ",")
	lastGood := ""
	for i := len(parts) - 1; i >= 0; i-- {
		entry := strings.TrimSpace(parts[i])
		if net.ParseIP(entry) == nil {
			break
		}
		lastGood = entry
		if !s.isTrustedProxy(entry) {
			return entry
		}
	}
	return lastGood
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
	return s.isTrustedProxy(peer)
}

// isTrustedProxy reports whether ip falls inside one of the configured
// trusted_proxies CIDR ranges. It backs both trustsPeer (should we honour
// forwarding headers from this immediate peer at all) and
// rightmostUntrustedForwardedIP (should this X-Forwarded-For entry be
// skipped as a known proxy hop rather than treated as the real client).
func (s *Server) isTrustedProxy(ip string) bool {
	parsed := net.ParseIP(ip)
	if parsed == nil {
		return false
	}
	for _, network := range s.trustedProxies {
		if network.Contains(parsed) {
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
// a hash of the verified signature or raw body for static-header auth.
func (s *Server) idempotencyKey(r *http.Request, endpoint config.EndpointConfig, rawBody []byte) string {
	if header := endpoint.Auth.IdempotencyHeader; header != "" {
		if v := strings.TrimSpace(r.Header.Get(header)); v != "" {
			return v
		}
	}
	if endpoint.Auth.Type == auth.AuthTypeHeaderExact {
		sum := sha256.Sum256(rawBody)
		return hex.EncodeToString(sum[:])
	}
	sig := r.Header.Get(endpoint.Auth.SignatureHeader)
	if sig == "" {
		return ""
	}
	sum := sha256.Sum256([]byte(endpoint.Auth.SignatureHeader + ":" + sig))
	return hex.EncodeToString(sum[:])
}

func (s *Server) replayReservationKey(endpoint config.EndpointConfig, r *http.Request, rawBody []byte) string {
	if endpoint.Auth.Type == auth.AuthTypeHeaderExact {
		if header := endpoint.Auth.IdempotencyHeader; header != "" {
			if v := strings.TrimSpace(r.Header.Get(header)); v != "" {
				return v
			}
		}
		sum := sha256.Sum256(rawBody)
		return hex.EncodeToString(sum[:])
	}
	return r.Header.Get(endpoint.Auth.SignatureHeader)
}

func authFailureReason(authType string) string {
	if authType == auth.AuthTypeHeaderExact {
		return "invalid_auth"
	}
	return "invalid_signature"
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
		// The trace-id segment must be well-formed hex, not merely the right
		// length: this value is reused both for logging (trace_id) and, when
		// no valid inbound traceparent is available to propagate verbatim, as
		// the trace-id embedded in a synthesized outbound "00-<id>-...-01"
		// header (see traceparentFromRequest). A length-only check lets a
		// crafted non-hex segment (e.g. 32 "g" characters) flow into that
		// synthesized header and violate W3C well-formedness.
		if len(parts) >= 2 && len(parts[1]) == 32 && isHex(parts[1]) {
			return parts[1]
		}
	}
	if id := strings.TrimSpace(r.Header.Get("X-Trace-ID")); id != "" {
		return id
	}
	return strings.ReplaceAll(uuid.NewString(), "-", "")
}

// traceparentFromRequest returns the outbound W3C traceparent header value
// for this request's deliveries: the inbound Traceparent header verbatim when
// it is syntactically valid, otherwise a synthesized
// "00-<traceID>-<16-hex-random-span>-01" using the same trace id already
// derived for this request (traceIDFromRequest). Synthesizing (rather than
// leaving it empty) keeps distributed traces intact across the gateway even
// when the sender didn't participate in W3C trace context.
func traceparentFromRequest(r *http.Request, traceID string) string {
	if tp := r.Header.Get("Traceparent"); isValidTraceparent(tp) {
		return tp
	}
	return "00-" + traceID + "-" + randomSpanID() + "-01"
}

// isValidTraceparent checks the structural shape of a W3C traceparent header:
// four hyphen-separated hex fields of length 2 (version), 32 (trace-id), 16
// (span-id), 2 (flags). It does not validate the version-specific rules
// beyond that (e.g. version "ff" or an all-zero trace/span id), which is
// sufficient to distinguish a well-formed propagated header from an absent or
// garbled one.
func isValidTraceparent(tp string) bool {
	if tp == "" {
		return false
	}
	parts := strings.Split(tp, "-")
	if len(parts) != 4 {
		return false
	}
	fieldLens := [4]int{2, 32, 16, 2}
	for i, part := range parts {
		if len(part) != fieldLens[i] || !isHex(part) {
			return false
		}
	}
	return true
}

func isHex(s string) bool {
	for _, r := range s {
		if !((r >= '0' && r <= '9') || (r >= 'a' && r <= 'f') || (r >= 'A' && r <= 'F')) {
			return false
		}
	}
	return true
}

// randomSpanID returns a 16-hex-character (8-byte) span id, reusing the same
// uuid-derived randomness source traceIDFromRequest already relies on rather
// than adding a crypto/rand dependency for one call site.
func randomSpanID() string {
	return strings.ReplaceAll(uuid.NewString(), "-", "")[:16]
}

type requestIDKey struct{}
type traceIDKey struct{}
type traceparentKey struct{}

func requestIDFromContext(ctx context.Context) string {
	id, _ := ctx.Value(requestIDKey{}).(string)
	return id
}

func traceIDFromContext(ctx context.Context) string {
	id, _ := ctx.Value(traceIDKey{}).(string)
	return id
}

func traceparentFromContext(ctx context.Context) string {
	tp, _ := ctx.Value(traceparentKey{}).(string)
	return tp
}

func defaultScheme(scheme string) string {
	if scheme == "" {
		return auth.SchemeHMACSHA256TSColon
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

// patternPath is an enabled endpoint whose path contains {segment}
// placeholders, matched after the literal-path map misses.
type patternPath struct {
	name    string
	pattern vars.PathPattern
}

// matchEndpoint resolves the request path to an enabled endpoint: exact
// literal paths first (O(1), the pre-variables behaviour), then pattern paths.
// Config validation guarantees at most one pattern can match.
func (s *Server) matchEndpoint(path string) (string, config.EndpointConfig, map[string]string, bool) {
	if name, ok := s.literalPaths[path]; ok {
		return name, s.cfg.Endpoints[name], nil, true
	}
	for _, pp := range s.patternPaths {
		if captured, ok := pp.pattern.Match(path); ok {
			return pp.name, s.cfg.Endpoints[pp.name], captured, true
		}
	}
	return "", config.EndpointConfig{}, nil, false
}

// Close releases resources owned by the server that are not tied to a
// listener: the lookup-source variable providers (DynamoDB clients, HTTP
// connection pools, Postgres pools). Safe to call once after Shutdown.
func (s *Server) Close() error {
	var errs []error
	for _, ev := range s.endpointVars {
		for _, p := range ev.providers {
			if err := p.Close(); err != nil {
				errs = append(errs, err)
			}
		}
	}
	return errors.Join(errs...)
}
