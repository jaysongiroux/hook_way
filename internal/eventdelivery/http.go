package eventdelivery

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"
)

func signature(secret []byte, id, stamp string, body []byte) string {
	h := hmac.New(sha256.New, secret)
	_, _ = h.Write([]byte(id + "." + stamp + "."))
	_, _ = h.Write(body)
	return "v1," + base64.StdEncoding.EncodeToString(h.Sum(nil))
}
func (r *Runtime) send(ctx context.Context, url, id, subscription string, body, secret, previous []byte) (*http.Response, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		cancel()
		return nil, err
	}
	stamp := strconv.FormatInt(time.Now().Unix(), 10)
	sig := signature(secret, id, stamp, body)
	if len(previous) > 0 {
		sig += " " + signature(previous, id, stamp, body)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("webhook-id", id)
	req.Header.Set("webhook-timestamp", stamp)
	req.Header.Set("webhook-signature", sig)
	req.Header.Set("X-MCP-Subscription-Id", subscription)
	res, err := r.client.Do(req)
	if err != nil {
		cancel()
		return nil, err
	}
	// Copy only a bounded response; callers never expose its untrusted content.
	b, e := io.ReadAll(io.LimitReader(res.Body, 4097))
	_ = res.Body.Close()
	cancel()
	if e != nil || len(b) > 4096 {
		res.Body = io.NopCloser(strings.NewReader(""))
	} else {
		res.Body = io.NopCloser(bytes.NewReader(b))
	}
	return res, nil
}
func (r *Runtime) verifyCallback(ctx context.Context, owner, url, id string, secret []byte) error {
	keyBytes, _ := json.Marshal([]string{owner, url, base64.StdEncoding.EncodeToString(secret)})
	h := sha256.Sum256(keyBytes)
	cacheKey := hex.EncodeToString(h[:])
	r.mu.Lock()
	if r.verified == nil {
		r.verified = map[string]time.Time{}
	}
	until := r.verified[cacheKey]
	r.mu.Unlock()
	if time.Now().Before(until) {
		return nil
	}
	challenge := make([]byte, 32)
	if _, e := rand.Read(challenge); e != nil {
		return rpcError(-32603, "Challenge unavailable", "")
	}
	value := base64.RawURLEncoding.EncodeToString(challenge)
	body, _ := json.Marshal(map[string]string{"type": "verification", "challenge": value})
	res, e := r.send(ctx, url, "msg_verification_"+hex.EncodeToString(challenge), id, body, secret, nil)
	if e != nil {
		reason := "connection_refused"
		var ne net.Error
		if errors.As(e, &ne) && ne.Timeout() {
			reason = "timeout"
		} else if strings.Contains(strings.ToLower(e.Error()), "tls") {
			reason = "tls_error"
		}
		return rpcError(-32015, "CallbackEndpointError", reason)
	}
	defer func() { _ = res.Body.Close() }()
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		reason := "challenge_failed"
		if res.StatusCode >= 500 {
			reason = "http_5xx"
		} else if res.StatusCode >= 400 {
			reason = "http_4xx"
		}
		return rpcError(-32015, "CallbackEndpointError", reason)
	}
	var reply struct {
		Challenge string `json:"challenge"`
	}
	if json.NewDecoder(res.Body).Decode(&reply) != nil || subtle.ConstantTimeCompare([]byte(reply.Challenge), []byte(value)) != 1 {
		return rpcError(-32015, "CallbackEndpointError", "challenge_failed")
	}
	r.mu.Lock()
	for k, t := range r.verified {
		if !time.Now().Before(t) {
			delete(r.verified, k)
		}
	}
	if len(r.verified) < r.cfg.MaxSubscriptions {
		r.verified[cacheKey] = time.Now().Add(5 * time.Minute)
	}
	r.mu.Unlock()
	return nil
}
