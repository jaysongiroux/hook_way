package httpapi

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/jasongiroux/hookway/internal/config"
	"github.com/jasongiroux/hookway/internal/flowconfig"
	"github.com/jasongiroux/hookway/internal/graph"
	hooklogging "github.com/jasongiroux/hookway/internal/logging"
	"github.com/jasongiroux/hookway/internal/metrics"
	"github.com/jasongiroux/hookway/internal/queue"
	"github.com/jasongiroux/hookway/internal/vars"
)

// FlowServer serves schema_version 2 flows through the graph executor.
// V1 Server stays untouched until Chunk 9.
type FlowServer struct {
	cfg            flowconfig.Config
	v1cfg          config.Config
	publisher      queue.Publisher
	logger         *slog.Logger
	metrics        *metrics.Metrics
	trustedProxies []*net.IPNet
	inflight       chan struct{}
	literalPaths   map[string]string
	patternPaths   []flowPattern
	flows          map[string]mountedFlow
	mux            *http.ServeMux
	readiness      *readinessProber
}

type flowPattern struct {
	name    string
	pattern vars.PathPattern
}

type mountedFlow struct {
	name       string
	methods    map[string]struct{}
	allow      string
	flow       *graph.Flow
	destByNode map[string]string
	// broken is set when graph.Compile failed. The path is still registered
	// so matching requests return 500 rather than 404 (I13).
	broken bool
}

type queueResolver struct{ pub queue.Publisher }

func (r queueResolver) Resolve(string) (flowconfig.Publisher, error) {
	return queuePublisherAdapter{pub: r.pub}, nil
}

type queuePublisherAdapter struct{ pub queue.Publisher }

func (a queuePublisherAdapter) Publish(ctx context.Context, destinationID, renderedTarget string, env graph.Envelope) error {
	return a.pub.Publish(ctx, destinationID, renderedTarget, graphEnvelopeToMessage(env, destinationID, renderedTarget))
}

func graphEnvelopeToMessage(env graph.Envelope, destID, target string) queue.Message {
	if cap, ok := env.Extra.(flowconfig.CapturedEvent); ok {
		msg := envelopeToMessage(cap.Event)
		msg.DestinationQueueID = destID
		msg.Queue = target
		if env.Vars != nil {
			msg.Variables = env.Vars
		}
		return attachMessageAttributes(msg, env)
	}
	head := env.V.Raw.Head
	query := map[string]any{}
	for k, v := range head.Query {
		query[k] = append([]string(nil), v...)
	}
	var rawB64 *string
	if env.Body == nil && len(env.V.Raw.Body) > 0 {
		encoded := base64.StdEncoding.EncodeToString(env.V.Raw.Body)
		rawB64 = &encoded
	}
	return attachMessageAttributes(queue.Message{
		SchemaVersion: "1.0", ID: head.EventID, RequestID: head.RequestID, TraceID: head.TraceID,
		Traceparent: head.Traceparent, Endpoint: head.Path, DestinationQueueID: destID, Queue: target,
		ReceivedAt: time.Now().UTC(), Method: head.Method, Path: head.Path, Headers: head.Header,
		Query: query, Body: env.Body, RawBodyBase64: rawB64, SourceIP: head.SourceIP,
		Variables: env.Vars, RawBody: env.V.Raw.Body, OrigHeaders: head.Header, Hops: head.Hops,
	}, env)
}

func attachMessageAttributes(msg queue.Message, env graph.Envelope) queue.Message {
	if len(env.MessageAttributes) == 0 {
		return msg
	}
	msg.MessageAttributes = make(map[string]queue.MessageAttribute, len(env.MessageAttributes))
	for k, v := range env.MessageAttributes {
		msg.MessageAttributes[k] = queue.MessageAttribute{DataType: v.DataType, StringValue: v.Value}
	}
	return msg
}

