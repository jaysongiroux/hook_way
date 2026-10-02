package flowconfig

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/jaysongiroux/hook_way/internal/deliverymeta"
	"github.com/jaysongiroux/hook_way/internal/graph"
	"github.com/jaysongiroux/hook_way/internal/jsoncanon"
	"github.com/jaysongiroux/hook_way/internal/vars"
)

func validateIdentity(o PublishIdentityOptions) error {
	switch o.Source {
	case "static":
		if err := require("value", o.Value); err != nil {
			return err
		}
		if err := validateTemplate("value", o.Value); err != nil {
			return err
		}
	case "body", "header", "variable":
		if err := require("path", o.Path); err != nil {
			return err
		}
	case "raw_sha256", "json_js_sha256":
	default:
		return fmt.Errorf("unsupported FIFO identity source %q", o.Source)
	}
	if o.Source != "static" && o.Value != "" {
		return fmt.Errorf("value is only valid for static identity")
	}
	if o.Source != "body" && o.Source != "header" && o.Source != "variable" && o.Path != "" {
		return fmt.Errorf("path is only valid for body/header/variable identity")
	}
	if o.Source == "static" {
		tpl, _ := vars.Parse(o.Value)
		if len(tpl.Names()) == 0 && !deliverymeta.ValidFIFOIdentity(o.Value) {
			return fmt.Errorf("literal FIFO identity must be 1..128 ASCII alphanumeric or punctuation characters")
		}
	}
	if o.FallbackHash != "" && o.FallbackHash != "raw_sha256" && o.FallbackHash != "json_js_sha256" {
		return fmt.Errorf("fallback_hash must be raw_sha256 or json_js_sha256")
	}
	if o.FallbackHash != "" && (o.Fallback != "" || o.Source == "static" || o.Source == "raw_sha256" || o.Source == "json_js_sha256") {
		return fmt.Errorf("fallback_hash requires body/header/variable source and cannot be combined with fallback")
	}
	hash := o.Source == "raw_sha256" || o.Source == "json_js_sha256" || o.FallbackHash != ""
	if o.HashLength < 0 || o.HashLength > 64 || (!hash && o.HashLength != 0) {
		return fmt.Errorf("hash_length is 1..64 for hash sources only")
	}
	return validateTemplate("fallback", o.Fallback)
}

