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
	"strings"
	"syscall"
	"time"

	"github.com/jaysongiroux/hook_way/internal/capture"
	"github.com/jaysongiroux/hook_way/internal/cli"
	"github.com/jaysongiroux/hook_way/internal/config"
	"github.com/jaysongiroux/hook_way/internal/configapi"
	"github.com/jaysongiroux/hook_way/internal/control"
	"github.com/jaysongiroux/hook_way/internal/editor"
	"github.com/jaysongiroux/hook_way/internal/eventdelivery"
	"github.com/jaysongiroux/hook_way/internal/flowconfig"
	"github.com/jaysongiroux/hook_way/internal/httpapi"
	hooklogging "github.com/jaysongiroux/hook_way/internal/logging"
	"github.com/jaysongiroux/hook_way/internal/metrics"
	"github.com/jaysongiroux/hook_way/internal/queue"
)

// version and commit identify the build. They default to "dev"/"none" for a
// plain `go build`/`go run` and are overridden at build time via
// `-ldflags "-X main.version=... -X main.commit=..."` (see the Makefile's
// build target and the Dockerfile).
var (
	version = "dev"
	commit  = "none"
)

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

// run dispatches subcommands and returns a process exit code. With no
// command, or only flags, it serves.
func run(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		return runServe(args, stdout, stderr)
	}
	switch args[0] {
	case "-h", "-help", "--help":
		program.WriteHelp(stdout)
		return cli.ExitOK
	case "help":
		return runHelp(args[1:], stdout, stderr)
	case "serve":
		return runServe(args[1:], stdout, stderr)
	case "import-openapi":
		return runImportOpenAPI(args[1:], stdout, stderr)
	case "validate":
		return runValidate(args[1:], stdout, stderr)
	case "healthcheck":
		return runHealthcheck(args[1:], stdout, stderr)
	case "schema":
		return runSchema(args[1:], stdout, stderr)
	case "editor":
		return runEditor(args[1:], stdout, stderr)
	case "event-store":
		return runEventStore(args[1:], stdout, stderr)
	case "state-store":
		return runStateStore(args[1:], stdout, stderr)
	case "version":
		if code, ok := program.Parse("version", flag.NewFlagSet("version", flag.ContinueOnError), args[1:], stdout, stderr); !ok {
			return code
		}
		writef(stdout, "hookway %s (%s)\n", version, commit)
		return cli.ExitOK
	}
	if strings.HasPrefix(args[0], "-") {
		return runServe(args, stdout, stderr)
	}
	return program.UnknownCommand(stderr, args[0])
}

