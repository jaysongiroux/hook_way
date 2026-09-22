package config

import "testing"

// webhookDeliveryYAML builds a config with a webhook destination "notify" and a
// non-webhook DLQ destination "dlq", letting each test tweak the webhook block.
func webhookDeliveryYAML(webhookBlock string) string {
	return `
server:
  addr: ":8080"
  max_body_bytes: 1048576
destinations:
  notify:
    type: "webhook"
    webhook:
` + webhookBlock + `
  dlq:
    type: "rabbitmq"
    rabbitmq:
      url: "amqp://guest:guest@localhost:5672/"
      exchange: "hookway.dlq"
      exchange_type: "direct"
`
}

func TestWebhookParsesDeliveryModeAndDeadLetter(t *testing.T) {
	body := webhookDeliveryYAML(`      url: "https://x.example.com/hooks"
      delivery_mode: "async"
      max_queue: 2000
      dead_letter:
        destination: "dlq"
        target: "webhook-dead-letters"`)
	cfg, err := loadSharedYAML(t, body)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	w := cfg.Destinations["notify"].Webhook
	if w.DeliveryMode != "async" {
		t.Fatalf("DeliveryMode = %q, want async", w.DeliveryMode)
	}
	if w.MaxQueue != 2000 {
		t.Fatalf("MaxQueue = %d, want 2000", w.MaxQueue)
	}
	if w.DeadLetter.Destination != "dlq" || w.DeadLetter.Target != "webhook-dead-letters" {
		t.Fatalf("DeadLetter = %#v", w.DeadLetter)
	}
}

func TestWebhookValidationMatrix(t *testing.T) {
	cases := []struct {
		name        string
		webhook     string
		wantField   string // "" => expect valid
		wantNoIssue bool
	}{
		{
			name:        "valid async with dead_letter",
			webhook:     `      url: "https://x/h"` + "\n      delivery_mode: \"async\"\n      dead_letter:\n        destination: \"dlq\"\n        target: \"t\"",
			wantNoIssue: true,
		},
		{
			name:      "bad delivery_mode",
			webhook:   `      url: "https://x/h"` + "\n      delivery_mode: \"eventually\"",
			wantField: "destinations.notify.webhook.delivery_mode",
		},
		{
			name:      "dead_letter with sync",
			webhook:   `      url: "https://x/h"` + "\n      delivery_mode: \"sync\"\n      dead_letter:\n        destination: \"dlq\"\n        target: \"t\"",
			wantField: "destinations.notify.webhook.dead_letter",
		},
		{
			name:      "dead_letter destination without target",
			webhook:   `      url: "https://x/h"` + "\n      dead_letter:\n        destination: \"dlq\"",
			wantField: "destinations.notify.webhook.dead_letter",
		},
		{
			name:      "dead_letter target without destination",
			webhook:   `      url: "https://x/h"` + "\n      dead_letter:\n        target: \"t\"",
			wantField: "destinations.notify.webhook.dead_letter",
		},
		{
			name:      "unknown dead_letter destination",
			webhook:   `      url: "https://x/h"` + "\n      dead_letter:\n        destination: \"nope\"\n        target: \"t\"",
			wantField: "destinations.notify.webhook.dead_letter.destination",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			issues := sharedIssues(webhookDeliveryYAML(tc.webhook))
			if tc.wantNoIssue {
				for _, iss := range issues {
					if iss.Field == "destinations.notify.webhook.delivery_mode" ||
						iss.Field == "destinations.notify.webhook.dead_letter" ||
						iss.Field == "destinations.notify.webhook.dead_letter.destination" {
						t.Fatalf("expected no webhook delivery issue, got %+v", issues)
					}
				}
				return
			}
			if !hasIssue(issues, tc.wantField) {
				t.Fatalf("expected issue %q, got %+v", tc.wantField, issues)
			}
		})
	}
}

func TestWebhookRejectsWebhookTypedDeadLetterDestination(t *testing.T) {
	body := `
server:
  addr: ":8080"
  max_body_bytes: 1048576
destinations:
  notify:
    type: "webhook"
    webhook:
      url: "https://x/h"
      dead_letter:
        destination: "other"
        target: "t"
  other:
    type: "webhook"
    webhook:
      url: "https://y/h"
`
	issues := sharedIssues(body)
	if !hasIssue(issues, "destinations.notify.webhook.dead_letter.destination") {
		t.Fatalf("expected webhook-typed DLQ destination to be rejected, got %+v", issues)
	}
}
