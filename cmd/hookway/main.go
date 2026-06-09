package main

import (
	"context"
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
	return runServe(args, stdout, stderr)
}

// runValidate checks config syntax and semantic validation without opening network connections.
func runValidate(args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("validate", flag.ContinueOnError)
	flags.SetOutput(stderr)
	configFlag := flags.String("config", "", "path to Hookway YAML config")
	if err := flags.Parse(args); err != nil {
		return 2
	}
	path, err := config.ResolvePath(*configFlag)
	if err != nil {
		fmt.Fprintf(stderr, "config path required: %v\n", err)
		return 1
	}
	if err := config.NewService().Validate(path); err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	fmt.Fprintf(stdout, "config valid: %s\n", path)
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
	publisher, err := queue.NewFactory(queue.FactoryOptions{Metrics: appMetrics}).Create(context.Background(), cfg)
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
