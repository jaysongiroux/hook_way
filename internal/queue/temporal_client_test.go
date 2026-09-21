package queue

import (
	"errors"
	"fmt"
	"testing"

	enumspb "go.temporal.io/api/enums/v1"
	"go.temporal.io/api/serviceerror"

	"github.com/jasongiroux/hookway/internal/config"
)

func TestBuildTemporalTLSConfig_DisabledReturnsNil(t *testing.T) {
	cfg, err := buildTemporalTLSConfig(config.TemporalTLSConfig{Enabled: false, ServerName: "ignored"})
	if err != nil || cfg != nil {
		t.Fatalf("expected nil config for disabled TLS, got %#v err=%v", cfg, err)
	}
}

func TestBuildTemporalTLSConfig_ServerNameAndInsecure(t *testing.T) {
	cfg, err := buildTemporalTLSConfig(config.TemporalTLSConfig{Enabled: true, ServerName: "temporal.internal", InsecureSkipVerify: true})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.ServerName != "temporal.internal" || !cfg.InsecureSkipVerify {
		t.Fatalf("bad tls config: %#v", cfg)
	}
}

func TestBuildTemporalTLSConfig_MissingCAFile(t *testing.T) {
	if _, err := buildTemporalTLSConfig(config.TemporalTLSConfig{Enabled: true, CACert: "/nonexistent/ca.pem"}); err == nil {
		t.Fatal("expected error reading missing ca_cert")
	}
}

func TestBuildTemporalTLSConfig_PartialClientCert(t *testing.T) {
	if _, err := buildTemporalTLSConfig(config.TemporalTLSConfig{Enabled: true, ClientCert: "only-cert.pem"}); err == nil {
		t.Fatal("expected error when client_key missing")
	}
}

func TestMapWorkflowIDReusePolicy(t *testing.T) {
	cases := map[string]enumspb.WorkflowIdReusePolicy{
		"":                            enumspb.WORKFLOW_ID_REUSE_POLICY_UNSPECIFIED,
		"allow_duplicate":             enumspb.WORKFLOW_ID_REUSE_POLICY_ALLOW_DUPLICATE,
		"allow_duplicate_failed_only": enumspb.WORKFLOW_ID_REUSE_POLICY_ALLOW_DUPLICATE_FAILED_ONLY,
		"reject_duplicate":            enumspb.WORKFLOW_ID_REUSE_POLICY_REJECT_DUPLICATE,
		//nolint:staticcheck // SA1019: config still accepts terminate_if_running
		"terminate_if_running": enumspb.WORKFLOW_ID_REUSE_POLICY_TERMINATE_IF_RUNNING,
	}
	for in, want := range cases {
		got, err := mapWorkflowIDReusePolicy(in)
		if err != nil || got != want {
			t.Fatalf("%q: got %v err=%v want %v", in, got, err, want)
		}
	}
	if _, err := mapWorkflowIDReusePolicy("bogus"); err == nil {
		t.Fatal("expected error for unknown policy")
	}
}

func TestMapTemporalStartError(t *testing.T) {
	already := &serviceerror.WorkflowExecutionAlreadyStarted{Message: "already", RunId: "r1"}
	if got := mapTemporalStartError(fmt.Errorf("wrapped: %w", already)); !errors.Is(got, errTemporalAlreadyStarted) {
		t.Fatalf("expected errTemporalAlreadyStarted, got %v", got)
	}
	other := errors.New("boom")
	if got := mapTemporalStartError(other); !errors.Is(got, other) {
		t.Fatalf("expected passthrough, got %v", got)
	}
	if got := mapTemporalStartError(nil); got != nil {
		t.Fatalf("expected nil, got %v", got)
	}
}
