package queue

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	s3types "github.com/aws/aws-sdk-go-v2/service/s3/types"

	"github.com/jaysongiroux/hook_way/internal/vars"
)

type s3API interface {
	HeadBucket(ctx context.Context, in *s3.HeadBucketInput, optFns ...func(*s3.Options)) (*s3.HeadBucketOutput, error)
	CreateBucket(ctx context.Context, in *s3.CreateBucketInput, optFns ...func(*s3.Options)) (*s3.CreateBucketOutput, error)
	PutObject(ctx context.Context, in *s3.PutObjectInput, optFns ...func(*s3.Options)) (*s3.PutObjectOutput, error)
}

// S3Target is a single route target. For S3 the target name is the base folder
// (key prefix) under which that route's objects are written.
type S3Target struct {
	Name string
	// Template is set for dynamic routes whose target contains {variable}
	// placeholders; Name is then the authored template and any rendered
	// target the matcher accepts is a valid folder.
	Template vars.Matcher
}

type S3Config struct {
	Region          string
	Endpoint        string
	AccessKeyID     string
	SecretAccessKey string
	Bucket          string
	Create          bool
	SSE             string // "" | "AES256" | "aws:kms"
	SSEKMSKeyID     string
	DestinationID   string
	Logger          *slog.Logger
	Targets         []S3Target
}

type S3Publisher struct {
	client  s3API
	bucket  string
	sse     string
	kmsKey  string
	logger  *slog.Logger
	folders *targetResolver[struct{}] // known base folders (route targets), static and dynamic
}

// s3ObjectKey builds the object key for a message:
//
//	envelope: <base_folder>/yyyy/mm/dd/<received_at>-<id>.json
//	object:   <base_folder>/yyyy/mm/dd/<received_at>-<id>[-<filename>]
//
// The date partition and timestamp are derived from ReceivedAt in UTC. The
// timestamp is formatted without colons (S3-key-safe) with an explicit "Z"
// suffix since the value is always UTC.
func s3ObjectKey(folder string, msg Message) string {
	t := msg.ReceivedAt.UTC()
	datePath := t.Format("2006/01/02")
	ts := t.Format("20060102T150405.000000000") + "Z"
	name := ts + "-" + msg.ID
	if msg.PayloadMode == "object" {
		if safe := SanitizeObjectFilename(msg.ObjectFilename); safe != "" {
			name += "-" + safe
		}
	} else {
		name += ".json"
	}

	parts := make([]string, 0, 3)
	if folder = strings.Trim(folder, "/"); folder != "" {
		parts = append(parts, folder)
	}
	parts = append(parts, datePath, name)
	return strings.Join(parts, "/")
}

// NewS3Publisher verifies the destination bucket exists (creating it when
// Create is set), and records the configured base folders. S3 prefixes are
// implicit, so nothing per-folder is provisioned.
func NewS3Publisher(ctx context.Context, client s3API, cfg S3Config) (*S3Publisher, error) {
	logger := cfg.Logger
	if logger == nil {
		logger = slog.Default()
	}
	if err := ensureS3Bucket(ctx, client, cfg, logger); err != nil {
		return nil, err
	}
	if cfg.SSE == "aws:kms" && cfg.SSEKMSKeyID == "" {
		logger.Warn("s3 destination uses aws:kms without sse_kms_key_id; falling back to the bucket default KMS key",
			"destination", cfg.DestinationID, "bucket", cfg.Bucket)
	}
	folders := newTargetResolver[struct{}](0, logger)
	for _, t := range cfg.Targets {
		if t.Template.IsTemplate() {
			folders.addTemplate(t.Template, struct{}{})
		} else {
			folders.addStatic(t.Name, struct{}{})
		}
	}
	return &S3Publisher{
		client:  client,
		bucket:  cfg.Bucket,
		sse:     cfg.SSE,
		kmsKey:  cfg.SSEKMSKeyID,
		logger:  logger,
		folders: folders,
	}, nil
}

func ensureS3Bucket(ctx context.Context, client s3API, cfg S3Config, logger *slog.Logger) error {
	_, err := client.HeadBucket(ctx, &s3.HeadBucketInput{Bucket: aws.String(cfg.Bucket)})
	if err == nil {
		logger.Debug("bucket verified", slog.String("destination", cfg.DestinationID), slog.String("target", cfg.Bucket))
		return nil
	}
	var notFound *s3types.NotFound
	if !errors.As(err, &notFound) {
		return fmt.Errorf("s3 bucket %q: %w", cfg.Bucket, err)
	}
	if !cfg.Create {
		return fmt.Errorf("s3 bucket %q does not exist (set create: true to auto-create)", cfg.Bucket)
	}
	in := &s3.CreateBucketInput{Bucket: aws.String(cfg.Bucket)}
	// us-east-1 must NOT set a LocationConstraint; every other region must.
	if cfg.Region != "" && cfg.Region != "us-east-1" {
		in.CreateBucketConfiguration = &s3types.CreateBucketConfiguration{
			LocationConstraint: s3types.BucketLocationConstraint(cfg.Region),
		}
	}
	if _, err := client.CreateBucket(ctx, in); err != nil {
		return fmt.Errorf("s3 bucket %q: create: %w", cfg.Bucket, err)
	}
	logger.Info("created bucket", slog.String("destination", cfg.DestinationID), slog.String("target", cfg.Bucket))
	return nil
}

func (p *S3Publisher) Publish(ctx context.Context, destinationID, target string, msg Message) error {
	if _, err := p.folders.resolve(ctx, target, passthroughProvision[struct{}]); err != nil {
		return fmt.Errorf("s3 target %q is not configured", target)
	}
	body, contentType, err := s3PutBody(msg)
	if err != nil {
		return err
	}
	in := &s3.PutObjectInput{
		Bucket:      aws.String(p.bucket),
		Key:         aws.String(s3ObjectKey(target, msg)),
		Body:        bytes.NewReader(body),
		ContentType: aws.String(contentType),
	}
	if msg.PayloadMode == "object" && msg.ID != "" {
		in.Metadata = map[string]string{"hookway-event-id": msg.ID}
		if safe := SanitizeObjectFilename(msg.ObjectFilename); safe != "" {
			in.Metadata["hookway-filename"] = safe
		}
	}
	if p.sse != "" {
		in.ServerSideEncryption = s3types.ServerSideEncryption(p.sse)
		if p.sse == "aws:kms" && p.kmsKey != "" {
			in.SSEKMSKeyId = aws.String(p.kmsKey)
		}
	}
	_, err = p.client.PutObject(ctx, in)
	return err
}

func s3PutBody(msg Message) ([]byte, string, error) {
	if msg.PayloadMode == "object" {
		contentType, err := normalizeObjectContentType(msg.ObjectContentType)
		if err != nil {
			return nil, "", err
		}
		return msg.ObjectBytes, contentType, nil
	}
	body, err := json.Marshal(msg)
	if err != nil {
		return nil, "", err
	}
	return body, "application/json", nil
}

// Ready confirms the destination bucket is reachable.
func (p *S3Publisher) Ready(ctx context.Context) error {
	_, err := p.client.HeadBucket(ctx, &s3.HeadBucketInput{Bucket: aws.String(p.bucket)})
	if err != nil {
		return fmt.Errorf("s3 bucket %q: %w", p.bucket, err)
	}
	return nil
}

func (p *S3Publisher) Close() error { return nil }