func validatePublishDelivery(o PublishOptions) error {
	switch o.WireBody {
	case "", "envelope", "raw", "json", "json_js":
	default:
		return fmt.Errorf("unsupported wire_body %q", o.WireBody)
	}
	if o.Payload == "object" && o.WireBody != "" && o.WireBody != "envelope" {
		return fmt.Errorf("object payload and broker wire_body cannot be combined")
	}
	if o.GroupID != nil {
		if o.FIFO != nil {
			return fmt.Errorf("group_id is for standard targets; use fifo.group_id on FIFO targets")
		}
		if err := validateIdentity(*o.GroupID); err != nil {
			return fmt.Errorf("group_id: %w", err)
		}
	}
	if o.SQS != nil && o.SQS.QueueOwnerAccountID != "" {
		id := o.SQS.QueueOwnerAccountID
		if len(id) != 12 || strings.Trim(id, "0123456789") != "" || o.Create {
			return fmt.Errorf("sqs.queue_owner_account_id must be 12 digits and requires create: false")
		}
	}
	if o.SNS != nil && o.SNS.TopicARN != "" {
		parts := strings.Split(o.SNS.TopicARN, ":")
		if len(parts) != 6 || parts[0] != "arn" || (parts[1] != "aws" && parts[1] != "aws-cn" && parts[1] != "aws-us-gov") || parts[2] != "sns" || parts[3] == "" || len(parts[4]) != 12 || strings.Trim(parts[4], "0123456789") != "" || parts[5] != o.Target || o.Create {
			return fmt.Errorf("sns.topic_arn must identify target in an AWS partition and requires create: false")
		}
	}
	if o.SQS != nil && o.SQS.DelaySeconds != nil && (*o.SQS.DelaySeconds < 0 || *o.SQS.DelaySeconds > 900 || o.FIFO != nil) {
		return fmt.Errorf("sqs.delay_seconds must be 0..900 and is only valid for standard queues")
	}
	if o.SNS != nil {
		if o.SNS.MessageStructure == "json" && len(o.MessageAttributes) > 0 {
			return fmt.Errorf("sns.message_structure: json does not deliver message attributes")
		}
		if o.SNS.MessageStructure != "" && o.SNS.MessageStructure != "json" {
			return fmt.Errorf("sns.message_structure must be json")
		}
		if len(o.SNS.Subject) > 99 || strings.ContainsAny(o.SNS.Subject, "\r\n") || !utf8.ValidString(o.SNS.Subject) {
			return fmt.Errorf("sns.subject must be valid UTF-8, less than 100 bytes, without line breaks")
		}
		if o.FIFO != nil && o.SNS.Subject != "" {
			return fmt.Errorf("sns.subject is only supported on standard topics")
		}
	}
	if o.FIFO != nil {
		if err := validateIdentity(o.FIFO.GroupID); err != nil {
			return fmt.Errorf("fifo.group_id: %w", err)
		}
		if (o.FIFO.DeduplicationID != nil) == o.FIFO.ContentBased {
			return fmt.Errorf("fifo requires exactly one of deduplication_id or content_based_deduplication")
		}
		if o.FIFO.DeduplicationID != nil {
			if err := validateIdentity(*o.FIFO.DeduplicationID); err != nil {
				return fmt.Errorf("fifo.deduplication_id: %w", err)
			}
		}
	}
	if o.Retry != nil {
		if o.Retry.MaxAttempts < 1 || o.Retry.MaxAttempts > 10 {
			return fmt.Errorf("retry.max_attempts must be 1..10")
		}
		for name, value := range map[string]string{"initial_backoff": o.Retry.InitialBackoff, "max_backoff": o.Retry.MaxBackoff} {
			if err := validateDuration(name, value, false); err != nil {
				return err
			}
		}
		initial, max := publishBackoffs(o.Retry)
		if max < initial || max > time.Minute {
			return fmt.Errorf("retry backoffs must increase and be at most 1m")
		}
	}
	return nil
}

func publishBackoffs(o *PublishRetryOptions) (time.Duration, time.Duration) {
	initial, max := 100*time.Millisecond, time.Second
	if o.InitialBackoff != "" {
		initial, _ = time.ParseDuration(o.InitialBackoff)
	}
	if o.MaxBackoff != "" {
		max, _ = time.ParseDuration(o.MaxBackoff)
	}
	return initial, max
}

