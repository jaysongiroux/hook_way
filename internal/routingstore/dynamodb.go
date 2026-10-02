// Package routingstore binds typed routing contracts to native AWS clients.
package routingstore

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	types "github.com/aws/aws-sdk-go-v2/service/dynamodb/types"

	"github.com/jaysongiroux/hook_way/internal/awsconf"
	"github.com/jaysongiroux/hook_way/internal/routing"
)

type api interface {
	GetItem(context.Context, *dynamodb.GetItemInput, ...func(*dynamodb.Options)) (*dynamodb.GetItemOutput, error)
	PutItem(context.Context, *dynamodb.PutItemInput, ...func(*dynamodb.Options)) (*dynamodb.PutItemOutput, error)
}
type Store struct {
	client  api
	opts    routing.StoreConfig
	timeout time.Duration
	close   func()
}

// NewWithAdmissionBudget binds a private SDK pool to the serving admission
// budget. Zero uses the transport's bounded default connection retention.
func NewWithAdmissionBudget(ctx context.Context, opts routing.StoreConfig, admitted int) (*Store, error) {
	if err := opts.Validate(); err != nil {
		return nil, err
	}
	opts = opts.Defaults()
	timeout, _ := time.ParseDuration(opts.Timeout)
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	cfg, err := awsconf.Load(ctx, opts.Region, opts.Endpoint, "", "")
	if err != nil {
		return nil, fmt.Errorf("routing AWS config: %w", err)
	}
	var closeIdle func()
	client := dynamodb.NewFromConfig(cfg, func(o *dynamodb.Options) {
		o.HTTPClient, closeIdle = awsconf.OwnHTTPClient(o.HTTPClient, admitted)
	})
	out, err := client.DescribeTable(ctx, &dynamodb.DescribeTableInput{TableName: aws.String(opts.Table)})
	if err != nil {
		closeIdle()
		return nil, fmt.Errorf("routing table startup check: %w", err)
	}
	keys := map[types.KeyType]string{}
	attrs := map[string]types.ScalarAttributeType{}
	if out.Table != nil {
		for _, k := range out.Table.KeySchema {
			keys[k.KeyType] = aws.ToString(k.AttributeName)
		}
		for _, a := range out.Table.AttributeDefinitions {
			attrs[aws.ToString(a.AttributeName)] = a.AttributeType
		}
	}
	if keys[types.KeyTypeHash] != opts.IDField || keys[types.KeyTypeRange] != opts.TypeField || attrs[opts.IDField] != types.ScalarAttributeTypeS || attrs[opts.TypeField] != types.ScalarAttributeTypeS {
		closeIdle()
		return nil, errors.New("routing table requires configured string HASH and RANGE keys")
	}
	return &Store{client: client, opts: opts, timeout: timeout, close: closeIdle}, nil
}
func (s *Store) Close() error {
	if s.close != nil {
		s.close()
	}
	return nil
}
func (s *Store) key(k routing.Key) map[string]types.AttributeValue {
	return map[string]types.AttributeValue{s.opts.IDField: &types.AttributeValueMemberS{Value: k.ID}, s.opts.TypeField: &types.AttributeValueMemberS{Value: k.IDType}}
}
func (s *Store) Get(ctx context.Context, k routing.Key) (routing.Record, bool, error) {
	if err := routing.ValidateKey(k); err != nil {
		return routing.Record{}, false, err
	}
	ctx, cancel := context.WithTimeout(ctx, s.timeout)
	defer cancel()
	out, err := s.client.GetItem(ctx, &dynamodb.GetItemInput{TableName: aws.String(s.opts.Table), Key: s.key(k), ConsistentRead: aws.Bool(true)})
	if err != nil {
		return routing.Record{}, false, fmt.Errorf("routing read: %w", err)
	}
	if len(out.Item) == 0 {
		return routing.Record{}, false, nil
	}
	r, err := s.record(out.Item)
	if err != nil {
		return routing.Record{}, false, err
	}
	return r, true, nil
}
func (s *Store) Put(ctx context.Context, r routing.Record) error {
	if err := routing.ValidateRecord(r); err != nil {
		return err
	}
	if r.CreatedAt == 0 {
		r.CreatedAt = time.Now().Unix()
	}
	item := s.key(r.Key)
	item[s.opts.EnvironmentField] = &types.AttributeValueMemberS{Value: r.Environment}
	item[s.opts.CreatedAtField] = &types.AttributeValueMemberN{Value: strconv.FormatInt(r.CreatedAt, 10)}
	ctx, cancel := context.WithTimeout(ctx, s.timeout)
	defer cancel()
	_, err := s.client.PutItem(ctx, &dynamodb.PutItemInput{TableName: aws.String(s.opts.Table), Item: item})
	if err != nil {
		return fmt.Errorf("routing write: %w", err)
	}
	return nil
}
func (s *Store) record(item map[string]types.AttributeValue) (routing.Record, error) {
	stringAt := func(f string) string {
		v, _ := item[f].(*types.AttributeValueMemberS)
		if v == nil {
			return ""
		}
		return v.Value
	}
	r := routing.Record{Key: routing.Key{ID: stringAt(s.opts.IDField), IDType: stringAt(s.opts.TypeField)}, Environment: stringAt(s.opts.EnvironmentField)}
	n, ok := item[s.opts.CreatedAtField].(*types.AttributeValueMemberN)
	if !ok {
		return r, errors.New("malformed routing row timestamp")
	}
	v, err := strconv.ParseInt(n.Value, 10, 64)
	if err != nil {
		return r, errors.New("malformed routing row timestamp")
	}
	r.CreatedAt = v
	if err := routing.ValidateRecord(r); err != nil {
		return r, errors.New("malformed routing row")
	}
	return r, nil
}
