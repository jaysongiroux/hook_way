package conformance

import (
	"context"
	"encoding/base64"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jaysongiroux/hook_way/internal/queue"
)

type polarRecordingPublisher struct {
	testPublisher
	last queue.Message
}

func (p *polarRecordingPublisher) Publish(ctx context.Context, destination, target string, msg queue.Message) error {
	if err := p.testPublisher.Publish(ctx, destination, target, msg); err != nil {
		return err
	}
	p.last = msg
	return nil
}

func TestPolarRecipeBothSigningFormatsAndHeaderBinding(t *testing.T) {
	yaml, paths := providerRecipe(t)
	secret := "whsec_" + base64.StdEncoding.EncodeToString([]byte("polar-current-secret-key"))
	if err := os.WriteFile(paths["polar"], []byte(secret), 0600); err != nil {
		t.Fatal(err)
	}
	pub := &polarRecordingPublisher{}
	srv := newServer(t, yaml, pub)
	for _, mode := range []string{"polar", "polar_standard"} {
		t.Run(mode, func(t *testing.T) {
			// Auth is independent of event schemas, including future Polar types.
			body := "{ \"type\": \"future.event\", \"data\": {\"text\":\"café\"} }\n"
			request := func() *http.Request { return providerRequest(mode, secret, body, time.Now()) }
			before := pub.published
			for _, header := range []string{"webhook-id", "webhook-timestamp", "webhook-signature"} {
				for _, duplicate := range []bool{false, true} {
					req := request()
					if duplicate {
						req.Header.Add(header, req.Header.Get(header))
					} else {
						req.Header.Del(header)
					}
					if got := post(srv, req).Code; got != 401 {
						t.Fatalf("%s duplicate=%v returned %d", header, duplicate, got)
					}
				}
			}
			req := request()
			req.Header.Set("webhook-id", "changed-id")
			if got := post(srv, req).Code; got != 401 {
				t.Fatal("changed ID accepted", got)
			}
			if pub.published != before {
				t.Fatal("invalid request published")
			}
			if got := post(srv, request()).Code; got != 200 {
				t.Fatal("valid request rejected", got)
			}
			// Envelope metadata is a lowercase-keyed map, not canonical HTTP headers.
			headers := map[string][]string(pub.last.Headers)
			ids := headers["webhook-id"]
			if len(ids) != 1 || ids[0] != "polar-event-1" || string(pub.last.RawBody) != body {
				t.Fatal("message ID or body lost", pub.last)
			}
			if len(headers["webhook-signature"]) != 0 {
				t.Fatal("signature should not be forwarded by recipe")
			}
			if got := post(srv, request()).Code; got != 200 || pub.published != before+2 {
				t.Fatal("duplicates must reach the consumer for deduplication", got)
			}
		})
	}
	// Existing per-flow response customization also applies to Polar failures.
	yaml = strings.Replace(yaml, "  polar:\n    responses:", "  polar:\n    responses:\n      auth_error: {status: 403, body: invalid}", 1)
	override := newServer(t, yaml, &testPublisher{})
	if got := post(override, providerRequest("polar", "wrong", `{}`, time.Now())).Code; got != 403 {
		t.Fatal("auth response override lost", got)
	}
}
