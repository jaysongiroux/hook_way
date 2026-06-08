package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"log/slog"
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

func run(args []string, stdout, stderr io.Writer) int {
	if len(args) > 0 && args[0] == "validate" {
		return runValidate(args[1:], stdout, stderr)
	}
	return runServe(args, stdout, stderr)
}

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
