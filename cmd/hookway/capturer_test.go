package main

import (
	"log/slog"
	"testing"

	"github.com/jaysongiroux/hook_way/internal/config"
	"github.com/jaysongiroux/hook_way/internal/flowconfig"
	"github.com/jaysongiroux/hook_way/internal/metrics"
)

func TestNewCapturerFollowsTheReplayBlock(t *testing.T) {
	logger := slog.New(slog.DiscardHandler)
	off, err := newCapturer(flowconfig.Config{}, metrics.New(), logger)
	if err != nil || off != nil {
		t.Fatalf("no replay block: %v, %v", off, err)
	}
	cfg := flowconfig.Config{Replay: config.ReplayConfig{Configured: true, URL: "http://127.0.0.1:1", CaptureToken: "t",
		MaxRecordBytes: config.DefaultReplayMaxRecordBytes}}
	on, err := newCapturer(cfg, metrics.New(), logger)
	if err != nil || on == nil {
		t.Fatalf("replay block: %v, %v", on, err)
	}
	_ = on.Close()
	cfg.Replay.TLS.CACert = "/nonexistent/ca.pem"
	if _, err := newCapturer(cfg, metrics.New(), logger); err == nil {
		t.Fatal("bad replay tls accepted")
	}
}
