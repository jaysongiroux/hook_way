package httpapi

import (
	"context"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	ddbtypes "github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/modules/localstack"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"

	"github.com/jaysongiroux/hook_way/internal/awsconf"
	"github.com/jaysongiroux/hook_way/internal/flowconfig"
	"github.com/jaysongiroux/hook_way/internal/testenv"
)

func assertStandaloneDatabaseLookup(t *testing.T, providerYAML string) {
	t.Helper()
	src := strings.Replace(standaloneLookupYAML("https://unused.example", ""), "timeout: 100ms", "timeout: 2s", 1)
	start := strings.Index(src, "    http:")
	end := strings.Index(src, "flows:")
	src = src[:start] + providerYAML + "\n" + src[end:]
	cfg, issues := flowconfig.LoadBytes([]byte(src), "integration", true)
	if len(issues) > 0 {
		t.Fatal(issues)
	}
	srv, e := NewFlowServer(cfg, &fakePublisher{}, nil, nil)
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { _ = srv.Close() })
	for _, tc := range []struct {
		key    string
		status int
	}{{"known", 200}, {"absent", 400}, {"known' OR '1'='1", 400}} {
		if w := lookupRequest(t, srv, `{"id":"`+tc.key+`"}`); w.Code != tc.status {
			t.Fatalf("key %q: status %d: %s", tc.key, w.Code, w.Body.String())
		}
	}
}

func TestPostgresStandaloneLookup(t *testing.T) {
	if testing.Short() {
		t.Skip("requires Docker")
	}
	ctx := t.Context()
	container, e := tcpostgres.Run(ctx, "postgres:16-alpine", tcpostgres.WithDatabase("routes"), tcpostgres.WithUsername("hookway"), tcpostgres.WithPassword("hookway"), tcpostgres.BasicWaitStrategies())
	testenv.RequireContainer(t, e, "postgres")
	t.Cleanup(func() { _ = container.Terminate(context.Background()) })
	dsn, e := container.ConnectionString(ctx, "sslmode=disable")
	if e != nil {
		t.Fatal(e)
	}
	pool, e := pgxpool.New(ctx, dsn)
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(pool.Close)
	if _, e := pool.Exec(ctx, `CREATE TABLE routes (id text PRIMARY KEY, tenant text NOT NULL); INSERT INTO routes VALUES ('known','sandbox')`); e != nil {
		t.Fatal(e)
	}
	t.Setenv("LOOKUP_INTEGRATION_DSN", dsn)
	assertStandaloneDatabaseLookup(t, `    postgres:
      url: ${LOOKUP_INTEGRATION_DSN}
      query: SELECT tenant FROM routes WHERE id = {key}`)
}

func TestAWSStandaloneLookup(t *testing.T) {
	if testing.Short() {
		t.Skip("requires Docker")
	}
	ctx := t.Context()
	container, e := localstack.Run(ctx, "localstack/localstack:3.4", testcontainers.WithEnv(map[string]string{"SERVICES": "dynamodb"}))
	testenv.RequireContainer(t, e, "localstack")
	t.Cleanup(func() { _ = container.Terminate(context.Background()) })
	host, e := container.Host(ctx)
	if e != nil {
		t.Fatal(e)
	}
	port, e := container.MappedPort(ctx, "4566/tcp")
	if e != nil {
		t.Fatal(e)
	}
	endpoint := "http://" + host + ":" + port.Port()
	cfg, e := awsconf.Load(ctx, "us-east-1", endpoint, "test", "test")
	if e != nil {
		t.Fatal(e)
	}
	client := dynamodb.NewFromConfig(cfg)
	_, e = client.CreateTable(ctx, &dynamodb.CreateTableInput{TableName: aws.String("routes"), BillingMode: ddbtypes.BillingModePayPerRequest, AttributeDefinitions: []ddbtypes.AttributeDefinition{{AttributeName: aws.String("id"), AttributeType: ddbtypes.ScalarAttributeTypeS}}, KeySchema: []ddbtypes.KeySchemaElement{{AttributeName: aws.String("id"), KeyType: ddbtypes.KeyTypeHash}}})
	if e != nil {
		t.Fatal(e)
	}
	_, e = client.PutItem(ctx, &dynamodb.PutItemInput{TableName: aws.String("routes"), Item: map[string]ddbtypes.AttributeValue{"id": &ddbtypes.AttributeValueMemberS{Value: "known"}, "tenant": &ddbtypes.AttributeValueMemberS{Value: "sandbox"}}})
	if e != nil {
		t.Fatal(e)
	}
	t.Setenv("AWS_ACCESS_KEY_ID", "test")
	t.Setenv("AWS_SECRET_ACCESS_KEY", "test")
	t.Setenv("AWS_EC2_METADATA_DISABLED", "true")
	assertStandaloneDatabaseLookup(t, `    dynamodb:
      table: routes
      region: us-east-1
      endpoint: `+endpoint+`
      value_attribute: tenant`)
}
