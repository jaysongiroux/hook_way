package httpapi

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"maps"
	"net"
	"net/http"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/google/uuid"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"

	"github.com/jaysongiroux/hook_way/internal/auth"
	"github.com/jaysongiroux/hook_way/internal/capture"
	"github.com/jaysongiroux/hook_way/internal/config"
	"github.com/jaysongiroux/hook_way/internal/flowconfig"
	"github.com/jaysongiroux/hook_way/internal/graph"
	hooklogging "github.com/jaysongiroux/hook_way/internal/logging"
	"github.com/jaysongiroux/hook_way/internal/metrics"
	"github.com/jaysongiroux/hook_way/internal/outboundcred"
	"github.com/jaysongiroux/hook_way/internal/publishmsg"
	"github.com/jaysongiroux/hook_way/internal/queue"
	"github.com/jaysongiroux/hook_way/internal/routing"
	"github.com/jaysongiroux/hook_way/internal/routingstore"
	"github.com/jaysongiroux/hook_way/internal/secretref"
	"github.com/jaysongiroux/hook_way/internal/vars"
)

// FlowServer serves configured flows through the graph executor.
type FlowServer struct {
	cfg            flowconfig.Config
	process        config.Config
	publisher      queue.Publisher
	logger         *slog.Logger
	metrics        *metrics.Metrics
	trustedProxies []*net.IPNet
	gate           *requestGate
	admission      *metrics.Admission
	literalPaths   map[string]string
	patternPaths   []flowPattern
	flows          map[string]mountedFlow
	mux            *http.ServeMux
	readiness      *readinessProber
	protection     *protectionRuntime
	// closers holds provider pools, lookup transports, and secret resolvers.
	// Close stops their background work after the server has drained.
	closers   []io.Closer
	lifecycle atomic.Uint32 // 0 running, 1 withdrawing, 2 draining, 3 stopped
	closeOnce sync.Once
	closeErr  error
	// capture, when set, records acknowledged failures for the replay service.
	capture *capture.Capturer
}

// SetCapturer turns on failure capture. Call it before serving.
func (s *FlowServer) SetCapturer(c *capture.Capturer) { s.capture = c }

type captureRequestKey struct{}

func captureRequestFrom(ctx context.Context) *capture.Request {
	r, _ := ctx.Value(captureRequestKey{}).(*capture.Request)
	return r
}

var nodeTracer = otel.Tracer("hookway")

type flowPattern struct {
	name    string
	pattern vars.PathPattern
}

type mountedFlow struct {
	overload   *overloadLog
	responses  map[string]graph.ResponsePolicy
	nodeTypes  map[string]string
	name       string
	methods    map[string]struct{}
	allow      string
	flow       *graph.Flow
	destByNode map[string]string
	destIDs    []string
	// broken is set when graph.Compile failed. The path is still registered
	// so matching requests return 500 rather than 404 (I13).
	broken bool
}

type queueResolver struct{ pub queue.Publisher }

func (r queueResolver) Resolve(string) (flowconfig.Publisher, error) {
	return queuePublisherAdapter(r), nil
}

type queuePublisherAdapter struct{ pub queue.Publisher }

func (a queuePublisherAdapter) Publish(ctx context.Context, destinationID, renderedTarget string, env graph.Envelope) error {
	if creq := captureRequestFrom(ctx); creq != nil {
		ctx = creq.WithOrigin(ctx, flowconfig.PublishingNode(ctx))
	}
	msg, err := publishmsg.FromEnvelope(env, destinationID, renderedTarget)
	if err != nil {
		// Conversion failures are deterministic payload problems; retrying the
		// destination cannot fix malformed object metadata or base64 content.
		return graph.NodeError{Status: http.StatusBadRequest, Err: fmt.Errorf("prepare publish payload: %w", err)}
	}
	return a.pub.Publish(ctx, destinationID, renderedTarget, msg)
}

func NewFlowServer(cfg flowconfig.Config, pub queue.Publisher, logger *slog.Logger, m *metrics.Metrics) (*FlowServer, error) {
	if issues := flowconfig.StandaloneIssues(cfg); len(issues) > 0 {
		return nil, fmt.Errorf("unsupported flow: %s", issues[0].Message)
	}
	return newFlowServer(cfg, pub, logger, m, flowconfig.RegistryOptions{})
}

