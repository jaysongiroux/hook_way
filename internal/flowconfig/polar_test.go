package flowconfig

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/jaysongiroux/hook_way/internal/auth"
	"github.com/jaysongiroux/hook_way/internal/graph"
)

func TestPolarAuthConfiguration(t *testing.T) {
	valid := AuthOptions{Scheme: auth.SchemeHMACSHA256Polar, Secret: "legacy!", SignatureHeader: "webhook-signature", TimestampHeader: "webhook-timestamp", MessageIDHeader: "webhook-id", TimestampUnit: "seconds"}
	if err := validateAuth(valid); err != nil {
		t.Fatal(err)
	}
	if AuthSpec(valid).TimestampTolerance != 5*time.Minute {
		t.Fatal("missing default tolerance")
	}
	for _, tc := range []struct {
		name   string
		change func(*AuthOptions)
	}{
		{"missing_signature", func(o *AuthOptions) { o.SignatureHeader = "" }},
		{"missing_timestamp", func(o *AuthOptions) { o.TimestampHeader = "" }},
		{"missing_id", func(o *AuthOptions) { o.MessageIDHeader = "" }},
		{"id_matches_signature", func(o *AuthOptions) { o.MessageIDHeader = "Webhook-Signature" }},
		{"id_matches_timestamp", func(o *AuthOptions) { o.MessageIDHeader = "Webhook-Timestamp" }},
		{"timestamp_matches_signature", func(o *AuthOptions) { o.TimestampHeader = "Webhook-Signature" }},
		{"query", func(o *AuthOptions) { o.Source = &AuthSourceOptions{Kind: "query", Key: "sig"} }},
		{"header_source_collision", func(o *AuthOptions) { o.Source = &AuthSourceOptions{Kind: "header", Key: "Webhook-Id"} }},
		{"invalid_id_header", func(o *AuthOptions) { o.MessageIDHeader = "bad header" }},
		{"other_scheme", func(o *AuthOptions) { o.Scheme = auth.SchemeHMACSHA256SlackV0 }},
		{"milliseconds", func(o *AuthOptions) { o.TimestampUnit = "milliseconds" }},
		{"age", func(o *AuthOptions) { o.TimestampPolicy = "age" }},
		{"unchecked", func(o *AuthOptions) { o.TimestampPolicy = "unchecked" }},
		{"reserialized", func(o *AuthOptions) { o.BodyRepresentation = "json_js" }},
		{"bearer", func(o *AuthOptions) { o.RemoveBearer = true }},
		{"zero_tolerance", func(o *AuthOptions) { o.TimestampTolerance = "0s" }},
		{"negative_tolerance", func(o *AuthOptions) { o.TimestampTolerance = "-1s" }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			o := valid
			tc.change(&o)
			if err := validateAuth(o); err == nil {
				t.Fatal("invalid options accepted")
			}
		})
	}
	valid.Source = &AuthSourceOptions{Kind: "header", Key: "X-Polar-Signature"}
	if err := validateAuth(valid); err != nil {
		t.Fatal("valid header override rejected", err)
	}
}

func TestPolarAuthHeadersAndIdentity(t *testing.T) {
	node := mustAuthNode(t, `scheme: hmac_sha256_polar
secret: legacy!
signature_header: webhook-signature
timestamp_header: webhook-timestamp
message_id_header: webhook-id
timestamp_unit: seconds
credential_id: polar-account
`)
	ts := strconv.FormatInt(time.Now().Unix(), 10)
	body := []byte("{ \"type\": \"future.event\" }\n")
	mac := hmac.New(sha256.New, []byte("legacy!"))
	_, _ = mac.Write([]byte("event-1." + ts + "."))
	_, _ = mac.Write(body)
	header := http.Header{}
	header.Set("webhook-id", "event-1")
	header.Set("webhook-timestamp", ts)
	header.Set("webhook-signature", "v1,"+base64.StdEncoding.EncodeToString(mac.Sum(nil)))
	in := graph.RawRequest{Head: graph.RequestHead{Header: header}, Body: body}
	out, err := node.Run(context.Background(), in)
	if err != nil {
		t.Fatal(err)
	}
	verified := out.(graph.Verified)
	if verified.CredentialID != "polar-account" || verified.Identity != auth.SchemeHMACSHA256Polar {
		t.Fatal("identity lost", verified)
	}
	for _, name := range []string{"webhook-id", "webhook-timestamp", "webhook-signature"} {
		for _, duplicate := range []bool{false, true} {
			modified := in
			modified.Head.Header = header.Clone()
			if duplicate {
				modified.Head.Header.Add(name, header.Get(name))
			} else {
				modified.Head.Header.Del(name)
			}
			_, err := node.Run(context.Background(), modified)
			var failure graph.NodeError
			if !errors.As(err, &failure) || failure.Status != http.StatusUnauthorized {
				t.Fatalf("header %s duplicate=%v: %v", name, duplicate, err)
			}
			if strings.Contains(err.Error(), "legacy!") {
				t.Fatal("secret leaked")
			}
		}
	}
}
