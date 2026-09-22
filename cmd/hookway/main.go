package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/jaysongiroux/hook_way/internal/config"
	"github.com/jaysongiroux/hook_way/internal/configapi"
	"github.com/jaysongiroux/hook_way/internal/editor"
	"github.com/jaysongiroux/hook_way/internal/flowconfig"
	"github.com/jaysongiroux/hook_way/internal/httpapi"
	hooklogging "github.com/jaysongiroux/hook_way/internal/logging"
	"github.com/jaysongiroux/hook_way/internal/metrics"
	"github.com/jaysongiroux/hook_way/internal/queue"
	"github.com/jaysongiroux/hook_way/internal/replayapi"
	"github.com/jaysongiroux/hook_way/internal/replaystore"
)

// version and commit identify the build. They default to "dev"/"none" for a
// plain `go build`/`go run` and are overridden at build time via
// `-ldflags "-X main.version=... -X main.commit=..."` (see the Makefile's
// build target and the Dockerfile).
var (
	version = "dev"
	commit  = "none"
)

// replayCleanupShutdownWait bounds how long shutdown waits for the replay
// cleanup goroutine to exit after its context is cancelled. It is a small
// fixed duration -- NOT derived from cfg.Replay.CleanupInterval (default
// 1h) -- because the cleanup ctx is cancelled first and a mid-sweep pgx call
// (Ping/PurgeExpired/PurgeOverCount) respects ctx cancellation, so the
// goroutine exits near-instantly; a wedged sweep can't delay shutdown past
// this wait, and tying it to CleanupInterval would let shutdown block for up
// to an hour -- far past a typical k8s terminationGracePeriodSeconds (~30s),
// so it would never actually protect shutdown.
const replayCleanupShutdownWait = 5 * time.Second

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

// run dispatches subcommands and returns a process exit code.
func run(args []string, stdout, stderr io.Writer) int {
	if len(args) > 0 && args[0] == "validate" {
		return runValidate(args[1:], stdout, stderr)
	}
	if len(args) > 0 && args[0] == "healthcheck" {
		return runHealthcheck(args[1:], stdout, stderr)
	}
	if len(args) > 0 && args[0] == "schema" {
		return runSchema(args[1:], stdout, stderr)
	}
	if len(args) > 0 && args[0] == "editor" {
		return runEditor(args[1:], stdout, stderr)
	}
	if len(args) > 0 && args[0] == "version" {
		writef(stdout, "hookway %s (%s)\n", version, commit)
		return 0
	}
	return runServe(args, stdout, stderr)
}

// runValidate checks config syntax and semantic validation without opening network connections.
//
// Env-expansion behavior across the two output modes and --no-env:
//   - plain validate (no flags): flowconfig.LoadBytes with expansion, which
//     expands ${VAR} strictly and HARD-fails (nonzero exit) on any
//     referenced-but-unset variable.
//   - validate --json: configapi.ValidateWithEnv(data, true) also expands
//     ${VAR}, but a missing variable is reported as a "config.env.<NAME>"
//     issue at severity "warning" (exit 0 if it's the only issue) rather than
//     aborting — CI commonly validates without production secrets set.
//   - --no-env (either mode): skips expansion entirely, so ${VAR} is left as
//     an opaque literal and only structural/syntax issues can appear.
func runValidate(args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("validate", flag.ContinueOnError)
	flags.SetOutput(stderr)
	configFlag := flags.String("config", "", "path to Hookway YAML config")
	jsonOut := flags.Bool("json", false, "emit validation results as JSON")
	noEnv := flags.Bool("no-env", false, "skip ${VAR} environment expansion; check syntax and structure only")
	strict := flags.Bool("strict", false, "treat lint warnings as errors")
	if err := flags.Parse(args); err != nil {
		return 2
	}
	path, err := config.ResolvePath(*configFlag)
	if err != nil {
		writef(stderr, "config path required: %v\n", err)
		return 1
	}

	data, err := os.ReadFile(filepath.Clean(path))
	if err != nil {
		writef(stderr, "read config: %v\n", err)
		return 1
	}
	if *jsonOut {
		resp := configapi.ValidateWithEnv(data, !*noEnv)
		enc := json.NewEncoder(stdout)
		enc.SetIndent("", "  ")
		if err := enc.Encode(resp); err != nil {
			writef(stderr, "encode json: %v\n", err)
			return 1
		}
		for _, iss := range resp.Issues {
			if iss.Severity != "warning" || *strict {
				return 1
			}
		}
		return 0
	}

	var issues []config.ValidationIssue
	if *noEnv {
		issues = flowconfig.ValidateBytes(data)
	} else {
		var cfg flowconfig.Config
		cfg, issues = flowconfig.LoadBytes(data, path, true)
		issues = append(issues, flowconfig.Lint(cfg)...)
		issues = append(issues, flowconfig.StandaloneIssues(cfg)...)
	}
	return printValidateResult(issues, *strict, path, stdout, stderr)
}

