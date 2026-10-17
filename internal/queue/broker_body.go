package queue

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/aws/smithy-go"

	"github.com/jaysongiroux/hook_way/internal/deliverymeta"
)

// BrokerBody is shared by sending and downloadable failure payloads. Prepared
// body modes carry frozen bytes; the default envelope retains its old encoding.
func BrokerBody(msg Message) ([]byte, error) {
	if msg.Publish == nil || msg.Publish.WireBody == "" || msg.Publish.WireBody == "envelope" {
		return json.Marshal(msg)
	}
	switch msg.Publish.WireBody {
	case "raw", "json", "json_js":
	default:
		return nil, fmt.Errorf("unsupported broker wire_body")
	}
	if len(msg.Publish.Body) == 0 || !utf8.Valid(msg.Publish.Body) {
		return nil, fmt.Errorf("broker body must be non-empty UTF-8")
	}
	return msg.Publish.Body, nil
}

func brokerFIFO(msg Message, target string, defaults *FIFOSettings) (group, dedup string, err error) {
	var f *deliverymeta.FIFO
	if msg.Publish != nil {
		f = msg.Publish.FIFO
	}
	if !strings.HasSuffix(target, ".fifo") {
		if f != nil {
			return "", "", fmt.Errorf("FIFO metadata on a standard target")
		}
		if msg.Publish != nil && msg.Publish.GroupID != "" {
			if !deliverymeta.ValidFIFOIdentity(msg.Publish.GroupID) {
				return "", "", fmt.Errorf("invalid standard group identity")
			}
			return msg.Publish.GroupID, "", nil
		}
		return "", "", nil
	}
	if f != nil {
		group, dedup = f.GroupID, f.DeduplicationID
		if f.ContentBased == (dedup != "") {
			return "", "", fmt.Errorf("FIFO requires one deduplication strategy")
		}
	} else if defaults != nil {
		group, dedup, _ = resolveFIFO(msg, *defaults)
		if dedup == "" && !defaults.DedupContentBasedFallback {
			return "", "", fmt.Errorf("FIFO deduplication identity is missing")
		}
	} else {
		return "", "", fmt.Errorf("FIFO settings are missing")
	}
	for _, id := range []string{group, dedup} {
		if id != "" && !deliverymeta.ValidFIFOIdentity(id) {
			return "", "", fmt.Errorf("invalid FIFO identity")
		}
	}
	if group == "" {
		return "", "", fmt.Errorf("FIFO group identity is missing")
	}
	return group, dedup, nil
}

func validateBrokerSize(body []byte, attrs map[string]MessageAttribute, max int) error {
	size := len(body)
	for name, attr := range attrs {
		valueBytes := len(attr.StringValue)
		base, _, _ := strings.Cut(attr.DataType, ".")
		if base == "Binary" {
			decoded, err := base64.StdEncoding.Strict().DecodeString(attr.StringValue)
			if err != nil {
				return fmt.Errorf("invalid binary attribute")
			}
			valueBytes = len(decoded)
		}
		size += len(name) + len(attr.DataType) + valueBytes
	}
	if size > max {
		return fmt.Errorf("broker body and attributes exceed %d bytes", max)
	}
	return nil
}

func brokerRetry(ctx context.Context, msg Message, send func(bool) error) error {
	if singleAttempt(ctx) {
		if err := ctx.Err(); err != nil {
			return err
		}
		return send(true)
	}
	if msg.Publish == nil || msg.Publish.Retry == nil {
		return send(false)
	}
	retry := msg.Publish.Retry
	if retry.MaxAttempts < 1 || retry.MaxAttempts > 10 || retry.InitialBackoff <= 0 || retry.MaxBackoff < retry.InitialBackoff || retry.MaxBackoff > int64(time.Minute) {
		return fmt.Errorf("invalid broker retry policy")
	}
	backoff := time.Duration(retry.InitialBackoff)
	for attempt := 1; attempt <= retry.MaxAttempts; attempt++ {
		if err := ctx.Err(); err != nil {
			return err
		}
		err := send(true) // Explicit attempts disable SDK retry stacking.
		if err == nil || attempt == retry.MaxAttempts || !retryableBrokerError(err) {
			return err
		}
		timer := time.NewTimer(backoff)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
		backoff = min(backoff*2, time.Duration(retry.MaxBackoff))
	}
	return nil
}

func retryableBrokerError(err error) bool {
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return false
	}
	var netErr net.Error
	if errors.As(err, &netErr) {
		return true
	}
	var api smithy.APIError
	if errors.As(err, &api) {
		switch api.ErrorCode() {
		case "KmsThrottled", "Throttling", "ThrottlingException", "RequestThrottled", "TooManyRequestsException", "InternalError", "InternalFailure", "ServiceUnavailable":
			return true
		}
		return api.ErrorFault() == smithy.FaultServer
	}
	return false
}

func validateSQSBody(body []byte) error {
	// Decode directly from the immutable body. Converting the whole body to a
	// string here made a second body-sized allocation before the SDK's copy.
	for len(body) > 0 {
		r, size := utf8.DecodeRune(body)
		body = body[size:]
		if r == 9 || r == 10 || r == 13 || r >= 0x20 && r <= 0xd7ff || r >= 0xe000 && r <= 0xfffd || r >= 0x10000 {
			continue
		}
		return fmt.Errorf("SQS body contains an unsupported character")
	}
	return nil
}
