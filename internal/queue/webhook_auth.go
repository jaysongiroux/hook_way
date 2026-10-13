package queue

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"time"
)

// webhookAuth is the resolved outbound-auth settings for a webhook destination.
type webhookAuth struct {
	Type          string // none | api_key | hmac | proxy
	Header        string
	Value         string
	Secret        string
	Scheme        string // hmac_sha256_ts_dot (default) | hmac_sha256_ts_colon | hmac_sha256_hex
	EventIDHeader string
}

// signWebhookHMAC computes the signature header value for the given scheme.
// Default scheme hmac_sha256_ts_dot signs "<ts>.<body>" and emits "t=<ts>,v1=<hex>".
func signWebhookHMAC(scheme, secret string, ts int64, body []byte) string {
	mac := hmac.New(sha256.New, []byte(secret))
	switch scheme {
	case "", "hmac_sha256_ts_dot":
		_, _ = fmt.Fprintf(mac, "%d.%s", ts, body)
		return fmt.Sprintf("t=%d,v1=%s", ts, hex.EncodeToString(mac.Sum(nil)))
	case "hmac_sha256_ts_colon":
		_, _ = fmt.Fprintf(mac, "%d:%s", ts, body)
		return fmt.Sprintf("ts=%d;h1=%s", ts, hex.EncodeToString(mac.Sum(nil)))
	case "hmac_sha256_hex":
		mac.Write(body)
		return "sha256=" + hex.EncodeToString(mac.Sum(nil))
	default:
		// validation rejects unknown schemes; fall back to the default format.
		_, _ = fmt.Fprintf(mac, "%d.%s", ts, body)
		return fmt.Sprintf("t=%d,v1=%s", ts, hex.EncodeToString(mac.Sum(nil)))
	}
}

// applyWebhookAuth sets outbound auth headers on req. none/proxy are no-ops.
func applyWebhookAuth(req *http.Request, a webhookAuth, body []byte, eventID string, now time.Time) {
	switch a.Type {
	case "api_key":
		if a.Header != "" {
			req.Header.Set(a.Header, a.Value)
		}
	case "hmac":
		header := a.Header
		if header == "" {
			header = "X-Hookway-Signature"
		}
		req.Header.Set(header, signWebhookHMAC(a.Scheme, a.Secret, now.Unix(), body))
		eidHeader := a.EventIDHeader
		if eidHeader == "" {
			eidHeader = "X-Hookway-Event-Id"
		}
		if eventID != "" {
			req.Header.Set(eidHeader, eventID)
		}
	default:
		// none, proxy, "" -> no auth added
	}
}
