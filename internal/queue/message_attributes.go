package queue

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"math"
	"strconv"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	snstypes "github.com/aws/aws-sdk-go-v2/service/sns/types"
	sqstypes "github.com/aws/aws-sdk-go-v2/service/sqs/types"
)

const (
	maxMessageAttributes          = 10
	maxMessageAttributeNameBytes  = 256
	maxMessageAttributeValueBytes = 1048576
)

func validateMessageAttributes(attrs map[string]MessageAttribute) error {
	if len(attrs) == 0 {
		return nil
	}
	if len(attrs) > maxMessageAttributes {
		return fmt.Errorf("message attributes: at most %d attributes allowed (AWS limit), got %d", maxMessageAttributes, len(attrs))
	}
	for name, attr := range attrs {
		if err := validateMessageAttribute(name, attr); err != nil {
			return err
		}
	}
	return nil
}

func validateMessageAttribute(name string, attr MessageAttribute) error {
	if name == "" || strings.TrimSpace(name) != name {
		return fmt.Errorf("message attribute %q: name is empty or has surrounding space", name)
	}
	if len(name) > maxMessageAttributeNameBytes {
		return fmt.Errorf("message attribute %q: name is %d bytes, AWS limit is %d", name, len(name), maxMessageAttributeNameBytes)
	}
	if len(attr.DataType) > 256 || strings.HasSuffix(attr.DataType, ".") {
		return fmt.Errorf("invalid attribute data type")
	}
	base, _, _ := strings.Cut(attr.DataType, ".")
	switch base {
	case "String", "Number", "Binary":
	default:
		return fmt.Errorf("message attribute %q: unsupported DataType %q (use String, Number, Binary, or String.Array)", name, attr.DataType)
	}
	size := len(attr.StringValue)
	if base == "Binary" {
		if size > base64.StdEncoding.EncodedLen(maxMessageAttributeValueBytes) {
			return fmt.Errorf("binary message attribute exceeds size limit")
		}
		decoded, _ := base64.StdEncoding.Strict().DecodeString(attr.StringValue)
		size = len(decoded)
	}
	if size > maxMessageAttributeValueBytes {
		return fmt.Errorf("message attribute %q: value is %d bytes, AWS limit is %d", name, len(attr.StringValue), maxMessageAttributeValueBytes)
	}
	switch {
	case base == "Binary":
		decoded, err := base64.StdEncoding.Strict().DecodeString(attr.StringValue)
		if err != nil || len(decoded) == 0 {
			return fmt.Errorf("message attribute %q: Binary value must be nonempty base64", name)
		}
	case base == "Number":
		if n, err := strconv.ParseFloat(attr.StringValue, 64); err != nil || math.IsNaN(n) || math.IsInf(n, 0) {
			return fmt.Errorf("message attribute %q: Number value %q is not numeric", name, attr.StringValue)
		}
	case attr.DataType == "String.Array":
		var arr []any
		if err := json.Unmarshal([]byte(attr.StringValue), &arr); err != nil || arr == nil {
			return fmt.Errorf("message attribute %q: String.Array value must be a JSON array, got %q", name, attr.StringValue)
		}
	}
	return nil
}

func snsMessageAttributes(attrs map[string]MessageAttribute) (map[string]snstypes.MessageAttributeValue, error) {
	for _, attr := range attrs {
		if attr.DataType != "String" && attr.DataType != "Number" && attr.DataType != "String.Array" && attr.DataType != "Binary" {
			return nil, fmt.Errorf("SNS supports String, Number, String.Array and Binary attributes")
		}
	}
	if err := validateMessageAttributes(attrs); err != nil {
		return nil, err
	}
	if len(attrs) == 0 {
		return nil, nil
	}
	out := make(map[string]snstypes.MessageAttributeValue, len(attrs))
	for name, attr := range attrs {
		value := snstypes.MessageAttributeValue{DataType: aws.String(attr.DataType)}
		base, _, _ := strings.Cut(attr.DataType, ".")
		if base == "Binary" {
			value.BinaryValue, _ = base64.StdEncoding.Strict().DecodeString(attr.StringValue)
		} else {
			value.StringValue = aws.String(attr.StringValue)
		}
		out[name] = value
	}
	return out, nil
}

func sqsMessageAttributes(attrs map[string]MessageAttribute) (map[string]sqstypes.MessageAttributeValue, error) {
	if err := validateMessageAttributes(attrs); err != nil {
		return nil, err
	}
	if len(attrs) == 0 {
		return nil, nil
	}
	out := make(map[string]sqstypes.MessageAttributeValue, len(attrs))
	for name, attr := range attrs {
		value := sqstypes.MessageAttributeValue{DataType: aws.String(attr.DataType)}
		base, _, _ := strings.Cut(attr.DataType, ".")
		if base == "Binary" {
			value.BinaryValue, _ = base64.StdEncoding.Strict().DecodeString(attr.StringValue)
		} else {
			value.StringValue = aws.String(attr.StringValue)
		}
		out[name] = value
	}
	return out, nil
}
