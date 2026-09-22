package secretref

import (
	"context"
	"fmt"
	"os"
	"sort"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/secretsmanager"
	smtypes "github.com/aws/aws-sdk-go-v2/service/secretsmanager/types"

	"github.com/jaysongiroux/hook_way/internal/awsconf"
)

// secretsAPI is the Secrets Manager surface used by awsFetcher so tests can
// inject a fake without LocalStack.
type secretsAPI interface {
	ListSecrets(ctx context.Context, params *secretsmanager.ListSecretsInput, optFns ...func(*secretsmanager.Options)) (*secretsmanager.ListSecretsOutput, error)
	GetSecretValue(ctx context.Context, params *secretsmanager.GetSecretValueInput, optFns ...func(*secretsmanager.Options)) (*secretsmanager.GetSecretValueOutput, error)
}

type awsFetcher struct {
	client secretsAPI
	name   string
}

func newAWSFetcher(ctx context.Context, name string) (*awsFetcher, error) {
	region := os.Getenv("AWS_REGION")
	if region == "" {
		region = os.Getenv("AWS_DEFAULT_REGION")
	}
	if region == "" {
		region = "us-east-1"
	}
	endpoint := os.Getenv("AWS_ENDPOINT_URL")
	if endpoint == "" {
		endpoint = os.Getenv("AWS_ENDPOINT")
	}
	cfg, err := awsconf.Load(ctx, region, endpoint, os.Getenv("AWS_ACCESS_KEY_ID"), os.Getenv("AWS_SECRET_ACCESS_KEY"))
	if err != nil {
		return nil, err
	}
	return &awsFetcher{client: secretsmanager.NewFromConfig(cfg), name: name}, nil
}

type listedSecret struct {
	id      string
	changed time.Time
}

func (f *awsFetcher) Fetch(ctx context.Context) ([]byte, error) {
	listed, err := f.listMatching(ctx)
	if err != nil || len(listed) == 0 {
		return f.get(ctx, f.name)
	}
	sort.Slice(listed, func(i, j int) bool {
		return listed[i].changed.After(listed[j].changed)
	})
	return f.get(ctx, listed[0].id)
}

func (f *awsFetcher) listMatching(ctx context.Context) ([]listedSecret, error) {
	var (
		out    []listedSecret
		token  *string
		filter = []smtypes.Filter{{
			Key:    smtypes.FilterNameStringTypeName,
			Values: []string{f.name},
		}}
	)
	for {
		page, err := f.client.ListSecrets(ctx, &secretsmanager.ListSecretsInput{
			Filters:   filter,
			NextToken: token,
		})
		if err != nil {
			return nil, err
		}
		for _, s := range page.SecretList {
			id := aws.ToString(s.ARN)
			if id == "" {
				id = aws.ToString(s.Name)
			}
			if id == "" {
				continue
			}
			changed := time.Time{}
			if s.LastChangedDate != nil {
				changed = *s.LastChangedDate
			}
			out = append(out, listedSecret{id: id, changed: changed})
		}
		if page.NextToken == nil || *page.NextToken == "" {
			return out, nil
		}
		token = page.NextToken
	}
}

func (f *awsFetcher) get(ctx context.Context, id string) ([]byte, error) {
	out, err := f.client.GetSecretValue(ctx, &secretsmanager.GetSecretValueInput{SecretId: aws.String(id)})
	if err != nil {
		return nil, err
	}
	if out.SecretString != nil {
		return []byte(*out.SecretString), nil
	}
	if len(out.SecretBinary) > 0 {
		return out.SecretBinary, nil
	}
	return nil, fmt.Errorf("secrets manager secret %q has no string or binary value", id)
}