func printValidateResult(issues []config.ValidationIssue, strict bool, path string, stdout, stderr io.Writer) int {
	blocking := false
	for _, issue := range issues {
		severity := issue.Severity
		if severity == "" {
			severity = config.SeverityError
		}
		if severity != config.SeverityWarning || strict {
			blocking = true
		}
	}
	if blocking {
		if len(issues) > 0 {
			writeln(stderr, config.ConfigValidationError{Issues: issues})
		}
		return 1
	}
	for _, issue := range issues {
		writef(stderr, "warning: %s: %s\n", issue.Field, issue.Message)
	}
	writef(stdout, "config valid: %s\n", path)
	return 0
}

func blockingValidationIssues(issues []config.ValidationIssue) []config.ValidationIssue {
	var blocking []config.ValidationIssue
	for _, issue := range issues {
		severity := issue.Severity
		if severity == "" {
			severity = config.SeverityError
		}
		if severity != config.SeverityWarning {
			blocking = append(blocking, issue)
		}
	}
	return blocking
}

// runSchema prints the JSON Schema describing the Hookway config to stdout.
func runSchema(args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("schema", flag.ContinueOnError)
	flags.SetOutput(stderr)
	nodes := flags.Bool("nodes", false, "emit per-node option JSON Schema metadata")
	destinations := flags.Bool("destinations", false, "emit per-destination-type JSON Schema metadata")
	if err := flags.Parse(args); err != nil {
		return 2
	}
	if *nodes {
		data, err := json.MarshalIndent(flowconfig.NodeSchemas(), "", "  ")
		if err != nil {
			writef(stderr, "generate node schema: %v\n", err)
			return 1
		}
		writeln(stdout, string(data))
		return 0
	}
	if *destinations {
		data, err := json.MarshalIndent(config.DestinationTypeSchemas(), "", "  ")
		if err != nil {
			writef(stderr, "generate destination schema: %v\n", err)
			return 1
		}
		writeln(stdout, string(data))
		return 0
	}
	data, err := configapi.Schema()
	if err != nil {
		writef(stderr, "generate schema: %v\n", err)
		return 1
	}
	writeln(stdout, string(data))
	return 0
}

// editorAPIKey resolves the config-API auth key from config. When no config is
// available (neither --config nor HOOKWAY_CONFIG), it returns "" so the editor
// runs unauthenticated. A provided-but-unloadable config is a hard error.
func editorAPIKey(configFlag string) (string, error) {
	if strings.TrimSpace(configFlag) == "" && os.Getenv("HOOKWAY_CONFIG") == "" {
		return "", nil
	}
	path, err := config.ResolvePath(configFlag)
	if err != nil {
		return "", err
	}
	cfg, issues := flowconfig.Load(path)
	if blocking := blockingValidationIssues(issues); len(blocking) > 0 {
		return "", config.ConfigValidationError{Issues: blocking}
	}
	return cfg.Security.APIKey, nil
}

