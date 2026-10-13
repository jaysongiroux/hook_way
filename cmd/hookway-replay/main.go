// Command hookway-replay is the optional replay service. It stores failed
// deliveries that core captured, and lets operators inspect, download,
// retry, and dismiss them.
package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/prometheus/client_golang/prometheus/promhttp"

	"github.com/jaysongiroux/hook_way/internal/cli"
	hookconfig "github.com/jaysongiroux/hook_way/internal/config"
	hooklogging "github.com/jaysongiroux/hook_way/internal/logging"
	"github.com/jaysongiroux/hook_way/internal/replay/api"
	"github.com/jaysongiroux/hook_way/internal/replay/auth"
	replayconfig "github.com/jaysongiroux/hook_way/internal/replay/config"
	"github.com/jaysongiroux/hook_way/internal/replay/gc"
	"github.com/jaysongiroux/hook_way/internal/replay/metrics"
	"github.com/jaysongiroux/hook_way/internal/replay/store"
	"github.com/jaysongiroux/hook_way/internal/replay/worker"
)

// version and commit identify the build; -ldflags overrides them (see the
// Makefile's build target).
var (
	version = "dev"
	commit  = "none"
)

const (
	openTimeout       = 10 * time.Second
	migrateTimeout    = 5 * time.Minute
	shutdownTimeout   = 30 * time.Second
	sampleInterval    = 15 * time.Second
	sampleTimeout     = 5 * time.Second
	readHeaderTimeout = 5 * time.Second
	readTimeout       = 2 * time.Minute
	writeTimeout      = 2 * time.Minute
	idleTimeout       = 2 * time.Minute
	maxHeaderBytes    = 16 << 10
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	code := run(ctx, os.Args[1:], os.Stdout, os.Stderr)
	stop()
	os.Exit(code)
}

// run dispatches a subcommand and returns the process exit code.
func run(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		program.WriteHelp(stderr)
		return cli.ExitUsage
	}
	switch args[0] {
	case "serve":
		return runServe(ctx, args[1:], stdout, stderr)
	case "migrate":
		return runMigrate(ctx, args[1:], stdout, stderr)
	case "gc":
		return runGC(ctx, args[1:], stdout, stderr)
	case "validate":
		return runValidate(args[1:], stdout, stderr)
	case "healthcheck":
		return runHealthcheck(args[1:], stdout, stderr)
	case "version":
		if code, ok := program.Parse("version", flag.NewFlagSet("version", flag.ContinueOnError), args[1:], stdout, stderr); !ok {
			return code
		}
		writef(stdout, "hookway-replay %s (%s)\n", version, commit)
		return cli.ExitOK
	case "help":
		return runHelp(ctx, args[1:], stdout, stderr)
	case "-h", "-help", "--help":
		program.WriteHelp(stdout)
		return cli.ExitOK
	default:
		return program.UnknownCommand(stderr, args[0])
	}
}

// runHelp prints the command list, or one command's help page.
func runHelp(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		program.WriteHelp(stdout)
		return cli.ExitOK
	}
	if _, ok := program.Lookup(args[0]); !ok || len(args) > 1 {
		return program.UnknownCommand(stderr, strings.Join(args, " "))
	}
	return run(ctx, []string{args[0], "-h"}, stdout, stderr)
}

// configPath parses a command whose only flag is --config (falling back to
// HOOKWAY_REPLAY_CONFIG). When ok is false the caller returns code.
func configPath(name string, args []string, stdout, stderr io.Writer) (path string, code int, ok bool) {
	flags := flag.NewFlagSet(name, flag.ContinueOnError)
	configFlag := flags.String("config", "", "the config `FILE` (default $HOOKWAY_REPLAY_CONFIG)")
	if code, ok := program.Parse(name, flags, args, stdout, stderr); !ok {
		return "", code, false
	}
	path = *configFlag
	if path == "" {
		path = os.Getenv("HOOKWAY_REPLAY_CONFIG")
	}
	if path == "" {
		writef(stderr, "config path required via --config or HOOKWAY_REPLAY_CONFIG\n")
		return "", cli.ExitFail, false
	}
	return path, cli.ExitOK, true
}

