package awsconf

import (
	"context"
	"testing"
)

// isolate pins the credential environment so the default chain resolves to
// known values instead of whatever the host or CI runner happens to have,
// and keeps LoadDefaultConfig from reaching for EC2 instance metadata.
func isolate(t *testing.T) {
	t.Helper()
	t.Setenv("AWS_EC2_METADATA_DISABLED", "true")
	t.Setenv("AWS_CONFIG_FILE", "/dev/null")
	t.Setenv("AWS_SHARED_CREDENTIALS_FILE", "/dev/null")
	t.Setenv("AWS_PROFILE", "")
	t.Setenv("AWS_ACCESS_KEY_ID", "env-key")
	t.Setenv("AWS_SECRET_ACCESS_KEY", "env-secret")
	t.Setenv("AWS_SESSION_TOKEN", "")
	t.Setenv("AWS_REGION", "us-east-1")
}

func TestLoadUsesStaticCredentialsWhenBothFieldsAreSet(t *testing.T) {
	isolate(t)
	cfg, err := Load(context.Background(), "eu-west-2", "", "static-key", "static-secret")
	if err != nil {
		t.Fatalf("Load() = %v", err)
	}
	creds, err := cfg.Credentials.Retrieve(context.Background())
	if err != nil {
		t.Fatalf("Retrieve() = %v", err)
	}
	if creds.AccessKeyID != "static-key" || creds.SecretAccessKey != "static-secret" {
		t.Errorf("credentials = %q/%q, want the configured static pair", creds.AccessKeyID, creds.SecretAccessKey)
	}
	if cfg.Region != "eu-west-2" {
		t.Errorf("Region = %q, want eu-west-2", cfg.Region)
	}
}

// TestLoadIgnoresPartialStaticCredentials is the security-relevant case: a
// config with only one of the two key fields must fall through to the default
// credential chain rather than constructing a half-populated static provider,
// which would fail at request time with a confusing signing error.
func TestLoadIgnoresPartialStaticCredentials(t *testing.T) {
	for _, tc := range []struct{ name, key, secret string }{
		{"only access key id", "static-key", ""},
		{"only secret", "", "static-secret"},
		{"neither", "", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			isolate(t)
			cfg, err := Load(context.Background(), "", "", tc.key, tc.secret)
			if err != nil {
				t.Fatalf("Load() = %v", err)
			}
			creds, err := cfg.Credentials.Retrieve(context.Background())
			if err != nil {
				t.Fatalf("Retrieve() = %v", err)
			}
			if creds.AccessKeyID != "env-key" || creds.SecretAccessKey != "env-secret" {
				t.Errorf("credentials = %q/%q, want the default chain's env-key/env-secret: "+
					"a partial static pair must never be used", creds.AccessKeyID, creds.SecretAccessKey)
			}
		})
	}
}

func TestLoadAppliesEndpointOverride(t *testing.T) {
	isolate(t)
	cfg, err := Load(context.Background(), "us-east-1", "http://localhost:4566", "k", "s")
	if err != nil {
		t.Fatalf("Load() = %v", err)
	}
	if cfg.BaseEndpoint == nil {
		t.Fatal("BaseEndpoint = nil; a non-empty endpoint must override the service endpoint (LocalStack)")
	}
	if *cfg.BaseEndpoint != "http://localhost:4566" {
		t.Errorf("BaseEndpoint = %q, want http://localhost:4566", *cfg.BaseEndpoint)
	}
}

func TestLoadWithoutEndpointLeavesDefaultResolution(t *testing.T) {
	isolate(t)
	cfg, err := Load(context.Background(), "us-east-1", "", "k", "s")
	if err != nil {
		t.Fatalf("Load() = %v", err)
	}
	if cfg.BaseEndpoint != nil {
		t.Errorf("BaseEndpoint = %q, want nil so the SDK resolves the real AWS endpoint", *cfg.BaseEndpoint)
	}
}
