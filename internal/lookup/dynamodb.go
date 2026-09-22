package lookup

import (
	"context"
	"fmt"
	"strconv"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	ddbtypes "github.com/aws/aws-sdk-go-v2/service/dynamodb/types"

	"github.com/jaysongiroux/hook_way/internal/awsconf"
)

// DynamoDBOptions configures a Query against a table (or one of its indexes)
// whose partition key is the lookup key. Items are read newest-first
// (ScanIndexForward=false) and the optional equality Filter is applied
// client-side so a Limit can never hide the matching row.
type DynamoDBOptions struct {
	Table           string
	Index           string
	KeyAttribute    string // default "id"
	FilterAttribute string // optional, e.g. "id_type"
	FilterValue     string
	ValueAttribute  string // e.g. "env_name"
	ConsistentRead  bool
	PageLimit       int32 // default 25
	Region          string
	Endpoint        string
	AccessKeyID     string
	SecretAccessKey string
}

// dynamoAPI is the SDK surface the provider uses, so tests can inject a fake.
type dynamoAPI interface {
	Query(ctx context.Context, in *dynamodb.QueryInput, optFns ...func(*dynamodb.Options)) (*dynamodb.QueryOutput, error)
}

type dynamoProvider struct {
	client dynamoAPI
	opts   DynamoDBOptions
}

func newDynamoDB(ctx context.Context, opts DynamoDBOptions) (*dynamoProvider, error) {
	awsCfg, err := awsconf.Load(ctx, opts.Region, opts.Endpoint, opts.AccessKeyID, opts.SecretAccessKey)
	if err != nil {
		return nil, fmt.Errorf("dynamodb: aws config: %w", err)
	}
	return newDynamoDBWithClient(dynamodb.NewFromConfig(awsCfg), opts), nil
}

func newDynamoDBWithClient(client dynamoAPI, opts DynamoDBOptions) *dynamoProvider {
	if opts.KeyAttribute == "" {
		opts.KeyAttribute = "id"
	}
	if opts.PageLimit <= 0 {
		opts.PageLimit = 25
	}
	return &dynamoProvider{client: client, opts: opts}
}

// Lookup queries by key, newest first, and returns the value attribute of the
// first item that satisfies the optional filter. Pages until a match, the
// results are exhausted, or the context ends.
func (p *dynamoProvider) Lookup(ctx context.Context, key string) (string, bool, error) {
	in := &dynamodb.QueryInput{
		TableName:                aws.String(p.opts.Table),
		KeyConditionExpression:   aws.String("#k = :k"),
		ExpressionAttributeNames: map[string]string{"#k": p.opts.KeyAttribute},
		ExpressionAttributeValues: map[string]ddbtypes.AttributeValue{
			":k": &ddbtypes.AttributeValueMemberS{Value: key},
		},
		ScanIndexForward: aws.Bool(false),
		Limit:            aws.Int32(p.opts.PageLimit),
		ConsistentRead:   aws.Bool(p.opts.ConsistentRead),
	}
	if p.opts.Index != "" {
		in.IndexName = aws.String(p.opts.Index)
	}
	for {
		out, err := p.client.Query(ctx, in)
		if err != nil {
			return "", false, fmt.Errorf("dynamodb query: %w", err)
		}
		for _, item := range out.Items {
			if p.opts.FilterAttribute != "" {
				if got, ok := attrString(item[p.opts.FilterAttribute]); !ok || got != p.opts.FilterValue {
					continue
				}
			}
			value, ok := attrString(item[p.opts.ValueAttribute])
			if !ok || value == "" {
				continue
			}
			return value, true, nil
		}
		if len(out.LastEvaluatedKey) == 0 {
			return "", false, nil
		}
		in.ExclusiveStartKey = out.LastEvaluatedKey
	}
}

func (p *dynamoProvider) Close() error { return nil }

// attrString renders a string or number attribute as a string.
func attrString(av ddbtypes.AttributeValue) (string, bool) {
	switch v := av.(type) {
	case *ddbtypes.AttributeValueMemberS:
		return v.Value, true
	case *ddbtypes.AttributeValueMemberN:
		if _, err := strconv.ParseFloat(v.Value, 64); err != nil {
			return "", false
		}
		return v.Value, true
	default:
		return "", false
	}
}
