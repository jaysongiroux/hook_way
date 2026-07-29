package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/jasongiroux/hookway/internal/config"
	"github.com/jasongiroux/hookway/internal/configapi"
	"github.com/jasongiroux/hookway/internal/editor"
	"github.com/jasongiroux/hookway/internal/httpapi"
	hooklogging "github.com/jasongiroux/hookway/internal/logging"
	"github.com/jasongiroux/hookway/internal/metrics"
	"github.com/jasongiroux/hookway/internal/queue"
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

// run dispatches subcommands and returns a process exit code.
func run(args []string, stdout, stderr io.Writer) int {
	if len(args) > 0 && args[0] == "validate" {
		return runValidate(args[1:], stdout, stderr)
	}
	if len(args) > 0 && args[0] == "healthcheck" {
		return runHealthcheck(args[1:], stdout, stderr)
	}
	if len(args) > 0 && args[0] == "schema" {
		return runSchema(stdout, stderr)
	}
	if len(args) > 0 && args[0] == "editor" {
		return runEditor(args[1:], stdout, stderr)
	}
	if len(args) > 0 && args[0] == "version" {
		fmt.Fprintf(stdout, "hookway %s (%s)\n", version, commit)
		return 0
	}
	return runServe(args, stdout, stderr)
}

// runValidate checks config syntax and semantic validation without opening network connections.
//
// Env-expansion behavior across the two output modes and --no-env:
//   - plain validate (no flags): config.NewService().Validate → config.Load,
//     which expands ${VAR} strictly and HARD-fails (nonzero exit, no output
//     other than the error) on any referenced-but-unset variable. Unchanged
//     by this function's other additions.
//   - validate --json: configapi.ValidateWithEnv(data, true) also expands
//     ${VAR}, but a missing variable is reported as a "config.env.<NAME>"
//     issue at severity "warning" (exit 0 if it's the only issue) rather than
//     aborting — CI commonly validates without production secrets set.
//   - --no-env (either mode): skips expansion entirely, so ${VAR} is left as
//     an opaque literal and only structural/syntax issues can appear. Plain
//     --no-env uses config.NewService().ValidateBytes (the same non-expanding
//     path ValidateWithEnv(_, false) delegates to) instead of Load.
func runValidate(args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("validate", flag.ContinueOnError)
	flags.SetOutput(stderr)
	configFlag := flags.String("config", "", "path to Hookway YAML config")
	jsonOut := flags.Bool("json", false, "emit validation results as JSON")
	noEnv := flags.Bool("no-env", false, "skip ${VAR} environment expansion; check syntax and structure only")
	if err := flags.Parse(args); err != nil {
		return 2
	}
	path, err := config.ResolvePath(*configFlag)
	if err != nil {
		fmt.Fprintf(stderr, "config path required: %v\n", err)
		return 1
	}

	if *jsonOut {
		data, err := os.ReadFile(path)
		if err != nil {
			fmt.Fprintf(stderr, "read config: %v\n", err)
			return 1
		}
		resp := configapi.ValidateWithEnv(data, !*noEnv)
		enc := json.NewEncoder(stdout)
		enc.SetIndent("", "  ")
		if err := enc.Encode(resp); err != nil {
			fmt.Fprintf(stderr, "encode json: %v\n", err)
			return 1
		}
		for _, iss := range resp.Issues {
			if iss.Severity != "warning" {
				return 1
			}
		}
		return 0
	}

	if *noEnv {
		data, err := os.ReadFile(path)
		if err != nil {
			fmt.Fprintf(stderr, "read config: %v\n", err)
			return 1
		}
		if issues := config.NewService().ValidateBytes(data); len(issues) > 0 {
			fmt.Fprintln(stderr, config.ConfigValidationError{Issues: issues})
			return 1
		}
		fmt.Fprintf(stdout, "config valid (no-env): %s\n", path)
		return 0
	}

	if err := config.NewService().Validate(path); err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	fmt.Fprintf(stdout, "config valid: %s\n", path)
	return 0
}

// runSchema prints the JSON Schema describing the Hookway config to stdout.
func runSchema(stdout, stderr io.Writer) int {
	data, err := configapi.Schema()
	if err != nil {
		fmt.Fprintf(stderr, "generate schema: %v\n", err)
		return 1
	}
	fmt.Fprintln(stdout, string(data))
	return 0
}