func newFlowServer(cfg flowconfig.Config, pub queue.Publisher, logger *slog.Logger, m *metrics.Metrics, extra flowconfig.RegistryOptions) (*FlowServer, error) {
	if logger == nil {
		logger = slog.Default()
	}
	if m == nil {
		m = metrics.New()
	}
	if issues := flowconfig.RoutingIssues(cfg); len(issues) > 0 {
		return nil, fmt.Errorf("invalid flow routing: %s", issues[0].Message)
	}
	if issues := flowconfig.RoutingConfigurationIssues(cfg); len(issues) > 0 {
		return nil, fmt.Errorf("invalid routing configuration: %s", issues[0].Message)
	}
	if issues := flowconfig.CredentialConfigurationIssues(cfg); len(issues) > 0 {
		return nil, fmt.Errorf("invalid credentials: %s", issues[0].Message)
	}
	headers, queries := flowconfig.AuthenticationRedactions(cfg)
	cfg.Security.RedactedHeaderDenylist = append(append([]string(nil), cfg.Security.RedactedHeaderDenylist...), headers...)
	extra.QueryDenylist = queries
	processCfg := cfg.ServingConfig()
	extra.Publishers = queueResolver{pub: pub}
	extra.Logger = logger
	extra.HeaderDenylist = cfg.Security.RedactedHeaderDenylist
	extra.HeaderAllowlist = cfg.Security.ForwardedHeaderAllowlist
	// secret_ref resolvers each start a refresh goroutine, so they must be
	// retained and closed. Collected here because extra.Secrets is invoked
	// during NewRegistryWithOptions, before the FlowServer exists.
	var closers []io.Closer
	closeAll := func() {
		for _, c := range closers {
			_ = c.Close()
		}
	}
	extra.Credentials = maps.Clone(extra.Credentials)
	if extra.Credentials == nil {
		extra.Credentials = map[string]flowconfig.CredentialProvider{}
	}
	credentialNames := make([]string, 0, len(cfg.Credentials))
	for name := range cfg.Credentials {
		credentialNames = append(credentialNames, name)
	}
	sort.Strings(credentialNames)
	for _, name := range credentialNames {
		if extra.Credentials[name] != nil {
			continue
		}
		provider, err := outboundcred.New(name, cfg.Credentials[name], logger, m.Credential)
		if err != nil {
			closeAll()
			return nil, fmt.Errorf("credential profile %q: %w", name, err)
		}
		extra.Credentials[name] = provider
		closers = append(closers, provider)
	}
	if issues := flowconfig.LookupConfigurationIssues(cfg); len(issues) > 0 {
		closeAll()
		return nil, fmt.Errorf("invalid lookup configuration: %s", issues[0].Message)
	}
	if extra.Lookups == nil {
		bind, resources, err := bindLookups(cfg.Lookups, logger, m)
		closers = append(closers, resources...)
		if err != nil {
			closeAll()
			return nil, err
		}
		extra.Lookups = bind
	}
	var protectionState *protectionRuntime
	if extra.Protection == nil {
		var resources []io.Closer
		var err error
		protectionState, resources, err = bindProtection(cfg, m)
		closers = append(closers, resources...)
		if err != nil {
			closeAll()
			return nil, err
		}
		extra.Protection = protectionState.resolve
		// Stop background probes/cleanup before their stores close.
		closers = append([]io.Closer{protectionState}, closers...)
	}
	extra.StateStores = cfg.StateStores
	var protectionErr error
	extra.ProtectionPolicies, protectionErr = flowconfig.ProtectionPolicies(cfg)
	if protectionErr != nil {
		closeAll()
		return nil, protectionErr
	}
	if extra.RoutingRegistry == nil {
		extra.RoutingRegistry, _ = routing.NewRegistry(cfg.Routing)
	}
	extra.RoutingStores = maps.Clone(extra.RoutingStores)
	if extra.RoutingStores == nil {
		extra.RoutingStores = map[string]routing.Reader{}
	}
	for _, name := range sortedRoutingStores(cfg.Routing.Stores) {
		if extra.RoutingStores[name] != nil {
			continue
		}
		store, err := routingstore.NewWithAdmissionBudget(context.Background(), cfg.Routing.Stores[name], cfg.Server.MaxInflightRequests)
		if err != nil {
			closeAll()
			return nil, fmt.Errorf("routing store %q: %w", name, err)
		}
		closers = append(closers, store)
		extra.RoutingStores[name] = store
	}
	if extra.BatchObservation == nil {
		extra.BatchObservation = func(node, result string) { m.Batch(node, result) }
	}
	if extra.RoutingObservation == nil {
		extra.RoutingObservation = func(node, result string) { m.Routing(node, result) }
	}
	if extra.Secrets == nil && extra.AuthSecrets == nil {
		extra.AuthSecrets = func(o flowconfig.AuthOptions) (flowconfig.SecretsFunc, error) {
			validate := func(value []byte) error {
				return flowconfig.ValidateAuthKey(o, value)
			}
			fn, closer, err := bindSecretRef(*o.SecretRef, logger, m, validate)
			if err != nil {
				return nil, err
			}
			closers = append(closers, closer)
			return fn, nil
		}
	}
	s := &FlowServer{
		cfg:          cfg,
		process:      processCfg,
		publisher:    pub,
		logger:       logger,
		metrics:      m,
		literalPaths: map[string]string{},
		flows:        map[string]mountedFlow{},
		mux:          http.NewServeMux(),
		protection:   protectionState,
	}
	names := make([]string, 0, len(cfg.Flows))
	for name := range cfg.Flows {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		fc := cfg.Flows[name]
		path, methods, destByNode, err := triggerRouting(fc)
		if err != nil {
			closeAll()
			return nil, fmt.Errorf("flow %q: %w", name, err)
		}
		flowOptions := extra
		flowOptions.FlowID = name
		reg, err := flowconfig.NewRegistryWithOptions(flowOptions)
		if err != nil {
			closeAll()
			return nil, err
		}
		closerStart := len(closers)
		compiled, compileErr := graph.Compile(name, fc, reg)
		if compileErr != nil {
			if errors.Is(compileErr, flowconfig.ErrAuthConfiguration) || errors.Is(compileErr, flowconfig.ErrRoutingConfiguration) || errors.Is(compileErr, flowconfig.ErrCredentialConfiguration) || errors.Is(compileErr, flowconfig.ErrProtectionConfiguration) || errors.Is(compileErr, graph.ErrAdmissionRecovery) {
				closeAll()
				return nil, fmt.Errorf("flow %q: %w", name, compileErr)
			}
			for _, closer := range closers[closerStart:] {
				_ = closer.Close()
			}
			closers = closers[:closerStart]
			logger.Error("flow failed to compile; path will return 500 not 404",
				"flow", name, "error", compileErr)
		}
		if flowHasNoAuth(fc) {
			logger.Warn("flow uses no_auth; requests are accepted without verification", "flow", name)
		}
		nodeTypes := map[string]string{}
		for _, node := range flowconfig.ExpandedNodes(fc) {
			nodeTypes[node.ID] = node.Type
		}
		s.flows[name] = mountedFlow{
			overload:  &overloadLog{},
			responses: fc.Responses, nodeTypes: nodeTypes,
			name: name, methods: methodSet(methods), allow: strings.Join(methods, ", "),
			flow: compiled, destByNode: destByNode, destIDs: uniqueDestIDs(destByNode),
			broken: compileErr != nil || compiled == nil,
		}
		pattern, err := vars.ParsePath(path)
		if err != nil {
			closeAll()
			return nil, fmt.Errorf("flow %q path: %w", name, err)
		}
		if pattern.IsLiteral() {
			s.literalPaths[path] = name
		} else {
			s.patternPaths = append(s.patternPaths, flowPattern{name: name, pattern: pattern})
		}
	}
	// Assigned after the compile loop, not in the literal above: node
	// constructors (and therefore extra.Secrets) run inside graph.Compile, so
	// closers is still empty when the struct is built. Copying the slice
	// header early would drop every resolver.
	s.closers = closers
	s.trustedProxies = parseCIDRs(cfg.Security.TrustedProxies)
	s.gate = newRequestGate(cfg.Server.MaxInflightRequests, cfg.Server.MaxPendingRequests, cfg.Server.AdmissionWaitTimeout)
	s.metrics.RegisterConfig(processCfg)
	s.readiness = newReadinessProber(pub, m, logger, cfg.Server.ReadinessInterval, cfg.Server.ReadinessTimeout)
	s.routes()
	s.admission = m.NewAdmission(cfg.Server.MaxInflightRequests)
	return s, nil
}

