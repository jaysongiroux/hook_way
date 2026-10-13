package lookup

import (
	"context"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	ddbtypes "github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/modules/localstack"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"

	"github.com/jaysongiroux/hook_way/internal/awsconf"
	"github.com/jaysongiroux/hook_way/internal/testenv"
)

// TestAWSDynamoDBLookupAgainstRoutingTableSchema creates the ephemeral routing
// table exactly as the universal-ephemeral-proxy defines it (hash id, range
// id_type, LSI CreatedAtIndex on created_at) in LocalStack, seeds two rows for
// one id with different id_type and created_at, and checks the provider picks
// the row the id_type filter selects even though the newer row is of another
// type. Named TestAWS* so it runs under make aws-integration-test.
func TestAWSDynamoDBLookupAgainstRoutingTableSchema(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in -short mode")
	}
	ctx := context.Background()
	container, err := localstack.Run(ctx, "localstack/localstack:3.4", testcontainers.WithEnv(map[string]string{"SERVICES": "dynamodb"}))
	testenv.RequireContainer(t, err, "localstack")
	t.Cleanup(func() { _ = container.Terminate(ctx) })
	host, _ := container.Host(ctx)
	port, err := container.MappedPort(ctx, "4566/tcp")
	if err != nil {
		t.Fatal(err)
	}
	endpoint := "http://" + host + ":" + port.Port()
	awsCfg, err := awsconf.Load(ctx, "us-east-1", endpoint, "test", "test")
	if err != nil {
		t.Fatal(err)
	}
	client := dynamodb.NewFromConfig(awsCfg)
	_, err = client.CreateTable(ctx, &dynamodb.CreateTableInput{
		TableName:   aws.String("routing"),
		BillingMode: ddbtypes.BillingModePayPerRequest,
		AttributeDefinitions: []ddbtypes.AttributeDefinition{
			{AttributeName: aws.String("id"), AttributeType: ddbtypes.ScalarAttributeTypeS},
			{AttributeName: aws.String("id_type"), AttributeType: ddbtypes.ScalarAttributeTypeS},
			{AttributeName: aws.String("created_at"), AttributeType: ddbtypes.ScalarAttributeTypeN},
		},
		KeySchema: []ddbtypes.KeySchemaElement{
			{AttributeName: aws.String("id"), KeyType: ddbtypes.KeyTypeHash},
			{AttributeName: aws.String("id_type"), KeyType: ddbtypes.KeyTypeRange},
		},
		LocalSecondaryIndexes: []ddbtypes.LocalSecondaryIndex{{
			IndexName: aws.String("CreatedAtIndex"),
			KeySchema: []ddbtypes.KeySchemaElement{
				{AttributeName: aws.String("id"), KeyType: ddbtypes.KeyTypeHash},
				{AttributeName: aws.String("created_at"), KeyType: ddbtypes.KeyTypeRange},
			},
			Projection: &ddbtypes.Projection{ProjectionType: ddbtypes.ProjectionTypeAll},
		}},
	})
	if err != nil {
		t.Fatalf("CreateTable: %v", err)
	}
	put := func(id, idType, env string, createdAt int) {
		_, err := client.PutItem(ctx, &dynamodb.PutItemInput{TableName: aws.String("routing"), Item: map[string]ddbtypes.AttributeValue{
			"id":         &ddbtypes.AttributeValueMemberS{Value: id},
			"id_type":    &ddbtypes.AttributeValueMemberS{Value: idType},
			"env_name":   &ddbtypes.AttributeValueMemberS{Value: env},
			"created_at": &ddbtypes.AttributeValueMemberN{Value: aws.ToString(aws.String(itoa(createdAt)))},
		}})
		if err != nil {
			t.Fatalf("PutItem: %v", err)
		}
	}
	put("abc", "application", "tenant-a", 100)
	put("abc", "customer", "tenant-z", 200) // newer, wrong type: the gateway's Limit:1 query would miss

	p, err := New(ctx, Options{Name: "env", DynamoDB: &DynamoDBOptions{
		Table: "routing", Index: "CreatedAtIndex", FilterAttribute: "id_type", FilterValue: "application", ValueAttribute: "env_name",
		Region: "us-east-1", Endpoint: endpoint, AccessKeyID: "test", SecretAccessKey: "test",
	}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = p.Close() })
	v, found, err := p.Lookup(ctx, "abc")
	if err != nil || !found || v != "tenant-a" {
		t.Fatalf("application lookup: %q %v %v", v, found, err)
	}
	if _, found, err := p.Lookup(ctx, "zzz"); found || err != nil {
		t.Fatalf("unknown id should be a clean miss: %v %v", found, err)
	}
	// Cached: a second lookup of the same key is served without I/O.
	if c, ok := p.(*cached); !ok || !c.Cached("abc") {
		t.Fatal("hit should be cached")
	}
}

// TestPostgresLookupAgainstContainer runs the Postgres provider against a real
// database. Named TestPostgres* so it runs under make postgres-integration-test.
func TestPostgresLookupAgainstContainer(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in -short mode")
	}
	ctx := context.Background()
	container, err := tcpostgres.Run(ctx, "postgres:16-alpine",
		tcpostgres.WithDatabase("routing"), tcpostgres.WithUsername("hookway"), tcpostgres.WithPassword("hookway"),
		tcpostgres.BasicWaitStrategies(),
	)
	if err != nil {
		testenv.RequireContainer(t, err, "postgres")
	}
	t.Cleanup(func() { _ = container.Terminate(ctx) })
	url, err := container.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		t.Fatal(err)
	}
	pool, err := pgxpool.New(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	if _, err := pool.Exec(ctx, `CREATE TABLE routing (id text PRIMARY KEY, env_name text NOT NULL); INSERT INTO routing VALUES ('abc', 'tenant-a')`); err != nil {
		t.Fatal(err)
	}
	p, err := New(ctx, Options{Name: "env", Postgres: &PostgresOptions{URL: url, Query: "SELECT env_name FROM routing WHERE id = {key}"}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = p.Close() })
	if v, found, err := p.Lookup(ctx, "abc"); err != nil || !found || v != "tenant-a" {
		t.Fatalf("hit: %q %v %v", v, found, err)
	}
	if _, found, err := p.Lookup(ctx, "nope"); found || err != nil {
		t.Fatalf("miss: %v %v", found, err)
	}
	// The key is bound, never interpolated.
	if _, found, err := p.Lookup(ctx, "abc' OR '1'='1"); found || err != nil {
		t.Fatalf("injection attempt must be a plain miss: %v %v", found, err)
	}
}

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	var b []byte
	for i > 0 {
		b = append([]byte{byte('0' + i%10)}, b...)
		i /= 10
	}
	return string(b)
}