// editorAPIKey resolves the config-API auth key from config. When no config is
// available (neither --config nor HOOKWAY_CONFIG), it returns "" so the editor
// runs unauthenticated. A provided-but-unloadable config is a hard error.
func editorAPIKey(configFlag string) (string, error) {
	path, err := config.ResolvePath(configFlag)
	if err != nil {
		return "", nil // no config ⇒ unauthenticated (standalone editor)
	}
	cfg, err := config.Load(path)
	if err != nil {
		return "", err
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
		fmt.Fprintf(stderr, "load config for editor auth: %v\n", err)
		return 1
	}
	authState := "disabled"
	if apiKey != "" {
		authState = "enabled"
	}
	srv := editor.NewServer(*addr, apiKey)
	fmt.Fprintf(stdout, "hookway config API listening on %s (GET /schema, POST /validate); authentication: %s\n", *addr, authState)
	if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		fmt.Fprintf(stderr, "editor server stopped: %v\n", err)
		return 1
	}
	return 0
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
	client := http.Client{Timeout: *timeout}
	resp, err := client.Get(*url)
	if err != nil {
		fmt.Fprintf(stderr, "healthcheck failed: %v\n", err)
		return 1
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		fmt.Fprintf(stderr, "healthcheck failed: status %d\n", resp.StatusCode)
		return 1
	}
	fmt.Fprintln(stdout, "healthy")
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
		fmt.Fprintf(stdout, "hookway %s (%s)\n", version, commit)
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
	cfg, err := config.Load(path)
	if err != nil {
		appMetrics.ConfigLoadFailure("load")
		logger.Error("config load failed", "error", err)
		return 1
	}
	level, err := hooklogging.ParseLevel(cfg.Logging.Level)
	if err != nil {
		// Unreachable in practice: cfg.ValidationIssues() already rejected an
		// unsupported logging.level before Load returned successfully. Kept as
		// a defensive check so a future validation/parsing mismatch fails
		// loudly instead of silently running at the wrong level.
		logger.Error("invalid logging.level", "error", err)
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
	defer publisher.Close()

	app := httpapi.NewServerWithMetrics(cfg, publisher, logger, appMetrics)

	// Start the background readiness prober before serving traffic. It caches
	// destination readiness so /ready and /readyz never trigger a live broker
	// fan-out per request; cancelling probeCtx on shutdown stops the goroutine.
	//
	// The stop-and-wait defer below is registered after publisher.Close()'s
	// defer, so on every return path (graceful shutdown or an early error
	// return) it runs first: the prober goroutine has exited -- or the
	// bounded wait has elapsed -- before publisher.Close() runs underneath
	// it, so a well-behaved in-flight probe never races Close().
	probeCtx, cancelProbe := context.WithCancel(context.Background())
	proberDone := app.StartReadinessProbe(probeCtx)
	defer func() {
		cancelProbe()
		// A well-behaved probe always returns within readiness_timeout (its
		// per-probe context deadline); the small margin gives the goroutine
		// time to observe ctx.Done() and exit. The wait stays bounded -- not
		// unbounded -- so a destination client that ignores context
		// cancellation forever cannot hang shutdown.
		wait := cfg.Server.ReadinessTimeout + 2*time.Second
		select {
		case <-proberDone:
		case <-time.After(wait):
			logger.Warn("readiness prober did not stop within timeout")
		}
	}()

	srv := app.HTTPServer()
	// Buffered for 2: the main listener and the (optional) dedicated metrics
	// listener each get their own slot, so neither goroutine can block forever
	// writing an error nobody will read after the process is already on its
	// way out via the other one.
	errs := make(chan error, 2)
	go func() {
		logger.Info("hookway listening", "addr", cfg.Server.Addr)
		errs <- srv.ListenAndServe()
	}()

	metricsSrv := app.MetricsServer()
	if metricsSrv != nil {
		go func() {
			logger.Info("hookway metrics listening", "addr", metricsSrv.Addr, "path", cfg.Metrics.Path)
			// http.ErrServerClosed only comes back from a deliberate
			// Shutdown/Close (the deferred httpapi.Shutdown(ctx, metricsSrv)
			// below, which only runs once runServe is already on its way out).
			// That is a clean stop, not a failure, so it must NOT be pushed
			// onto errs -- doing so would race a spurious "fatal" error right
			// after the real shutdown signal was already handled. Any
			// other error (most commonly: the metrics address is already in
			// use) is a genuine startup/runtime failure and is routed onto the
			// same errs channel the main listener uses, so it drives the same
			// shutdown-and-exit path as a failed main listener.
			if err := metricsSrv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
				logger.Error("metrics server stopped", "error", err)
				errs <- err
			}
		}()
		defer func() {
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			_ = httpapi.Shutdown(ctx, metricsSrv)
		}()
	}

	signals := make(chan os.Signal, 1)
	signal.Notify(signals, syscall.SIGINT, syscall.SIGTERM)
	select {
	case sig := <-signals:
		logger.Info("shutdown signal received", "signal", sig.String())
	case err := <-errs:
		logger.Error("server stopped", "error", err)
		return 1
	}
	timeout := cfg.Server.ShutdownTimeout
	if timeout <= 0 {
		timeout = 10 * time.Second
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	if err := httpapi.Shutdown(ctx, srv); err != nil {
		logger.Error("graceful shutdown failed", "error", err)
		return 1
	}
	return 0
}
