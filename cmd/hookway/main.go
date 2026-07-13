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
	"github.com/jasongiroux/hookway/internal/metrics"
	"github.com/jasongiroux/hookway/internal/queue"
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
	return runServe(args, stdout, stderr)
}

// runValidate checks config syntax and semantic validation without opening network connections.
func runValidate(args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("validate", flag.ContinueOnError)
	flags.SetOutput(stderr)
	configFlag := flags.String("config", "", "path to Hookway YAML config")
	jsonOut := flags.Bool("json", false, "emit validation results as JSON")
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
		resp := configapi.Validate(data)
		enc := json.NewEncoder(stdout)
		enc.SetIndent("", "  ")
		if err := enc.Encode(resp); err != nil {
			fmt.Fprintf(stderr, "encode json: %v\n", err)
			return 1
		}
		if len(resp.Issues) > 0 {
			return 1
		}
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
	if err := flags.Parse(args); err != nil {
		return 2
	}

	logger := slog.New(slog.NewJSONHandler(stdout, nil))
	appMetrics := metrics.New()
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
	srv := app.HTTPServer()
	errs := make(chan error, 1)
	go func() {
		logger.Info("hookway listening", "addr", cfg.Server.Addr)
		errs <- srv.ListenAndServe()
	}()

	metricsSrv := app.MetricsServer()
	if metricsSrv != nil {
		go func() {
			logger.Info("hookway metrics listening", "addr", metricsSrv.Addr, "path", cfg.Metrics.Path)
			if err := metricsSrv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
				logger.Error("metrics server stopped", "error", err)
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