func prepareDelivery(ctx context.Context, o PublishOptions, target string, env graph.Envelope) (*deliverymeta.Publish, error) {
	// A resume snapshot freezes this destination's body and IDs. Another publish
	// target must prepare its own metadata rather than inherit that snapshot.
	if env.Publish != nil && env.Publish.NodeID == PublishingNode(ctx) && env.Publish.Destination == o.Destination && env.Publish.Target == target {
		return deliverymeta.Clone(env.Publish), nil
	}
	p := &deliverymeta.Publish{NodeID: PublishingNode(ctx), Destination: o.Destination, Target: target, WireBody: o.WireBody}
	if p.WireBody == "" {
		p.WireBody = "envelope"
	}
	var canonical []byte
	canonicalError := func(err error) error {
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return graph.NodeError{Status: 503, Err: err}
		}
		return graph.NodeError{Status: 400, Reason: "body_error", Err: err}
	}
	canonicalize := func() ([]byte, error) {
		if canonical != nil {
			return canonical, nil
		}
		var err error
		if data, match, reuseErr := env.V.OriginalJSON.Bytes(ctx, env.V.Raw.Body); match || reuseErr != nil {
			if reuseErr != nil {
				return nil, canonicalError(reuseErr)
			}
			canonical = data
			return canonical, nil
		}
		canonical, err = jsoncanon.Stringify(ctx, env.V.Raw.Body)
		if err != nil {
			err = canonicalError(err)
		}
		return canonical, err
	}
	var err error
	switch p.WireBody {
	case "raw":
		p.Body = slices.Clone(env.V.Raw.Body)
	case "json":
		if env.BodyFormat != "json" {
			return nil, fmt.Errorf("wire_body: json requires a JSON envelope")
		}
		if !json.Valid(env.V.Raw.Body) {
			return nil, graph.NodeError{Status: 400, Reason: "body_error", Err: fmt.Errorf("wire_body: invalid JSON request")}
		}
		p.Body, err = json.Marshal(env.Body)
	case "json_js":
		p.Body, err = canonicalize()
	}
	if err != nil {
		return nil, err
	}
	if p.WireBody != "envelope" && !utf8.Valid(p.Body) {
		return nil, fmt.Errorf("broker body must be valid UTF-8")
	}
	if o.GroupID != nil {
		p.GroupID, err = resolvePublishIdentity(*o.GroupID, env, canonicalize)
		if err != nil {
			return nil, err
		}
	}
	if o.SQS != nil && o.SQS.DelaySeconds != nil {
		d := *o.SQS.DelaySeconds
		p.DelaySeconds = &d
	}
	if o.SNS != nil {
		p.Subject = o.SNS.Subject
		p.MessageStructure = o.SNS.MessageStructure
	}
	if o.FIFO != nil {
		p.FIFO = &deliverymeta.FIFO{ContentBased: o.FIFO.ContentBased}
		p.FIFO.GroupID, err = resolvePublishIdentity(o.FIFO.GroupID, env, canonicalize)
		if err != nil {
			return nil, fmt.Errorf("FIFO group identity: %w", err)
		}
		if o.FIFO.DeduplicationID != nil {
			p.FIFO.DeduplicationID, err = resolvePublishIdentity(*o.FIFO.DeduplicationID, env, canonicalize)
			if err != nil {
				return nil, fmt.Errorf("FIFO dedup identity: %w", err)
			}
		}
	}
	if o.Retry != nil {
		initial, max := publishBackoffs(o.Retry)
		p.Retry = &deliverymeta.Retry{MaxAttempts: o.Retry.MaxAttempts, InitialBackoff: int64(initial), MaxBackoff: int64(max)}
	}
	return p, nil
}

func resolvePublishIdentity(o PublishIdentityOptions, env graph.Envelope, canonicalize func() ([]byte, error)) (string, error) {
	var value string
	switch o.Source {
	case "static":
		tpl, err := vars.Parse(o.Value)
		if err != nil {
			return "", err
		}
		value, err = tpl.Render(env.Vars)
		if err != nil {
			return "", err
		}
	case "body":
		value, _ = vars.BodyString(env.Body, o.Path)
	case "header":
		value = env.V.Raw.Head.Header.Get(o.Path)
	case "variable":
		value = env.Vars[o.Path]
	case "raw_sha256", "json_js_sha256":
		data := env.V.Raw.Body
		if o.Source == "json_js_sha256" {
			var err error
			data, err = canonicalize()
			if err != nil {
				return "", err
			}
		}
		sum := sha256.Sum256(data)
		value = hex.EncodeToString(sum[:])
		if o.HashLength > 0 {
			value = value[:o.HashLength]
		}
	}
	if value == "" && o.FallbackHash != "" {
		return resolvePublishIdentity(PublishIdentityOptions{Source: o.FallbackHash, HashLength: o.HashLength}, env, canonicalize)
	}
	if value == "" && o.Fallback != "" {
		tpl, err := vars.Parse(o.Fallback)
		if err != nil {
			return "", err
		}
		value, err = tpl.Render(env.Vars)
		if err != nil {
			return "", err
		}
	}
	if !deliverymeta.ValidFIFOIdentity(value) {
		return "", fmt.Errorf("identity must be 1..128 ASCII alphanumeric or punctuation characters")
	}
	return value, nil
}
