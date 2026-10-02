package config

import (
	"encoding/json"
	"fmt"
	"strconv"
)

// BrokerMutableAttributes is the supported resource configuration surface.
// Omitted attributes are left under the operator's control. FIFO type and
// content deduplication are owned by the explicit FIFO configuration.
func BrokerMutableAttributes(kind string) map[string]bool {
	if kind == "sqs" {
		return map[string]bool{
			"VisibilityTimeout": true, "MessageRetentionPeriod": true, "DelaySeconds": true,
			"ReceiveMessageWaitTimeSeconds": true, "MaximumMessageSize": true,
			"RedrivePolicy": true, "RedriveAllowPolicy": true, "KmsMasterKeyId": true,
			"KmsDataKeyReusePeriodSeconds": true, "SqsManagedSseEnabled": true,
			"DeduplicationScope": true, "FifoThroughputLimit": true,
		}
	}
	if kind == "sns" {
		return map[string]bool{
			"KmsMasterKeyId": true, "DeliveryPolicy": true, "TracingConfig": true,
			"FifoThroughputScope": true, "ArchivePolicy": true,
		}
	}
	return nil
}

func ValidateBrokerAttributes(kind string, fifo bool, attrs map[string]string) error {
	allowed := BrokerMutableAttributes(kind)
	ranges := map[string][2]int{
		"VisibilityTimeout": {0, 43200}, "MessageRetentionPeriod": {60, 1209600},
		"DelaySeconds": {0, 900}, "ReceiveMessageWaitTimeSeconds": {0, 20},
		"MaximumMessageSize": {1024, 1048576}, "KmsDataKeyReusePeriodSeconds": {60, 86400},
	}
	for key, value := range attrs {
		if !allowed[key] {
			return fmt.Errorf("unsupported %s resource attribute %q", kind, key)
		}
		if len(value) == 0 || len(value) > 65536 {
			return fmt.Errorf("resource attribute %s must be nonempty and at most 64 KiB", key)
		}
		if bounds, ok := ranges[key]; ok {
			n, err := strconv.Atoi(value)
			if err != nil || n < bounds[0] || n > bounds[1] {
				return fmt.Errorf("resource attribute %s must be %d..%d", key, bounds[0], bounds[1])
			}
		}
		switch key {
		case "RedrivePolicy", "RedriveAllowPolicy", "DeliveryPolicy", "ArchivePolicy":
			if key == "ArchivePolicy" && !fifo {
				return fmt.Errorf("ArchivePolicy requires a FIFO topic")
			}
			var obj map[string]json.RawMessage
			if json.Unmarshal([]byte(value), &obj) != nil || obj == nil {
				return fmt.Errorf("resource attribute %s must be a JSON object", key)
			}
		case "SqsManagedSseEnabled":
			if value != "true" && value != "false" {
				return fmt.Errorf("SqsManagedSseEnabled must be true or false")
			}
		case "DeduplicationScope":
			if !fifo || (value != "queue" && value != "messageGroup") {
				return fmt.Errorf("DeduplicationScope requires FIFO and queue or messageGroup")
			}
		case "FifoThroughputLimit":
			if !fifo || (value != "perQueue" && value != "perMessageGroupId") {
				return fmt.Errorf("FifoThroughputLimit requires FIFO and perQueue or perMessageGroupId")
			}
		case "FifoThroughputScope":
			if !fifo || (value != "Topic" && value != "MessageGroup") {
				return fmt.Errorf("FifoThroughputScope requires FIFO and Topic or MessageGroup")
			}
		case "TracingConfig":
			if value != "PassThrough" && value != "Active" {
				return fmt.Errorf("TracingConfig must be PassThrough or Active")
			}
		}
	}
	if attrs["FifoThroughputLimit"] == "perMessageGroupId" && attrs["DeduplicationScope"] != "messageGroup" {
		return fmt.Errorf("perMessageGroupId throughput requires DeduplicationScope: messageGroup")
	}
	if attrs["KmsMasterKeyId"] != "" && attrs["SqsManagedSseEnabled"] == "true" {
		return fmt.Errorf("KMS and SQS managed encryption cannot both be enabled")
	}
	return nil
}
