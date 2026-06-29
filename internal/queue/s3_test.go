package queue

import (
	"bytes"
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	s3types "github.com/aws/aws-sdk-go-v2/service/s3/types"
)

func TestS3ObjectKey(t *testing.T) {
	received := time.Date(2026, 6, 29, 14, 30, 0, 123456789, time.UTC)
	msg := Message{ID: "evt_1", ReceivedAt: received}

	cases := []struct {
		name   string
		folder string
		want   string
	}{
		{"with folder", "orders", "orders/2026/06/29/20260629T143000.123456789Z-evt_1.json"},
		{"trailing slash trimmed", "orders/", "orders/2026/06/29/20260629T143000.123456789Z-evt_1.json"},
		{"empty folder writes to root", "", "2026/06/29/20260629T143000.123456789Z-evt_1.json"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := s3ObjectKey(tc.folder, msg); got != tc.want {
				t.Fatalf("s3ObjectKey(%q) = %q, want %q", tc.folder, got, tc.want)
			}
		})
	}
}

func TestS3ObjectKeyConvertsToUTC(t *testing.T) {
	// A non-UTC ReceivedAt must be normalized to UTC for both partition and timestamp.
	loc := time.FixedZone("EST", -5*3600)
	msg := Message{ID: "evt_2", ReceivedAt: time.Date(2026, 6, 29, 23, 30, 0, 0, loc)}
	// 23:30 EST == 04:30 next-day UTC.
	want := "f/2026/06/30/20260630T043000.000000000Z-evt_2.json"
	if got := s3ObjectKey("f", msg); got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

type fakeS3 struct {
	buckets      map[string]bool
	puts         []*s3.PutObjectInput
	created      []string
	createInputs []*s3.CreateBucketInput
	heads        int
}

func newFakeS3() *fakeS3 {
	return &fakeS3{buckets: map[string]bool{}}
}

func (f *fakeS3) HeadBucket(ctx context.Context, in *s3.HeadBucketInput, _ ...func(*s3.Options)) (*s3.HeadBucketOutput, error) {
	f.heads++
	if f.buckets[aws.ToString(in.Bucket)] {
		return &s3.HeadBucketOutput{}, nil
	}
	return nil, &s3types.NotFound{}
}

func (f *fakeS3) CreateBucket(ctx context.Context, in *s3.CreateBucketInput, _ ...func(*s3.Options)) (*s3.CreateBucketOutput, error) {
	name := aws.ToString(in.Bucket)
	f.buckets[name] = true
	f.created = append(f.created, name)
	f.createInputs = append(f.createInputs, in)
	return &s3.CreateBucketOutput{}, nil
}

func (f *fakeS3) PutObject(ctx context.Context, in *s3.PutObjectInput, _ ...func(*s3.Options)) (*s3.PutObjectOutput, error) {
	f.puts = append(f.puts, in)
	return &s3.PutObjectOutput{}, nil
}

func (f *fakeS3) seedBucket(name string) { f.buckets[name] = true }

func TestS3PublisherVerifiesExistingBucket(t *testing.T) {
	fake := newFakeS3()
	fake.seedBucket("events")
	_, err := NewS3Publisher(context.Background(), fake, S3Config{
		Bucket: "events", Targets: []S3Target{{Name: "orders"}},
	})
	if err != nil {
		t.Fatalf("NewS3Publisher: %v", err)
	}
	if len(fake.created) != 0 {
		t.Fatalf("should not create an existing bucket: %#v", fake.created)
	}
}

func TestS3PublisherCreatesMissingBucketWhenCreate(t *testing.T) {
	fake := newFakeS3()
	_, err := NewS3Publisher(context.Background(), fake, S3Config{
		Bucket: "events", Create: true, Targets: []S3Target{{Name: "orders"}},
	})
	if err != nil {
		t.Fatalf("NewS3Publisher: %v", err)
	}
	if len(fake.created) != 1 || fake.created[0] != "events" {
		t.Fatalf("expected bucket creation, got %#v", fake.created)
	}
}

func TestS3PublisherErrorsOnMissingBucketWithoutCreate(t *testing.T) {
	fake := newFakeS3()
	_, err := NewS3Publisher(context.Background(), fake, S3Config{
		Bucket: "events", Create: false,
	})
	if err == nil {
		t.Fatalf("expected error for missing bucket with create=false")
	}
}

func TestS3PublisherPublishesObject(t *testing.T) {
	fake := newFakeS3()
	fake.seedBucket("events")
	pub, err := NewS3Publisher(context.Background(), fake, S3Config{
		Bucket: "events", Targets: []S3Target{{Name: "orders"}},
	})
	if err != nil {
		t.Fatalf("NewS3Publisher: %v", err)
	}
	msg := Message{ID: "evt_1", ReceivedAt: time.Date(2026, 6, 29, 14, 30, 0, 0, time.UTC)}
	if err := pub.Publish(context.Background(), "archive", "orders", msg); err != nil {
		t.Fatalf("Publish: %v", err)
	}
	if len(fake.puts) != 1 {
		t.Fatalf("expected 1 put, got %d", len(fake.puts))
	}
	put := fake.puts[0]
	wantKey := "orders/2026/06/29/20260629T143000.000000000Z-evt_1.json"
	if aws.ToString(put.Key) != wantKey {
		t.Fatalf("key = %q, want %q", aws.ToString(put.Key), wantKey)
	}
	if aws.ToString(put.Bucket) != "events" {
		t.Fatalf("bucket = %q", aws.ToString(put.Bucket))
	}
	if aws.ToString(put.ContentType) != "application/json" {
		t.Fatalf("content-type = %q", aws.ToString(put.ContentType))
	}
	// Body must be the JSON-marshaled message.
	body := readAll(t, put)
	var decoded Message
	if err := json.Unmarshal(body, &decoded); err != nil {
		t.Fatalf("body is not valid Message JSON: %v", err)
	}
	if decoded.ID != "evt_1" {
		t.Fatalf("decoded id = %q", decoded.ID)
	}
}

func TestS3PublisherSetsSSE(t *testing.T) {
	fake := newFakeS3()
	fake.seedBucket("events")
	pub, err := NewS3Publisher(context.Background(), fake, S3Config{
		Bucket: "events", SSE: "aws:kms", SSEKMSKeyID: "key-123",
		Targets: []S3Target{{Name: "orders"}},
	})
	if err != nil {
		t.Fatalf("NewS3Publisher: %v", err)
	}
	if err := pub.Publish(context.Background(), "archive", "orders", Message{ID: "evt_1", ReceivedAt: time.Date(2026, 6, 29, 14, 30, 0, 0, time.UTC)}); err != nil {
		t.Fatalf("Publish: %v", err)
	}
	put := fake.puts[0]
	if put.ServerSideEncryption != s3types.ServerSideEncryptionAwsKms {
		t.Fatalf("sse = %q", put.ServerSideEncryption)
	}
	if aws.ToString(put.SSEKMSKeyId) != "key-123" {
		t.Fatalf("kms key = %q", aws.ToString(put.SSEKMSKeyId))
	}
}

func TestS3PublisherRejectsUnknownTarget(t *testing.T) {
	fake := newFakeS3()
	fake.seedBucket("events")
	pub, err := NewS3Publisher(context.Background(), fake, S3Config{
		Bucket: "events", Targets: []S3Target{{Name: "orders"}},
	})
	if err != nil {
		t.Fatalf("NewS3Publisher: %v", err)
	}
	err = pub.Publish(context.Background(), "archive", "not-configured", Message{ID: "x", ReceivedAt: time.Now()})
	if err == nil {
		t.Fatalf("expected error for unknown target")
	}
}

func TestS3PublisherReadyProbesBucket(t *testing.T) {
	fake := newFakeS3()
	fake.seedBucket("events")
	pub, err := NewS3Publisher(context.Background(), fake, S3Config{Bucket: "events"})
	if err != nil {
		t.Fatalf("NewS3Publisher: %v", err)
	}
	before := fake.heads
	if err := pub.Ready(context.Background()); err != nil {
		t.Fatalf("Ready: %v", err)
	}
	if fake.heads <= before {
		t.Fatalf("Ready did not probe the bucket via HeadBucket")
	}
}

func TestS3CreateBucketLocationConstraint(t *testing.T) {
	fixedTime := time.Date(2026, 6, 29, 14, 30, 0, 0, time.UTC)

	cases := []struct {
		name            string
		region          string
		wantConstraint  s3types.BucketLocationConstraint
		wantNilConfig   bool
	}{
		{
			name:          "us-east-1 must not set LocationConstraint",
			region:        "us-east-1",
			wantNilConfig: true,
		},
		{
			name:           "eu-west-1 must set LocationConstraint",
			region:         "eu-west-1",
			wantConstraint: s3types.BucketLocationConstraint("eu-west-1"),
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fake := newFakeS3()
			_, err := NewS3Publisher(context.Background(), fake, S3Config{
				Region:  tc.region,
				Bucket:  "my-bucket",
				Create:  true,
				Targets: []S3Target{{Name: "orders"}},
			})
			if err != nil {
				t.Fatalf("NewS3Publisher: %v", err)
			}
			if len(fake.createInputs) != 1 {
				t.Fatalf("expected 1 CreateBucket call, got %d", len(fake.createInputs))
			}
			in := fake.createInputs[0]
			if tc.wantNilConfig {
				if in.CreateBucketConfiguration != nil {
					t.Fatalf("us-east-1: expected nil CreateBucketConfiguration, got %+v", in.CreateBucketConfiguration)
				}
			} else {
				if in.CreateBucketConfiguration == nil {
					t.Fatalf("region %q: expected non-nil CreateBucketConfiguration", tc.region)
				}
				got := in.CreateBucketConfiguration.LocationConstraint
				if got != tc.wantConstraint {
					t.Fatalf("LocationConstraint = %q, want %q", got, tc.wantConstraint)
				}
			}
			// Publish a message to confirm the publisher is functional.
			pub, err := NewS3Publisher(context.Background(), fake, S3Config{
				Region:  tc.region,
				Bucket:  "my-bucket",
				Targets: []S3Target{{Name: "orders"}},
			})
			if err != nil {
				t.Fatalf("second NewS3Publisher: %v", err)
			}
			msg := Message{ID: "evt_region", ReceivedAt: fixedTime}
			if err := pub.Publish(context.Background(), "arch", "orders", msg); err != nil {
				t.Fatalf("Publish: %v", err)
			}
		})
	}
}

// readAll reads a PutObjectInput body into bytes for assertions.
func readAll(t *testing.T, in *s3.PutObjectInput) []byte {
	t.Helper()
	buf := new(bytes.Buffer)
	if _, err := buf.ReadFrom(in.Body); err != nil {
		t.Fatalf("read body: %v", err)
	}
	return buf.Bytes()
}