// loadConfig returns the config and its warnings, or an error listing every
// blocking issue.
func loadConfig(path string) (replayconfig.Config, []hookconfig.ValidationIssue, error) {
	cfg, issues := replayconfig.Load(path)
	var blocking, warnings []hookconfig.ValidationIssue
	for _, issue := range issues {
		if issue.EffectiveSeverity() == hookconfig.SeverityWarning {
			warnings = append(warnings, issue)
		} else {
			blocking = append(blocking, issue)
		}
	}
	if len(blocking) > 0 {
		return replayconfig.Config{}, warnings, hookconfig.ConfigValidationError{Issues: blocking}
	}
	return cfg, warnings, nil
}

func runValidate(args []string, stdout, stderr io.Writer) int {
	path, code, ok := configPath("validate", args, stdout, stderr)
	if !ok {
		return code
	}
	_, warnings, err := loadConfig(path)
	if err != nil {
		writeln(stderr, err)
		return 1
	}
	for _, w := range warnings {
		writef(stderr, "warning: %s: %s\n", w.Field, w.Message)
	}
	writef(stdout, "config valid: %s\n", path)
	return 0
}

// runHealthcheck succeeds when the URL answers with any 2xx status.
func runHealthcheck(args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("healthcheck", flag.ContinueOnError)
	url := flags.String("url", "http://127.0.0.1:8080/healthz", "the `URL` to GET")
	timeout := flags.Duration("timeout", 5*time.Second, "give up after `DURATION`")
	if code, ok := program.Parse("healthcheck", flags, args, stdout, stderr); !ok {
		return code
	}
	ctx, cancel := context.WithTimeout(context.Background(), *timeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, *url, nil)
	if err != nil {
		writef(stderr, "healthcheck failed: %v\n", err)
		return 1
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		writef(stderr, "healthcheck failed: %v\n", err)
		return 1
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		writef(stderr, "healthcheck failed: status %d\n", resp.StatusCode)
		return 1
	}
	writeln(stdout, "healthy")
	return 0
}

func openStore(ctx context.Context, cfg replayconfig.Config) (*store.Store, error) {
	ctx, cancel := context.WithTimeout(ctx, openTimeout)
	defer cancel()
	return store.Open(ctx, store.Options{
		URL: cfg.Database.URL, MaxConns: cfg.Database.MaxConns, TLS: cfg.Database.TLS.Files(),
		Limits: store.Limits{TTL: cfg.Retention.TTL, MaxRecords: cfg.Retention.MaxRecords, MaxBytes: cfg.Retention.MaxBytes},
	})
}

func runMigrate(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	path, code, ok := configPath("migrate", args, stdout, stderr)
	if !ok {
		return code
	}
	cfg, _, err := loadConfig(path)
	if err != nil {
		writeln(stderr, err)
		return 1
	}
	ctx, cancel := context.WithTimeout(ctx, migrateTimeout)
	defer cancel()
	st, err := openStore(ctx, cfg)
	if err != nil {
		writef(stderr, "database connection failed: %v\n", err)
		return 1
	}
	defer st.Close()
	applied, err := st.Migrate(ctx)
	if err != nil {
		writef(stderr, "migrate failed: %v\n", err)
		return 1
	}
	current, err := st.SchemaVersion(ctx)
	if err != nil {
		writef(stderr, "read schema version failed: %v\n", err)
		return 1
	}
	if len(applied) == 0 {
		writef(stdout, "schema is current at version %d\n", current)
	} else {
		writef(stdout, "applied migrations %v; schema is at version %d\n", applied, current)
	}
	return 0
}

