package queue

import (
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
)

func TestSNSMessageAttributesTypes(t *testing.T) {
	got, err := snsMessageAttributes(map[string]MessageAttribute{
		"event":          {DataType: "String", StringValue: "opened"},
		"count":          {DataType: "Number", StringValue: "3"},
		"fields_changed": {DataType: "String.Array", StringValue: `["status","amount"]`},
	})
	if err != nil {
		t.Fatal(err)
	}
	if aws.ToString(got["event"].DataType) != "String" || aws.ToString(got["event"].StringValue) != "opened" {
		t.Fatalf("event = %+v", got["event"])
	}
	if aws.ToString(got["count"].DataType) != "Number" {
		t.Fatalf("count DataType = %q", aws.ToString(got["count"].DataType))
	}
	if aws.ToString(got["fields_changed"].DataType) != "String.Array" {
		t.Fatalf("fields_changed DataType = %q", aws.ToString(got["fields_changed"].DataType))
	}
}

func TestMessageAttributesRejectOversize(t *testing.T) {
	err := validateMessageAttributes(map[string]MessageAttribute{
		"too_big": {DataType: "String", StringValue: strings.Repeat("x", maxMessageAttributeValueBytes+1)},
	})
	if err == nil || !strings.Contains(err.Error(), "AWS limit") {
		t.Fatalf("err = %v, want AWS size-limit error", err)
	}
}

func TestSQSMessageAttributesEmptyIsNil(t *testing.T) {
	got, err := sqsMessageAttributes(nil)
	if err != nil {
		t.Fatal(err)
	}
	if got != nil {
		t.Fatalf("empty attrs should omit the AWS field, got %#v", got)
	}
}
