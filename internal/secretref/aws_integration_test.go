package secretref

import (
	"bytes"
	"context"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/secretsmanager"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/modules/localstack"

	"github.com/jasongiroux/hookway/internal/awsconf"
)

// TestAWSSecretsManagerResolveAndRefresh exercises D7 against LocalStack:
// resolve a secret, rotate it, and pick up the new value on refresh without
// restarting. Named TestAWS* so it runs under make aws-integration-test.
func TestAWSSecretsManagerResolveAndRefresh(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in -short mode")
	}
	ctx := context.Background()
	var container testcontainers.Container
	func() {
		defer func() {
			if rec := recover(); rec != nil {
				t.Skipf("localstack unavailable: %v", rec)
			}
		}()
		var err error
		container, err = localstack.Run(ctx, "localstack/localstack:3.4",
			testcontainers.WithEnv(map[string]string{"SERVICES": "secretsmanager"}),
		)
		if err != nil {
			t.Skipf("localstack unavailable: %v", err)
		}
	}()
	if t.Skipped() {
		return
	}
	t.Cleanup(func() { _ = container.Terminate(ctx) })
	host, err := container.Host(ctx)
	if err != nil {
		t.Fatal(err)
	}
	port, err := container.MappedPort(ctx, "4566/tcp")
	if err != nil {
		t.Fatal(err)
	}
	endpoint := "http://" + host + ":" + port.Port()

	t.Setenv("AWS_REGION", "us-east-1")
	t.Setenv("AWS_ENDPOINT_URL", endpoint)
	t.Setenv("AWS_ACCESS_KEY_ID", "test")
	t.Setenv("AWS_SECRET_ACCESS_KEY", "test")

	awsCfg, err := awsconf.Load(ctx, "us-east-1", endpoint, "test", "test")
	if err != nil {
		t.Fatal(err)
	}
	client := secretsmanager.NewFromConfig(awsCfg)
	name := "hookway-secretref-" + t.Name()
	if _, err := client.CreateSecret(ctx, &secretsmanager.CreateSecretInput{
		Name:         aws.String(name),
		SecretString: aws.String("first"),
	}); err != nil {
		t.Fatalf("CreateSecret: %v", err)
	}

	r, err := New(Ref{Provider: "aws_secrets_manager", Name: name, Refresh: time.Hour, Overlap: time.Minute}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = r.Close() })
	got, err := r.Secrets(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || !bytes.Equal(got[0], []byte("first")) {
		t.Fatalf("initial Secrets() = %q, want first", got)
	}

	if _, err := client.PutSecretValue(ctx, &secretsmanager.PutSecretValueInput{
		SecretId:     aws.String(name),
		SecretString: aws.String("second"),
	}); err != nil {
		t.Fatalf("PutSecretValue: %v", err)
	}
	c := r.(*cached)
	c.refreshOnce(ctx)
	got, err = r.Secrets(ctx)
	if err != nil {
		t.Fatal(err)
	}
	foundNew, foundOld := false, false
	for _, s := range got {
		if bytes.Equal(s, []byte("second")) {
			foundNew = true
		}
		if bytes.Equal(s, []byte("first")) {
			foundOld = true
		}
	}
	if !foundNew || !foundOld {
		t.Fatalf("after rotation Secrets() = %q, want both first and second during overlap", got)
	}
}
