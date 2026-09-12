// Package awsconf builds aws.Config values from hookway's flat AWS settings
// (region, endpoint override, optional static credentials). It is shared by
// the AWS destinations and the DynamoDB lookup provider.
package awsconf

import (
	"context"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
)

// Load builds an aws.Config. Static credentials are used only when both key
// fields are set; otherwise the default credential chain applies. A non-empty
// endpoint overrides the service endpoint, which is how LocalStack is targeted.
func Load(ctx context.Context, region, endpoint, accessKeyID, secretAccessKey string) (aws.Config, error) {
	opts := []func(*awsconfig.LoadOptions) error{}
	if region != "" {
		opts = append(opts, awsconfig.WithRegion(region))
	}
	if accessKeyID != "" && secretAccessKey != "" {
		opts = append(opts, awsconfig.WithCredentialsProvider(
			credentials.NewStaticCredentialsProvider(accessKeyID, secretAccessKey, ""),
		))
	}
	if endpoint != "" {
		opts = append(opts, awsconfig.WithBaseEndpoint(endpoint))
	}
	return awsconfig.LoadDefaultConfig(ctx, opts...)
}
