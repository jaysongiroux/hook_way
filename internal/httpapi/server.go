package httpapi

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"runtime/debug"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/prometheus/client_golang/prometheus/promhttp"

	"github.com/jasongiroux/hookway/internal/apikey"
	"github.com/jasongiroux/hookway/internal/config"
	"github.com/jasongiroux/hookway/internal/metrics"
)

// Server holds HTTP helpers shared by FlowServer (metrics, source IP,
// request/trace ids, panic recovery, replay capture).
type Server struct {
	cfg            config.Config
	logger         *slog.Logger
	metrics        *metrics.Metrics
	trustedProxies []*net.IPNet
	readiness      *readinessProber
	replay         ReplayCapturer
}

const readHeaderTimeout = 5 * time.Second

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
		Addr:              s.cfg.Metrics.Addr,
		Handler:           mux,
		ReadHeaderTimeout: readHeaderTimeout,
		ReadTimeout:       s.cfg.Server.ReadTimeout,
		WriteTimeout:      s.cfg.Server.WriteTimeout,
		IdleTimeout:       s.cfg.Server.IdleTimeout,
	}
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
		writeJSONLogged(s.logger, rw, http.StatusInternalServerError, map[string]string{"error": "internal error"}, requestID, traceID)
	}
}

func (s *Server) handleLive(w http.ResponseWriter, r *http.Request) {
	writeJSONLogged(s.logger, w, http.StatusOK, map[string]string{"status": "ok"}, requestIDFromContext(r.Context()), traceIDFromContext(r.Context()))
}

func (s *Server) sourceIP(r *http.Request) string {
	peer := peerHost(r.RemoteAddr)
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

func (s *Server) trustsPeer(peer string) bool {
	if !s.cfg.Security.TrustedProxyHeaders {
		return false
	}
	if len(s.trustedProxies) == 0 {
		return true
	}
	return s.isTrustedProxy(peer)
}

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
		if len(parts) >= 2 && isValidTraceID(parts[1]) {
			return parts[1]
		}
	}
	if id := strings.TrimSpace(r.Header.Get("X-Trace-ID")); isValidTraceID(id) {
		return id
	}
	return strings.ReplaceAll(uuid.NewString(), "-", "")
}

func isValidTraceID(id string) bool {
	if len(id) != 32 || !isHex(id) {
		return false
	}
	for i := 0; i < len(id); i++ {
		if id[i] != '0' {
			return true
		}
	}
	return false
}

func traceparentFromRequest(r *http.Request, traceID string) string {
	if tp := r.Header.Get("Traceparent"); isValidTraceparent(tp) {
		return tp
	}
	return "00-" + traceID + "-" + randomSpanID() + "-01"
}

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
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c >= '0' && c <= '9', c >= 'a' && c <= 'f', c >= 'A' && c <= 'F':
		default:
			return false
		}
	}
	return true
}

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

func writeJSONLogged(logger *slog.Logger, w http.ResponseWriter, status int, body any, requestID, traceID string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(body); err != nil && logger != nil {
		logger.Error("response write failed", "error", err, "request_id", requestID, "trace_id", traceID)
	}
}
