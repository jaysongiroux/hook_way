package queue

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	snstypes "github.com/aws/aws-sdk-go-v2/service/sns/types"
	sqstypes "github.com/aws/aws-sdk-go-v2/service/sqs/types"
)

const (
	maxMessageAttributes          = 10
	maxMessageAttributeNameBytes  = 256
	maxMessageAttributeValueBytes = 256 * 1024
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
	switch attr.DataType {
	case "String", "Number", "String.Array":
	default:
		return fmt.Errorf("message attribute %q: unsupported DataType %q (use String, Number, or String.Array)", name, attr.DataType)
	}
	if len(attr.StringValue) > maxMessageAttributeValueBytes {
		return fmt.Errorf("message attribute %q: value is %d bytes, AWS limit is %d", name, len(attr.StringValue), maxMessageAttributeValueBytes)
	}
	switch attr.DataType {
	case "Number":
		if _, err := strconv.ParseFloat(attr.StringValue, 64); err != nil {
			return fmt.Errorf("message attribute %q: Number value %q is not numeric", name, attr.StringValue)
		}
	case "String.Array":
		var arr []any
		if err := json.Unmarshal([]byte(attr.StringValue), &arr); err != nil {
			return fmt.Errorf("message attribute %q: String.Array value must be a JSON array, got %q", name, attr.StringValue)
		}
	}
	return nil
}

func snsMessageAttributes(attrs map[string]MessageAttribute) (map[string]snstypes.MessageAttributeValue, error) {
	if err := validateMessageAttributes(attrs); err != nil {
		return nil, err
	}
	if len(attrs) == 0 {
		return nil, nil
	}
	out := make(map[string]snstypes.MessageAttributeValue, len(attrs))
	for name, attr := range attrs {
		out[name] = snstypes.MessageAttributeValue{
			DataType:    aws.String(attr.DataType),
			StringValue: aws.String(attr.StringValue),
		}
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
		out[name] = sqstypes.MessageAttributeValue{
			DataType:    aws.String(attr.DataType),
			StringValue: aws.String(attr.StringValue),
		}
	}
	return out, nil
}
