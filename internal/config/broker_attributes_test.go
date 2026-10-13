package config

import "testing"

func TestBrokerResourceAttributes(t *testing.T) {
	for _, tc := range []struct {
		kind  string
		fifo  bool
		attrs map[string]string
		valid bool
	}{
		{"sqs", false, map[string]string{"RedrivePolicy": `{"deadLetterTargetArn":"arn:aws:sqs:us-east-1:000000000000:dlq","maxReceiveCount":5}`, "KmsMasterKeyId": "alias/sample"}, true},
		{"sqs", true, map[string]string{"DeduplicationScope": "messageGroup", "FifoThroughputLimit": "perMessageGroupId"}, true},
		{"sns", true, map[string]string{"FifoThroughputScope": "MessageGroup", "ArchivePolicy": `{"MessageRetentionPeriod":7}`}, true},
		{"sqs", false, map[string]string{"DeduplicationScope": "messageGroup"}, false},
		{"sns", false, map[string]string{"ArchivePolicy": `{}`}, false},
		{"sqs", false, map[string]string{"DelaySeconds": "901"}, false},
		{"sqs", false, map[string]string{"FifoQueue": "true"}, false},
		{"sns", false, map[string]string{"DeliveryPolicy": `[]`}, false},
		{"sqs", false, map[string]string{"KmsMasterKeyId": "alias/sample", "SqsManagedSseEnabled": "true"}, false},
	} {
		if err := ValidateBrokerAttributes(tc.kind, tc.fifo, tc.attrs); (err == nil) != tc.valid {
			t.Errorf("%+v: %v", tc, err)
		}
	}
}