func runGC(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	path, code, ok := configPath("gc", args, stdout, stderr)
	if !ok {
		return code
	}
	cfg, _, err := loadConfig(path)
	if err != nil {
		writeln(stderr, err)
		return 1
	}
	st, err := openStore(ctx, cfg)
	if err != nil {
		writef(stderr, "database connection failed: %v\n", err)
		return 1
	}
	defer st.Close()
	if err := st.Ready(ctx); err != nil {
		writef(stderr, "database schema compatibility check failed: %s\n", hooklogging.ErrorText(err))
		return 1
	}
	res, err := gc.Once(ctx, st, metrics.New(), slog.New(slog.DiscardHandler))
	switch {
	case errors.Is(err, store.ErrGCLocked):
		writef(stdout, "gc skipped: another replica is sweeping\n")
	case err != nil:
		writef(stderr, "gc failed after deleting %d records: %v\n", res.Records, err)
		return 1
	default:
		writef(stdout, "gc deleted %d records and freed %d bytes\n", res.Records, res.Bytes)
	}
	return 0
}

// workerID names this process in run claims and history: host plus a
// random suffix, so restarts and co-located replicas never share one.
func workerID() string {
	host, err := os.Hostname()
	if err != nil || host == "" {
		host = "replay"
	}
	var b [4]byte
	_, _ = rand.Read(b[:])
	return host + "-" + hex.EncodeToString(b[:])
}

func runServe(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	path, code, ok := configPath("serve", args, stdout, stderr)
	if !ok {
		return code
	}
	levelVar := new(slog.LevelVar)
	m := metrics.New()
	logger := slog.New(hooklogging.StormHandler(slog.NewJSONHandler(stdout, &slog.HandlerOptions{Level: levelVar}), m.LogSuppressed))
	logger.Info("hookway-replay starting", "version", version, "commit", commit)
	cfg, warnings, err := loadConfig(path)
	if err != nil {
		logger.Error("config load failed", "error", err)
		return 1
	}
	level, err := logLevel(cfg.Logging.Level)
	if err != nil {
		logger.Error("invalid log level", "error", err)
		return 1
	}
	levelVar.Set(level)
	for _, w := range warnings {
		logger.Warn("config warning", "field", w.Field, "message", w.Message)
	}

	m.SetBuildInfo(version, commit)
	if cfg.Metrics.RuntimeMetricsEnabled() {
		m.EnableRuntimeCollectors()
	}
	m.SetCapacity(cfg.Retention.MaxRecords, cfg.Retention.MaxBytes)
	m.SetGCInterval(cfg.Retention.GCInterval)

	st, err := openStore(ctx, cfg)
	if err != nil {
		logger.Error("database connection failed", "error", hooklogging.ErrorText(err))
		return 1
	}
	defer st.Close()
	if err := st.Ready(ctx); err != nil {
		logger.Error("database schema compatibility check failed", "error", err)
		return 1
	}
	m.RegisterPool(func() metrics.PoolStats {
		s := st.PoolStat()
		return metrics.PoolStats{Acquired: s.AcquiredConns(), Idle: s.IdleConns(), Total: s.TotalConns(),
			Max: s.MaxConns(), EmptyAcquires: s.EmptyAcquireCount()}
	})
	executor, err := worker.NewExecutor(worker.ExecutorOptions{
		ControlURL: cfg.Core.ControlURL, Token: cfg.Core.ControlToken, TLS: cfg.Core.TLS.Files(),
		ExecutionTimeout: cfg.Workers.ExecutionTimeout, MaxRecordBytes: cfg.Retention.MaxRecordBytes, Metrics: m,
	})
	if err != nil {
		logger.Error("core control client failed", "error", err)
		return 1
	}
	w := worker.New(worker.Options{Store: st, Deliverer: executor, Metrics: m, Logger: logger, ID: workerID(),
		Concurrency: cfg.Workers.Concurrency, Lease: cfg.Workers.Lease})

	authn := auth.New(cfg.AuthKeys(), m.AuthFailure)
	metricsHandler := promhttp.HandlerFor(m.Registry(), promhttp.HandlerOpts{})
	opts := api.Options{Store: st, Auth: authn, Metrics: m, Logger: logger, MaxRecordBytes: cfg.Retention.MaxRecordBytes, AllowedOrigins: cfg.Server.CORS.AllowedOrigins}
	if cfg.Metrics.Enabled && cfg.Metrics.Addr == "" {
		opts.MetricsPath, opts.MetricsHandler = cfg.Metrics.Path, metricsHandler
	}
	servers := []*http.Server{newServer(cfg.Server.Addr, api.New(opts))}
	if cfg.Metrics.Enabled && cfg.Metrics.Addr != "" {
		mux := http.NewServeMux()
		mux.Handle(cfg.Metrics.Path, authn.Require(auth.ScopeMetrics, metricsHandler))
		servers = append(servers, newServer(cfg.Metrics.Addr, mux))
	}
	return serve(ctx, logger, servers, func(ctx context.Context) {
		var wg sync.WaitGroup
		for _, loop := range []func(){
			func() { runSampler(ctx, st, m, logger) },
			func() { w.Run(ctx) },
			func() { gc.Loop(ctx, cfg.Retention.GCInterval, st, m, logger) },
		} {
			wg.Add(1)
			go func() {
				defer wg.Done()
				loop()
			}()
		}
		wg.Wait()
	})
}