// runEditor serves the config API (GET /schema, POST /validate) for a config UI.
func runEditor(args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("editor", flag.ContinueOnError)
	flags.SetOutput(stderr)
	addr := flags.String("addr", ":8090", "listen address for the config API")
	configFlag := flags.String("config", "", "path to Hookway YAML config (for API-key auth)")
	if err := flags.Parse(args); err != nil {
		return 2
	}
	apiKey, err := editorAPIKey(*configFlag)
	if err != nil {
		writef(stderr, "load config for editor auth: %v\n", err)
		return 1
	}
	authState := "disabled"
	if apiKey != "" {
		authState = "enabled"
	}
	srv := editor.NewServer(*addr, apiKey)
	writef(stdout, "hookway config API listening on %s (GET /schema, POST /validate); authentication: %s\n", *addr, authState)
	// The bundle serves the project overview at / and the config builder at
	// /builder, so point at the builder rather than leaving it to be found.
	writef(stdout, "overview: http://localhost%s/  builder: http://localhost%s/builder\n", hostPort(*addr), hostPort(*addr))
	if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		writef(stderr, "editor server stopped: %v\n", err)
		return 1
	}
	return 0
}

// hostPort renders a listen address as the ":port" suffix of a localhost URL,
// so ":8090" and "0.0.0.0:8090" both print as localhost:8090. An address it
// cannot split is returned unchanged.
func hostPort(addr string) string {
	if _, port, err := net.SplitHostPort(addr); err == nil && port != "" {
		return ":" + port
	}
	return addr
}