// runHelp prints the command list, or one command's help page.
func runHelp(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		program.WriteHelp(stdout)
		return cli.ExitOK
	}
	if _, ok := program.Lookup(args[0]); !ok || len(args) > 1 {
		return program.UnknownCommand(stderr, strings.Join(args, " "))
	}
	return run([]string{args[0], "-h"}, stdout, stderr)
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
	configFlag := flags.String("config", "", "the config `FILE` to check (default $HOOKWAY_CONFIG)")
	jsonOut := flags.Bool("json", false, `print {"issues": [...]} on stdout; an unset ${VAR} is a warning`)
	noEnv := flags.Bool("no-env", false, "leave ${VAR} unexpanded; check syntax and structure only")
	strict := flags.Bool("strict", false, "fail on lint warnings too")
	if code, ok := program.Parse("validate", flags, args, stdout, stderr); !ok {
		return code
	}
	path, err := config.ResolvePath(*configFlag)
	if err != nil {
		writef(stderr, "config path required: %v\n", err)
		return 1
	}

	if *jsonOut {
		resp := configapi.ValidateFileWithEnv(path, !*noEnv)
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

	doc, issues := flowconfig.ResolveFile(path)
	if !*noEnv && len(issues) == 1 && issues[0].Field == "config" {
		cfg, loaded := flowconfig.LoadFile(path, true)
		issues = append(loaded, flowconfig.Lint(cfg)...)
		issues = append(issues, flowconfig.StandaloneIssues(cfg)...)
		issues = (flowconfig.ResolvedDocument{Source: path}).Attribute(issues)
	} else if len(issues) == 0 {
		cfg, decoded := flowconfig.LoadBytes(doc.Data, path, !*noEnv)
		issues = append(decoded, flowconfig.Lint(cfg)...)
		issues = append(issues, flowconfig.StandaloneIssues(cfg)...)
		issues = doc.Attribute(issues)
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
		writef(stderr, "warning: %s: %s", issue.Field, issue.Message)
		if issue.Suggestion != "" {
			writef(stderr, " Suggestion: %s", issue.Suggestion)
		}
		writef(stderr, "\n")
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
	nodes := flags.Bool("nodes", false, "print each node type's ports and option schema")
	destinations := flags.Bool("destinations", false, "print each destination type's option schema")
	connections := flags.Bool("connections", false, "print each connection type's option schema")
	if code, ok := program.Parse("schema", flags, args, stdout, stderr); !ok {
		return code
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
	if *connections {
		data, err := json.MarshalIndent(config.ConnectionTypeSchemas(), "", "  ")
		if err != nil {
			writef(stderr, "generate connection schema: %v\n", err)
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
	addr := flags.String("addr", ":8090", "the `ADDR` to listen on")
	configFlag := flags.String("config", "", "config `FILE` whose security.api_key protects the API (default $HOOKWAY_CONFIG)")
	if code, ok := program.Parse("editor", flags, args, stdout, stderr); !ok {
		return code
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

// newCapturer builds failure capture when the replay block is configured.
func newCapturer(cfg flowconfig.Config, m *metrics.Metrics, logger *slog.Logger) (*capture.Capturer, error) {
	if !cfg.Replay.Configured {
		return nil, nil
	}
	host, _ := os.Hostname()
	return capture.New(capture.Options{Config: cfg, Metrics: m, Logger: logger, CoreVersion: version, CoreInstance: host})
}

// newControlServer builds the control listener replay redrives through, when
// the replay block is configured.
func newControlServer(cfg flowconfig.Config, redriver control.Redriver, m *metrics.Metrics, logger *slog.Logger, events ...control.EventOperator) *control.Server {
	if !cfg.Replay.Configured {
		return nil
	}
	flows := make([]string, 0, len(cfg.Flows))
	for name := range cfg.Flows {
		flows = append(flows, name)
	}
	host, _ := os.Hostname()
	ctl := cfg.Replay.Control
	var eventOperator control.EventOperator
	if len(events) > 0 {
		eventOperator = events[0]
	}
	return control.New(control.Options{Events: eventOperator,
		Addr: ctl.Addr, Token: ctl.Token, MaxConcurrent: ctl.MaxConcurrent, MaxRecordBytes: cfg.Replay.MaxRecordBytes,
		Flows: flows, Redriver: redriver, Metrics: m, Logger: logger, CoreVersion: version, CoreInstance: host,
	})
}

// runServe loads config, connects the destinations, and serves until shutdown.
func runServe(args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("serve", flag.ContinueOnError)
	configFlag := flags.String("config", "", "the config `FILE` (default $HOOKWAY_CONFIG)")
	versionFlag := flags.Bool("version", false, "print the version and exit")
	if code, ok := program.Parse("serve", flags, args, stdout, stderr); !ok {
		return code
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
	appMetrics := metrics.New()
	logger := slog.New(hooklogging.StormHandler(slog.NewJSONHandler(stdout, &slog.HandlerOptions{Level: levelVar}), appMetrics.LogSuppressed))
	logger.Info("hookway starting", "version", version, "commit", commit)
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
		logger.Warn("config lint", "field", issue.Field, "message", issue.Message, "suggestion", issue.Suggestion)
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
	capturer, err := newCapturer(flowCfg, appMetrics, logger)
	if err != nil {
		logger.Error("replay capture setup failed", "error", err)
		return 1
	}
	closeCapturer := func() {
		if capturer != nil {
			_ = capturer.Close()
		}
	}
	events, err := eventdelivery.New(context.Background(), flowCfg, appMetrics)
	if err != nil {
		logger.Error("event delivery initialization failed", "error", err)
		closeCapturer()
		return 1
	}
	if events != nil {
		defer func() { _ = events.Close() }()
	}
	factoryOpts := queue.FactoryOptions{Metrics: appMetrics, Logger: logger}
	if events != nil {
		factoryOpts.MCPEventsConnector = func(_ context.Context, _ string, c config.MCPEventsConfig) (queue.Publisher, error) {
			return &eventdelivery.Publisher{Runtime: events, Endpoint: c.Endpoint}, nil
		}
	}
	if capturer != nil {
		factoryOpts.OnTerminalFailure = capturer.OnTerminalFailure
	}
	publisher, err := queue.NewFactory(factoryOpts).Create(context.Background(), cfg)
	if err != nil {
		logger.Error("message queue connection failed", "error", err)
		closeCapturer()
		return 1
	}

	var eventRuntime flowconfig.EventRuntime
	if events != nil {
		eventRuntime = events
	}
	flowApp, err := httpapi.NewFlowServerWithEvents(flowCfg, publisher, logger, appMetrics, eventRuntime)
	if err != nil {
		logger.Error("flow server failed", "error", err)
		_ = publisher.Close()
		closeCapturer()
		return 1
	}
	if events != nil {
		events.Start()
	}
	flowApp.SetCapturer(capturer)
	app := flowApp

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

	ctl := newControlServer(flowCfg, flowApp, appMetrics, logger, events)
	if ctl != nil {
		controlHTTP := ctl.HTTPServer()
		go func() {
			logger.Info("hookway control listener", "addr", controlHTTP.Addr)
			if err := controlHTTP.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
				logger.Error("control listener stopped", "error", err)
				errs <- err
			}
		}()
	}

	signals := make(chan os.Signal, 1)
	signal.Notify(signals, syscall.SIGINT, syscall.SIGTERM)
	closers := []io.Closer{app, publisher}
	if events != nil {
		closers = append([]io.Closer{events}, closers...)
	}
	if capturer != nil {
		closers = append(closers, capturer) // after the publisher: draining engines can still capture
	}
	servers := []shutdowner{srv}
	if metricsSrv != nil {
		servers = append(servers, metricsSrv)
	}
	if ctl != nil {
		// Its Shutdown waits for in-flight redrives, so the publishers they
		// deliver through close only after those finish.
		servers = append(servers, ctl)
	}
	res := servingResources{
		logger:          logger,
		signals:         signals,
		servers:         servers,
		after:           []func(){stopProbe},
		closers:         closers,
		listenerTimeout: cfg.Server.ShutdownTimeout,
	}

	var waitErr error
	select {
	case sig := <-signals:
		logger.Info("shutdown signal received", "signal", sig.String())
	case waitErr = <-errs:
		logger.Error("server stopped", "error", waitErr)
	}
	timeout := cfg.Server.ShutdownTotalTimeout
	if timeout <= 0 {
		timeout = 60 * time.Second
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	delay := cfg.Server.ShutdownDelay
	if waitErr != nil {
		delay = 0
	} // a failed listener cannot serve during withdrawal
	if events != nil {
		events.Stop()
	}
	logger.Info("hookway withdrawing", "delay", delay, "shutdown_total_timeout", timeout)
	if err := withdrawBeforeDrain(ctx, delay, app.Withdraw); err != nil {
		logger.Error("readiness withdrawal failed", "error", err)
		return 1
	}
	app.BeginDrain()
	if ctl != nil {
		ctl.Drain()
	}
	logger.Info("hookway draining")
	if err := errors.Join(waitErr, res.drain(ctx)); err != nil {
		logger.Error("graceful shutdown failed", "error", err)
		return 1
	}
	logger.Info("hookway stopped")
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