func triggerRouting(fc graph.FlowConfig) (path string, methods []string, destByNode map[string]string, err error) {
	destByNode = map[string]string{}
	for _, nc := range flowconfig.ExpandedNodes(fc) {
		switch nc.Type {
		case "http_trigger":
			opts, decErr := flowconfig.TriggerOptions(nc)
			if decErr != nil {
				return "", nil, nil, decErr
			}
			path = opts.Path
			methods = append([]string(nil), opts.Methods...)
		case "publish":
			opts, decErr := flowconfig.PublishNodeOptions(nc)
			if decErr != nil {
				continue
			}
			destByNode[nc.ID] = opts.Destination
		}
	}
	if path == "" {
		return "", nil, nil, errors.New("missing http_trigger path")
	}
	if len(methods) == 0 {
		methods = []string{http.MethodPost}
	}
	return path, methods, destByNode, nil
}

func uniqueDestIDs(destByNode map[string]string) []string {
	seen := map[string]struct{}{}
	ids := make([]string, 0, len(destByNode))
	for _, id := range destByNode {
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}

func flowHasNoAuth(fc graph.FlowConfig) bool {
	for _, nc := range flowconfig.ExpandedNodes(fc) {
		if nc.Type == "no_auth" {
			return true
		}
	}
	return false
}

func methodSet(methods []string) map[string]struct{} {
	out := map[string]struct{}{}
	for _, m := range methods {
		out[strings.ToUpper(m)] = struct{}{}
	}
	return out
}

func (s *FlowServer) helpers() *Server {
	return &Server{
		cfg: s.process, logger: s.logger, metrics: s.metrics,
		trustedProxies: s.trustedProxies, readiness: s.readiness,
	}
}

func (s *FlowServer) routes() {
	s.mux.HandleFunc("/health", s.helpers().handleLive)
	s.mux.HandleFunc("/healthz", s.helpers().handleLive)
	s.mux.HandleFunc("/ready", s.handleReady)
	s.mux.HandleFunc("/readyz", s.handleReady)
	if s.cfg.Metrics.Enabled && s.cfg.Metrics.Addr == "" {
		s.mux.Handle(s.cfg.Metrics.Path, s.helpers().metricsHandler())
	}
	s.mux.HandleFunc("/", s.handleFlow)
}

func (s *FlowServer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Cache-Control", "no-store")
	requestID := s.helpers().requestID(r)
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
	defer s.helpers().recoverHandlerPanic(rw, requestID, traceID)
	s.mux.ServeHTTP(rw, r)
}

func (s *FlowServer) HTTPServer() *http.Server {
	srv := &http.Server{
		Addr:              s.cfg.Server.Addr,
		Handler:           s,
		ReadHeaderTimeout: readHeaderTimeout,
		ReadTimeout:       s.cfg.Server.ReadTimeout,
		WriteTimeout:      s.cfg.Server.WriteTimeout,
		IdleTimeout:       s.cfg.Server.IdleTimeout,
	}
	srv.RegisterOnShutdown(s.BeginDrain)
	return srv
}

func (s *FlowServer) MetricsServer() *http.Server {
	return s.helpers().MetricsServer()
}

func (s *FlowServer) StartReadinessProbe(ctx context.Context) <-chan struct{} {
	destDone := s.readiness.run(ctx)
	if s.protection == nil {
		return destDone
	}
	storeDone := s.protection.run(ctx, s.cfg.Server.ReadinessInterval)
	done := make(chan struct{})
	go func() { <-destDone; <-storeDone; close(done) }()
	return done
}

// Withdraw marks readiness unavailable while allowing load balancers time to
// remove this instance. Liveness and existing/new flow requests remain available.
func (s *FlowServer) Withdraw() { s.lifecycle.CompareAndSwap(0, 1) }

// BeginDrain rejects new and queued flow requests without interrupting admitted work.
func (s *FlowServer) BeginDrain() {
	s.Withdraw()
	s.lifecycle.CompareAndSwap(1, 2)
	s.gate.close()
}

// Close releases resources holding background goroutines. Safe to call more
// than once, including concurrent calls.
func (s *FlowServer) Close() error {
	s.closeOnce.Do(func() {
		s.BeginDrain()
		s.lifecycle.Store(3)
		s.admission.Close()
		var errs []error
		for _, c := range s.closers {
			errs = append(errs, c.Close())
		}
		s.closeErr = errors.Join(errs...)
	})
	return s.closeErr
}

func (s *FlowServer) matchFlow(path string) (mountedFlow, map[string]string, bool) {
	if name, ok := s.literalPaths[path]; ok {
		return s.flows[name], nil, true
	}
	for _, pp := range s.patternPaths {
		if captured, ok := pp.pattern.Match(path); ok {
			return s.flows[pp.name], captured, true
		}
	}
	return mountedFlow{}, nil, false
}

func (s *FlowServer) handleFlow(w http.ResponseWriter, r *http.Request) {
	start := time.Now()
	status := http.StatusInternalServerError
	flowLabel := ""
	executionOutcome := "error"
	var overload *overloadLog
	defer func() {
		s.metrics.HTTP(flowLabel, "", "", status)
		if flowLabel != "" {
			s.metrics.FlowOutcome(flowLabel, executionOutcome)
			if overload == nil {
				s.logger.Info("flow completed", "flow", flowLabel, "outcome", executionOutcome, "status", status, "request_id", requestIDFromContext(r.Context()), "trace_id", traceIDFromContext(r.Context()))
			}
			s.metrics.RequestDuration(flowLabel, "", "", status, time.Since(start))
		}
		if overload != nil {
			overload.record(s.logger, flowLabel)
			return
		}
		hooklogging.Request(s.logger, hooklogging.RequestAttrs{
			Method: r.Method, Path: r.URL.Path, Endpoint: flowLabel,
			Status: status, Duration: time.Since(start), RequestID: requestIDFromContext(r.Context()),
			TraceID: traceIDFromContext(r.Context()), Headers: r.Header,
			HeaderDenylist:  s.cfg.Security.RedactedHeaderDenylist,
			HeaderAllowlist: s.cfg.Logging.HeaderAllowlist,
		})
	}()

	mf, pathVars, ok := s.matchFlow(r.URL.Path)
	if !ok {
		status = http.StatusNotFound
		s.writeJSON(w, r, status, map[string]string{"error": "not found"})
		return
	}
	flowLabel = mf.name
	if _, allowAny := mf.methods["ANY"]; !allowAny {
		if _, allowed := mf.methods[r.Method]; !allowed {
			status = http.StatusMethodNotAllowed
			w.Header().Set("Allow", mf.allow)
			s.writeJSON(w, r, status, map[string]string{"error": "method not allowed"})
			return
		}
	}
	if mf.broken || mf.flow == nil {
		status = http.StatusInternalServerError
		s.writeJSON(w, r, status, map[string]string{"error": "internal error"})
		return
	}
	// URL.Path arrives percent-decoded, so "%2e%2e%2f" becomes "../" only
	// after ServeMux has routed. Reject traversal in a captured path variable
	// before it can be rendered into a proxied upstream URL.
	if name, unsafe := flowconfig.UnsafePathVar(pathVars); unsafe {
		status = http.StatusBadRequest
		s.logger.Warn("rejected request: path variable contains a traversal segment",
			"flow", mf.name, "variable", name,
			"request_id", requestIDFromContext(r.Context()))
		s.writeJSON(w, r, status, map[string]string{"error": "bad request"})
		return
	}
	waitStart := time.Now()
	queued := false
	result := s.gate.acquire(r.Context(), func() { queued = true; s.metrics.AdmissionWaiting(1) })
	if queued {
		s.metrics.AdmissionWaiting(-1)
		s.metrics.AdmissionWait(mf.name, string(result), time.Since(waitStart))
	} else if result == gateFull && s.cfg.Server.MaxPendingRequests > 0 {
		s.metrics.AdmissionWait(mf.name, string(result), 0)
	}
	if result != gateAdmitted {
		overload = mf.overload
		status = http.StatusServiceUnavailable
		w.Header().Set("Retry-After", "1")
		if result == gateFull || result == gateTimeout {
			s.metrics.Saturated(mf.name, "", "")
		}
		s.writeJSON(w, r, status, map[string]string{"error": "server busy"})
		return
	}
	defer s.gate.release()

	if !s.admission.Enter() {
		overload = mf.overload
		status = http.StatusServiceUnavailable
		w.Header().Set("Retry-After", "1")
		s.writeJSON(w, r, status, map[string]string{"error": "server closing"})
		return
	}
	defer s.admission.Leave()

	sourceIP := s.helpers().sourceIP(r)
	head := flowconfig.HeadFromRequest(r, pathVars, sourceIP)
	head.RequestID = requestIDFromContext(r.Context())
	head.TraceID = traceIDFromContext(r.Context())
	head.Traceparent = traceparentFromContext(r.Context())
	eventID, err := uuid.NewV7()
	if err == nil {
		head.EventID = eventID.String()
	} else {
		head.EventID = uuid.NewString()
	}
	// The request settles with its final status — including a panic's 500 —
	// only after the response is decided, so capture never shapes it.
	creq := s.capture.Begin(mf.name, head)
	defer func() {
		if rec := recover(); rec != nil {
			creq.Settle(http.StatusInternalServerError)
			panic(rec)
		}
		creq.Settle(status)
	}()

	ctx := flowconfig.WithBodyReader(r.Context(), r.Body)
	ctx = context.WithValue(ctx, captureRequestKey{}, creq)
	var dropped atomic.Bool
	var publishFailed atomic.Bool
	opts := graph.RunOptions{
		PublishTimeout:    s.cfg.Server.PublishTimeout,
		FanoutConcurrency: s.cfg.Server.FanoutConcurrencyLimit(),
		OnNode: func(ev graph.NodeEvent) {
			outcome := "ok"
			if ev.Dropped {
				outcome = "dropped"
			}
			if ev.Err != nil {
				outcome = "error"
				var panicErr graph.PanicError
				if errors.As(ev.Err, &panicErr) {
					outcome = "panic"
					s.metrics.Panic("node")
				}
			}
			s.metrics.Node(mf.name, ev.NodeID, ev.NodeType, outcome, ev.Duration)
			if ev.NodeType == "publish" {
				publishOutcome := "success"
				if ev.Err != nil {
					publishFailed.Store(true)
					publishOutcome = "error"
				}
				s.metrics.Publish(mf.name, mf.destByNode[ev.NodeID], ev.Target, publishOutcome, ev.Duration)
				if ev.Err == nil {
					s.metrics.MessagePublished(mf.name, mf.destByNode[ev.NodeID], ev.Target)
				}
			}
			if ev.Dropped {
				dropped.Store(true)
				level := "flow"
				if ev.DropReason != "" {
					level = ev.DropReason
				}
				s.metrics.Filtered(mf.name, "", "", level)
			}
			started := time.Now().Add(-ev.Duration)
			_, span := nodeTracer.Start(r.Context(), "hookway.node."+ev.NodeType,
				trace.WithTimestamp(started),
				trace.WithAttributes(
					attribute.String("hookway.flow", mf.name),
					attribute.String("hookway.node_id", ev.NodeID),
					attribute.String("hookway.node_type", ev.NodeType),
					attribute.String("hookway.request_id", head.RequestID),
					attribute.String("hookway.event_id", head.EventID),
				),
			)
			attrs := []any{"flow", mf.name, "node_id", ev.NodeID, "node_type", ev.NodeType,
				"duration_ms", ev.Duration.Milliseconds(),
				"request_id", head.RequestID, "trace_id", head.TraceID, "event_id", head.EventID}
			if ev.DropReason != "" {
				attrs = append(attrs, "drop_reason", ev.DropReason)
			}
			if ev.Err != nil {
				span.RecordError(ev.Err)
				span.SetStatus(codes.Error, ev.Err.Error())
				s.logger.Error("node failed", append(attrs, "error", ev.Err.Error())...)
			} else {
				s.logger.Debug("node", attrs...)
			}
			span.End(trace.WithTimestamp(time.Now()))
		},
	}
	if creq != nil {
		opts.OnFailure = creq.OnFailure
	}
	term, runErr := mf.flow.Run(ctx, head, opts)
	if graph.NeedsCompensation(term, runErr) {
		timeout := s.cfg.Server.PublishTimeout
		if cErr := graph.RunCompensations(r.Context(), term, runErr, timeout); cErr != nil {
			s.logger.Error("replay reservation release failed; duplicate suppression "+
				"may reject a legitimate retry until the TTL expires",
				"error", cErr, "flow", mf.name,
				"request_id", requestIDFromContext(r.Context()))
			s.metrics.CompensationFailed(mf.name)
		}
	}
	published, failed := deliveryMaps(mf, term)
	executionOutcome = flowOutcome(mf, term, runErr, dropped.Load(), publishFailed.Load())
	if runErr != nil {
		s.recordFlowError(mf, statusFromNodeError(runErr), runErr)
	}
	if policy, configured := mf.responses[executionOutcome]; configured && (runErr != nil || term.Response == nil) {
		status = policy.Status
		for name, value := range policy.Headers {
			w.Header().Set(name, value)
		}
		w.WriteHeader(status)
		if r.Method != http.MethodHead && policy.Body != "" {
			if _, err := io.WriteString(w, policy.Body); err != nil {
				s.logger.Error("response write failed", "request_id", head.RequestID, "error", err)
			}
		}
		return
	}
	if runErr != nil {
		status = statusFromNodeError(runErr)
		if status == http.StatusServiceUnavailable {
			s.writeJSON(w, r, status, map[string]any{"error": "service unavailable", "destinations": published, "failed_destinations": failed, "event_id": head.EventID, "request_id": head.RequestID})
		} else {
			s.writeFlowError(w, r, mf, status, runErr)
		}
		return
	}
	if term.Response != nil {
		status = term.Response.Status
		for k, vs := range term.Response.Header {
			for _, v := range vs {
				w.Header().Add(k, v)
			}
		}
		w.WriteHeader(status)
		if r.Method != http.MethodHead && status != http.StatusNoContent && status != http.StatusNotModified && len(term.Response.Body) > 0 {
			if _, err := w.Write(term.Response.Body); err != nil {
				s.logger.Error("response write failed", "error", err,
					"request_id", requestIDFromContext(r.Context()),
					"trace_id", traceIDFromContext(r.Context()))
			}
		}
		return
	}
	status = http.StatusAccepted
	body := map[string]any{
		"status": "accepted", "endpoint": mf.name, "destinations": published,
		"event_id": head.EventID, "request_id": head.RequestID,
	}
	if len(failed) > 0 {
		body["failed_destinations"] = failed
	}
	s.writeJSON(w, r, status, body)
}

func (s *FlowServer) writeJSON(w http.ResponseWriter, r *http.Request, status int, body any) {
	writeJSONLogged(s.logger, w, status, body, requestIDFromContext(r.Context()), traceIDFromContext(r.Context()))
}

func (s *FlowServer) writeFlowError(w http.ResponseWriter, r *http.Request, mf mountedFlow, status int, err error) {
	switch status {
	case http.StatusUnauthorized:
		s.writeJSON(w, r, status, map[string]string{"error": "unauthorized"})
	case http.StatusForbidden:
		s.writeJSON(w, r, status, map[string]string{"error": "forbidden"})
	case http.StatusConflict:
		s.writeJSON(w, r, status, map[string]string{"error": "replay detected"})
	case http.StatusRequestEntityTooLarge:
		s.writeJSON(w, r, status, map[string]string{"error": "payload too large"})
	case http.StatusUnprocessableEntity:
		s.writeJSON(w, r, status, map[string]string{"error": "filtered"})
	case http.StatusTooManyRequests:
		s.writeJSON(w, r, status, map[string]string{"error": "rate limited"})
	case http.StatusServiceUnavailable:
		s.writeJSON(w, r, status, map[string]string{"error": "service unavailable"})
	case http.StatusGatewayTimeout:
		s.writeJSON(w, r, status, map[string]string{"error": "upstream timeout"})
	case http.StatusBadGateway:
		s.writeJSON(w, r, status, map[string]string{"error": "upstream error"})
	case http.StatusBadRequest:
		if strings.Contains(err.Error(), "loop detected") {
			s.writeJSON(w, r, status, map[string]string{"error": "loop detected"})
			return
		}
		s.writeJSON(w, r, status, map[string]string{"error": "invalid body"})
	default:
		s.writeJSON(w, r, status, map[string]string{"error": "internal error"})
	}
}

func statusFromNodeError(err error) int {
	var ne graph.NodeError
	if errors.As(err, &ne) && ne.Status > 0 {
		return ne.Status
	}
	return http.StatusInternalServerError
}

func deliveryMaps(mf mountedFlow, term graph.Terminal) (published, failed []map[string]string) {
	published = make([]map[string]string, 0, len(term.Deliveries))
	failed = make([]map[string]string, 0)
	for _, d := range term.Deliveries {
		item := map[string]string{
			"destination_queue_id": mf.destByNode[d.NodeID],
			"queue":                d.Target,
		}
		if d.RenderedTarget != "" {
			item["queue"] = d.RenderedTarget
		}
		if d.Err != nil || d.Unattempted {
			failed = append(failed, item)
			continue
		}
		published = append(published, item)
	}
	return published, failed
}

// bindSecretRef builds a secret resolver and returns both its lookup function
// and its closer. The closer must be retained: the resolver runs a refresh
// ticker goroutine that otherwise outlives the server.
func bindSecretRef(o flowconfig.SecretRefOptions, logger *slog.Logger, m *metrics.Metrics, validators ...func([]byte) error) (flowconfig.SecretsFunc, io.Closer, error) {
	ref := secretref.Ref{Provider: o.Provider, Name: o.Name, JSONField: o.JSONField, Selection: o.Selection}
	if len(validators) > 0 {
		ref.Validate = validators[0]
	}
	if o.Refresh != "" {
		d, err := time.ParseDuration(o.Refresh)
		if err != nil {
			return nil, nil, err
		}
		ref.Refresh = d
	}
	if o.Overlap != "" {
		d, err := time.ParseDuration(o.Overlap)
		if err != nil {
			return nil, nil, err
		}
		ref.Overlap = d
	}
	r, err := secretref.New(ref, logger, func() {
		m.SecretRefreshFailure(ref.Provider, ref.Name)
	})
	if err != nil {
		return nil, nil, err
	}
	return r.Secrets, r, nil
}

type flowReadyState struct {
	Ready   bool     `json:"ready"`
	Unready []string `json:"unready,omitempty"`
}

func (s *FlowServer) handleReady(w http.ResponseWriter, r *http.Request) {
	if state := s.lifecycle.Load(); state != 0 {
		status := "withdrawing"
		if state >= 2 {
			status = "draining"
		}
		if state == 3 {
			status = "stopped"
		}
		s.writeJSON(w, r, http.StatusServiceUnavailable, map[string]any{"error": "not ready", "status": status})
		return
	}
	if s.protection != nil {
		if stores := s.protection.unready(time.Now(), s.cfg.Server.ReadinessInterval); len(stores) > 0 {
			s.writeJSON(w, r, http.StatusServiceUnavailable, map[string]any{"error": "protection unavailable", "status": "not_ready", "state_stores": stores})
			return
		}
	}
	dest, fresh, _ := s.readiness.destinationSnapshot(time.Now())
	if !fresh {
		s.writeJSON(w, r, http.StatusServiceUnavailable, map[string]any{
			"error": "not ready", "status": "not_ready",
		})
		return
	}
	flows := map[string]flowReadyState{}
	names := make([]string, 0, len(s.flows))
	for name := range s.flows {
		names = append(names, name)
	}
	sort.Strings(names)
	readyCount := 0
	for _, name := range names {
		mf := s.flows[name]
		var unready []string
		if mf.broken {
			unready = append(unready, "flow_compile")
		}
		for _, id := range mf.destIDs {
			if destErr := destError(dest, id); destErr != nil {
				unready = append(unready, id)
			}
		}
		state := flowReadyState{Ready: len(unready) == 0, Unready: unready}
		if state.Ready {
			readyCount++
		}
		flows[name] = state
	}
	allReady := readyCount == len(flows)
	httpOK := allReady || (!s.cfg.Server.ReadinessRequireAll && readyCount > 0)
	if !httpOK {
		s.writeJSON(w, r, http.StatusServiceUnavailable, map[string]any{
			"error": "not ready", "status": "not_ready", "flows": flows,
		})
		return
	}
	status := "ready"
	if !allReady {
		status = "degraded"
	}
	s.writeJSON(w, r, http.StatusOK, map[string]any{"status": status, "flows": flows})
}

func destError(dest map[string]error, id string) error {
	if dest == nil {
		return errNotProbed
	}
	if err, ok := dest[id]; ok {
		return err
	}
	if err, ok := dest[destWildcard]; ok {
		return err
	}
	return errNotProbed
}

// Record gate failures using their execution status before response policies
// can map them to compatibility acknowledgements.
func (s *FlowServer) recordFlowError(mf mountedFlow, status int, err error) {
	var ne graph.NodeError
	if errors.As(err, &ne) && ne.Reason == "auth_unavailable" {
		s.metrics.AuthFailure(mf.name, "key_unavailable")
		return
	}
	switch status {
	case http.StatusUnauthorized:
		s.metrics.AuthFailure(mf.name, auth.FailureReason(err))
	case http.StatusForbidden:
		s.metrics.Forbidden(mf.name, "", "")
	case http.StatusConflict:
		s.metrics.DuplicateRejected(mf.name, "", "")
	case http.StatusUnprocessableEntity:
		s.metrics.Filtered(mf.name, "", "", "endpoint")
	case http.StatusTooManyRequests:
		s.metrics.RateLimited(mf.name, "", "")
	case http.StatusBadRequest:
		if strings.Contains(err.Error(), "loop detected") {
			s.metrics.LoopRejected(mf.name, "", "")
		}
	}
}

func sortedRoutingStores(stores map[string]routing.StoreConfig) []string {
	names := make([]string, 0, len(stores))
	for name := range stores {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}