func newServer(addr string, h http.Handler) *http.Server {
	return &http.Server{Addr: addr, Handler: h, ReadHeaderTimeout: readHeaderTimeout,
		ReadTimeout: readTimeout, WriteTimeout: writeTimeout, IdleTimeout: idleTimeout, MaxHeaderBytes: maxHeaderBytes}
}

// serve runs the servers and a background loop until ctx ends or a server
// fails, then drains the servers and stops the loop.
func serve(ctx context.Context, logger *slog.Logger, servers []*http.Server, background func(context.Context)) int {
	bgCtx, stopBackground := context.WithCancel(ctx)
	bgDone := make(chan struct{})
	go func() {
		defer close(bgDone)
		background(bgCtx)
	}()
	errs := make(chan error, len(servers))
	for _, srv := range servers {
		go func() {
			logger.Info("hookway-replay listening", "addr", srv.Addr)
			if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
				errs <- fmt.Errorf("%s: %w", srv.Addr, err)
			}
		}()
	}
	code := 0
	select {
	case <-ctx.Done():
		logger.Info("shutdown signal received")
	case err := <-errs:
		logger.Error("server stopped", "error", err)
		code = 1
	}
	// Stop claiming immediately, including when a listener fails while ctx is
	// still live. Already claimed runs keep their own lease-bounded contexts.
	stopBackground()
	shutdownCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), shutdownTimeout)
	defer cancel()
	for _, srv := range servers {
		if err := srv.Shutdown(shutdownCtx); err != nil {
			logger.Error("server shutdown failed", "addr", srv.Addr, "error", err)
			code = 1
		}
	}
	stopBackground()
	<-bgDone
	logger.Info("hookway-replay stopped")
	return code
}

// runSampler refreshes the state gauges every sampleInterval: one cheap
// query set per replica, so a scrape never queries the database.
func runSampler(ctx context.Context, st *store.Store, m *metrics.Metrics, logger *slog.Logger) {
	sample := func() {
		sctx, cancel := context.WithTimeout(ctx, sampleTimeout)
		defer cancel()
		stats, err := st.Stats(sctx)
		if err != nil {
			m.SetDBUp(false)
			if ctx.Err() == nil {
				logger.Warn("state sample failed", "error", hooklogging.ErrorText(err))
			}
			return
		}
		m.SetDBUp(true)
		m.SetState(metrics.State{RecordsByState: stats.RecordsByState, UsageRecords: stats.UsageRecords,
			UsageBytes: stats.UsageBytes, SchemaVersion: stats.SchemaVersion})
		m.SetRunState(metrics.RunState{Queued: stats.RunsByStatus[store.RunQueued], Running: stats.RunsByStatus[store.RunRunning],
			OldestQueued: stats.OldestQueuedAge})
	}
	sample()
	ticker := time.NewTicker(sampleInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			sample()
		}
	}
}

func logLevel(cfgLevel string) (slog.Level, error) {
	if env := strings.TrimSpace(os.Getenv("HOOKWAY_LOG_LEVEL")); env != "" {
		return hooklogging.ParseLevel(env)
	}
	return hooklogging.ParseLevel(cfgLevel)
}

func writef(w io.Writer, format string, args ...any) { _, _ = fmt.Fprintf(w, format, args...) }

func writeln(w io.Writer, args ...any) { _, _ = fmt.Fprintln(w, args...) }
