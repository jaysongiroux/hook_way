package eventops

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"github.com/jaysongiroux/hook_way/internal/tlsfiles"
	"io"
	"net/http"
	"strings"
	"time"
)

type Client struct {
	client     *http.Client
	url, token string
}

func NewClient(base, token string, files tlsfiles.Files) (*Client, error) {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	if files.Configured() {
		cfg, e := files.Build("core tls")
		if e != nil {
			return nil, e
		}
		transport.TLSClientConfig = cfg
	}
	return &Client{client: &http.Client{Transport: transport, Timeout: 12 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}, url: strings.TrimRight(base, "/") + ControlPath, token: token}, nil
}
func (c *Client) Close() { c.client.CloseIdleConnections() }
func (c *Client) Call(ctx context.Context, q Request) (json.RawMessage, int, error) {
	raw, _ := json.Marshal(q)
	req, e := http.NewRequestWithContext(ctx, "POST", c.url, bytes.NewReader(raw))
	if e != nil {
		return nil, 0, errors.New("event control unavailable")
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("Content-Type", "application/json")
	res, e := c.client.Do(req)
	if e != nil {
		return nil, 0, errors.New("event control unavailable")
	}
	defer func() { _ = res.Body.Close() }()
	raw, e = io.ReadAll(io.LimitReader(res.Body, (2<<20)+1))
	if e != nil || len(raw) > 2<<20 || !json.Valid(raw) {
		return nil, res.StatusCode, errors.New("invalid event control response")
	}
	return raw, res.StatusCode, nil
}