func NewFlowServer(cfg flowconfig.Config, pub queue.Publisher, logger *slog.Logger, m *metrics.Metrics) (*FlowServer, error) {
	if logger == nil {
		logger = slog.Default()
	}
	if m == nil {
		m = metrics.New()
	}
	v1cfg := cfg.ServingConfig()
	reg, err := flowconfig.NewRegistryWithOptions(flowconfig.RegistryOptions{
		Publishers:      queueResolver{pub: pub},
		Logger:          logger,
		HeaderDenylist:  cfg.Security.RedactedHeaderDenylist,
		HeaderAllowlist: cfg.Security.ForwardedHeaderAllowlist,
	})
	if err != nil {
		return nil, err
	}
	s := &FlowServer{
		cfg:          cfg,
		v1cfg:        v1cfg,
		publisher:    pub,
		logger:       logger,
		metrics:      m,
		literalPaths: map[string]string{},
		flows:        map[string]mountedFlow{},
		mux:          http.NewServeMux(),
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
			return nil, fmt.Errorf("flow %q: %w", name, err)
		}
		compiled, compileErr := graph.Compile(name, fc, reg)
		if compileErr != nil {
			logger.Error("flow failed to compile; path will return 500 not 404",
				"flow", name, "error", compileErr)
		}
		s.flows[name] = mountedFlow{
			name: name, methods: methodSet(methods), allow: strings.Join(methods, ", "),
			flow: compiled, destByNode: destByNode, broken: compileErr != nil || compiled == nil,
		}
		pattern, err := vars.ParsePath(path)
		if err != nil {
			return nil, fmt.Errorf("flow %q path: %w", name, err)
		}
		if pattern.IsLiteral() {
			s.literalPaths[path] = name
		} else {
			s.patternPaths = append(s.patternPaths, flowPattern{name: name, pattern: pattern})
		}
	}
	s.trustedProxies = parseCIDRs(cfg.Security.TrustedProxies)
	if cfg.Server.MaxInflightRequests > 0 {
		s.inflight = make(chan struct{}, cfg.Server.MaxInflightRequests)
	}
	s.metrics.RegisterConfig(v1cfg)
	s.readiness = newReadinessProber(pub, m, logger, cfg.Server.ReadinessInterval, cfg.Server.ReadinessTimeout)
	s.routes()
	return s, nil
}