// runHealthcheck returns success when the target endpoint responds with any 2xx status.
func runHealthcheck(args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("healthcheck", flag.ContinueOnError)
	flags.SetOutput(stderr)
	url := flags.String("url", "http://127.0.0.1:8080/healthz", "health endpoint URL")
	timeout := flags.Duration("timeout", 5*time.Second, "health request timeout")
	if err := flags.Parse(args); err != nil {
		return 2
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

// runServe loads config, connects the message queue publisher, and serves until shutdown.
func runServe(args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("hookway", flag.ContinueOnError)
	flags.SetOutput(stderr)
	configFlag := flags.String("config", "", "path to Hookway YAML config")
	versionFlag := flags.Bool("version", false, "print version and exit")
	if err := flags.Parse(args); err != nil {
		return 2
	}
	if *versionFlag {
		writef(stdout, "hookway %s (%s)\n", version, commit)
		return 0
	}

	// levelVar defaults to slog.LevelInfo (its zero value) until cfg loads, so
	// the config-path/config-load failure logs below are never suppressed;
	// it's then set from cfg.Logging.Level once config is available. Using a
	// *slog.LevelVar (rather than baking a fixed Level into HandlerOptions)
	// keeps the level adjustable after construction -- e.g. by a future admin
	// endpoint -- without rebuilding the logger/handler.
	levelVar := new(slog.LevelVar)
	logger := slog.New(slog.NewJSONHandler(stdout, &slog.HandlerOptions{Level: levelVar}))
	logger.Info("hookway starting", "version", version, "commit", commit)
	appMetrics := metrics.New()
	appMetrics.SetBuildInfo(version, commit)
	path, err := config.ResolvePath(*configFlag)
	if err != nil {
		appMetrics.ConfigLoadFailure("path")
		logger.Error("config path required", "error", err)
		return 1
	}
	flowCfg, issues := flowconfig.Load(path)
	issues = append(issues, flowconfig.Lint(flowCfg)...)
	issues = append(issues, flowconfig.StandaloneIssues(flowCfg)...)
	if blocking := blockingValidationIssues(issues); len(blocking) > 0 {
		appMetrics.ConfigLoadFailure("load")
		logger.Error("config load failed", "error", config.ConfigValidationError{Issues: blocking})
		return 1
	}
	for _, issue := range flowconfig.Lint(flowCfg) {
		logger.Warn("config lint", "field", issue.Field, "message", issue.Message)
	}
	cfg := flowCfg.ServingConfig()
	level, err := resolveLogLevel(cfg.Logging.Level)
	if err != nil {
		logger.Error("invalid log level", "error", err)
		return 1
	}
	levelVar.Set(level)
	if cfg.Metrics.RuntimeMetricsEnabled() {
		appMetrics.EnableRuntimeCollectors()
	}
	for id, destination := range cfg.Destinations {
		if destination.RabbitMQ.TLS.InsecureSkipVerify {
			logger.Warn("rabbitmq TLS certificate verification is disabled (destinations.<id>.rabbitmq.tls.insecure_skip_verify=true); do not use this in production", "destination_id", id)
		}
	}
	publisher, err := queue.NewFactory(queue.FactoryOptions{Metrics: appMetrics, Logger: logger}).Create(context.Background(), cfg)
	if err != nil {
		logger.Error("message queue connection failed", "error", err)
		return 1
	}

	flowApp, err := httpapi.NewFlowServer(flowCfg, publisher, logger, appMetrics)
	if err != nil {
		logger.Error("flow server failed", "error", err)
		_ = publisher.Close()
		return 1
	}
	app := flowApp

	var replayStore *replaystore.Store
	if cfg.Replay.Enabled {
		replayStore, err = replaystore.New(context.Background(), cfg.Replay, logger)
		if err != nil {
			logger.Error("replay store connection failed", "error", err)
			_ = app.Close()
			_ = publisher.Close()
			return 1
		}
		app.SetReplayCapturer(replayStore)
		logger.Info("replay capture enabled")
	}

	probeCtx, cancelProbe := context.WithCancel(context.Background())
	proberDone := app.StartReadinessProbe(probeCtx)
	stopProbe := func() {
		cancelProbe()
		wait := cfg.Server.ReadinessTimeout + 2*time.Second
		select {
		case <-proberDone:
		case <-time.After(wait):
			logger.Warn("readiness prober did not stop within timeout")
		}
	}

	srv := app.HTTPServer()
	errs := make(chan error, 3)
	go func() {
		logger.Info("hookway listening", "addr", cfg.Server.Addr)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			logger.Error("server stopped", "error", err)
			errs <- err
		}
	}()

	metricsSrv := app.MetricsServer()
	if metricsSrv != nil {
		go func() {
			logger.Info("hookway metrics listening", "addr", metricsSrv.Addr, "path", cfg.Metrics.Path)
			if err := metricsSrv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
				logger.Error("metrics server stopped", "error", err)
				errs <- err
			}
		}()
	}

	var replaySrv *http.Server
	var stopCleanup func()
	if replayStore != nil {
		cleanupCtx, cancelCleanup := context.WithCancel(context.Background())
		cleanupDone := replayStore.StartCleanup(cleanupCtx, cfg.Replay.TTL, cfg.Replay.MaxEvents, cfg.Replay.CleanupInterval, appMetrics)
		stopCleanup = func() {
			cancelCleanup()
			select {
			case <-cleanupDone:
			case <-time.After(replayCleanupShutdownWait):
				logger.Warn("replay cleanup did not stop within timeout")
			}
		}

		replaySrv = replayapi.NewServer(cfg.Replay.Addr, cfg.Security.APIKey, replayStore, publisher, nil, appMetrics, logger)
		go func() {
			logger.Info("hookway replay API listening", "addr", cfg.Replay.Addr)
			if err := replaySrv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
				logger.Error("replay API server stopped", "error", err)
				errs <- err
			}
		}()
	}

	signals := make(chan os.Signal, 1)
	signal.Notify(signals, syscall.SIGINT, syscall.SIGTERM)
	closers := []io.Closer{app, publisher}
	if replayStore != nil {
		closers = []io.Closer{replayStore, app, publisher}
	}
	res := servingResources{
		logger:  logger,
		signals: signals,
		servers: []*http.Server{srv, metricsSrv, replaySrv},
		after:   []func(){stopCleanup, stopProbe},
		closers: closers,
	}

	var waitErr error
	select {
	case sig := <-signals:
		logger.Info("shutdown signal received", "signal", sig.String())
	case waitErr = <-errs:
		logger.Error("server stopped", "error", waitErr)
	}
	timeout := cfg.Server.ShutdownTimeout
	if timeout <= 0 {
		timeout = 10 * time.Second
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	if err := errors.Join(waitErr, res.drain(ctx)); err != nil {
		logger.Error("graceful shutdown failed", "error", err)
		return 1
	}
	return 0
}

func resolveLogLevel(cfgLevel string) (slog.Level, error) {
	if env := strings.TrimSpace(os.Getenv("HOOKWAY_LOG_LEVEL")); env != "" {
		return hooklogging.ParseLevel(env)
	}
	return hooklogging.ParseLevel(cfgLevel)
}

func writef(w io.Writer, format string, args ...any) {
	_, _ = fmt.Fprintf(w, format, args...)
}

func writeln(w io.Writer, args ...any) {
	_, _ = fmt.Fprintln(w, args...)
}