func triggerRouting(fc graph.FlowConfig) (path string, methods []string, destByNode map[string]string, err error) {
	destByNode = map[string]string{}
	for _, nc := range fc.Nodes {
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

func methodSet(methods []string) map[string]struct{} {
	out := map[string]struct{}{}
	for _, m := range methods {
		out[strings.ToUpper(m)] = struct{}{}
	}
	return out
}

func (s *FlowServer) v1() *Server {
	return &Server{
		cfg: s.v1cfg, logger: s.logger, metrics: s.metrics,
		trustedProxies: s.trustedProxies, readiness: s.readiness,
	}
}

func (s *FlowServer) routes() {
	s.mux.HandleFunc("/health", s.v1().handleLive)
	s.mux.HandleFunc("/healthz", s.v1().handleLive)
	s.mux.HandleFunc("/ready", s.v1().handleReady)
	s.mux.HandleFunc("/readyz", s.v1().handleReady)
	if s.cfg.Metrics.Enabled && s.cfg.Metrics.Addr == "" {
		s.mux.Handle(s.cfg.Metrics.Path, s.v1().metricsHandler())
	}
	s.mux.HandleFunc("/", s.handleFlow)
}

func (s *FlowServer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Cache-Control", "no-store")
	requestID := s.v1().requestID(r)
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
	defer s.v1().recoverHandlerPanic(rw, requestID, traceID)
	s.mux.ServeHTTP(rw, r)
}

func (s *FlowServer) HTTPServer() *http.Server {
	return &http.Server{
		Addr:         s.cfg.Server.Addr,
		Handler:      s,
		ReadTimeout:  s.cfg.Server.ReadTimeout,
		WriteTimeout: s.cfg.Server.WriteTimeout,
		IdleTimeout:  s.cfg.Server.IdleTimeout,
	}
}

func (s *FlowServer) MetricsServer() *http.Server {
	return s.v1().MetricsServer()
}

func (s *FlowServer) StartReadinessProbe(ctx context.Context) <-chan struct{} {
	return s.readiness.run(ctx)
}

func (s *FlowServer) Close() error { return nil }

func (s *FlowServer) SetReplayCapturer(c ReplayCapturer) {
	if pinger, ok := c.(interface{ Ping(context.Context) error }); ok {
		s.readiness.setReplayPing(pinger.Ping)
	}
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
	defer func() {
		s.metrics.HTTP(flowLabel, "", "", status)
		if flowLabel != "" {
			s.metrics.RequestDuration(flowLabel, "", "", status, time.Since(start))
		}
		hooklogging.Request(s.logger, hooklogging.RequestAttrs{
			Method: r.Method, Path: r.URL.Path, Endpoint: flowLabel,
			Status: status, Duration: time.Since(start), RequestID: requestIDFromContext(r.Context()),
			TraceID: traceIDFromContext(r.Context()), Headers: r.Header,
			HeaderDenylist:  s.cfg.Security.RedactedHeaderDenylist,
			HeaderAllowlist: s.cfg.Security.ForwardedHeaderAllowlist,
		})
	}()

	mf, pathVars, ok := s.matchFlow(r.URL.Path)
	if !ok {
		status = http.StatusNotFound
		writeJSON(w, status, map[string]string{"error": "not found"})
		return
	}
	flowLabel = mf.name
	if _, allowed := mf.methods[r.Method]; !allowed {
		status = http.StatusMethodNotAllowed
		w.Header().Set("Allow", mf.allow)
		writeJSON(w, status, map[string]string{"error": "method not allowed"})
		return
	}
	if mf.broken || mf.flow == nil {
		status = http.StatusInternalServerError
		writeJSON(w, status, map[string]string{"error": "internal error"})
		return
	}
	if s.inflight != nil {
		select {
		case s.inflight <- struct{}{}:
			defer func() { <-s.inflight }()
		default:
			status = http.StatusServiceUnavailable
			w.Header().Set("Retry-After", "1")
			s.metrics.Saturated(mf.name, "", "")
			writeJSON(w, status, map[string]string{"error": "server busy"})
			return
		}
	}

	sourceIP := s.v1().sourceIP(r)
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

	ctx := flowconfig.WithBodyReader(r.Context(), r.Body)
	term, runErr := mf.flow.Run(ctx, head, graph.RunOptions{
		PublishTimeout:    s.cfg.Server.PublishTimeout,
		FanoutConcurrency: s.cfg.Server.FanoutConcurrencyLimit(),
		OnNode: func(ev graph.NodeEvent) {
			attrs := []any{"flow", mf.name, "node_id", ev.NodeID, "node_type", ev.NodeType, "duration_ms", ev.Duration.Milliseconds()}
			if ev.Err != nil {
				s.logger.Info("node failed", append(attrs, "error", ev.Err.Error())...)
				return
			}
			s.logger.Debug("node", attrs...)
		},
	})
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
	if runErr != nil {
		status = statusFromNodeError(runErr)
		s.writeFlowError(w, mf, status, runErr)
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
		_, _ = w.Write(term.Response.Body)
		return
	}
	status = http.StatusAccepted
	published := make([]map[string]string, 0, len(term.Deliveries))
	failed := make([]map[string]string, 0)
	for _, d := range term.Deliveries {
		item := map[string]string{
			"destination_queue_id": mf.destByNode[d.NodeID],
			"queue":                d.Target,
		}
		if d.Err != nil || d.Unattempted {
			failed = append(failed, item)
			continue
		}
		published = append(published, item)
	}
	body := map[string]any{
		"status": "accepted", "endpoint": mf.name, "destinations": published,
		"event_id": head.EventID, "request_id": head.RequestID,
	}
	if len(failed) > 0 {
		body["failed_destinations"] = failed
	}
	writeJSON(w, status, body)
}

func (s *FlowServer) writeFlowError(w http.ResponseWriter, mf mountedFlow, status int, err error) {
	switch status {
	case http.StatusUnauthorized:
		s.metrics.AuthFailure(mf.name, "invalid_signature")
		writeJSON(w, status, map[string]string{"error": "unauthorized"})
	case http.StatusForbidden:
		s.metrics.Forbidden(mf.name, "", "")
		writeJSON(w, status, map[string]string{"error": "forbidden"})
	case http.StatusConflict:
		s.metrics.ReplayRejected(mf.name, "", "")
		writeJSON(w, status, map[string]string{"error": "replay detected"})
	case http.StatusRequestEntityTooLarge:
		writeJSON(w, status, map[string]string{"error": "payload too large"})
	case http.StatusUnprocessableEntity:
		s.metrics.Filtered(mf.name, "", "", "endpoint")
		writeJSON(w, status, map[string]string{"error": "filtered"})
	case http.StatusTooManyRequests:
		s.metrics.RateLimited(mf.name, "", "")
		writeJSON(w, status, map[string]string{"error": "rate limited"})
	case http.StatusServiceUnavailable:
		writeJSON(w, status, map[string]string{"error": "service unavailable"})
	case http.StatusBadRequest:
		if strings.Contains(err.Error(), "loop detected") {
			s.metrics.LoopRejected(mf.name, "", "")
			writeJSON(w, status, map[string]string{"error": "loop detected"})
			return
		}
		writeJSON(w, status, map[string]string{"error": "invalid body"})
	default:
		writeJSON(w, status, map[string]string{"error": "internal error"})
	}
}

func statusFromNodeError(err error) int {
	var ne graph.NodeError
	if errors.As(err, &ne) && ne.Status > 0 {
		return ne.Status
	}
	return http.StatusInternalServerError
}
