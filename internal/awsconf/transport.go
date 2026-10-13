package awsconf

import (
	"context"
	"errors"
	"net"
	"net/http"
	"os"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awshttp "github.com/aws/aws-sdk-go-v2/aws/transport/http"
)

// OwnHTTPClient must run in a service constructor's final options callback,
// after the SDK has resolved service/default-mode dial, TLS and read timeouts.
// It gives that service a private pool and cleanup of the actual transport.
// GetTransport returns a clone; closing that clone would not close live pools.
// Injected non-buildable clients remain caller-owned and unchanged.
func OwnHTTPClient(client aws.HTTPClient, admitted int) (aws.HTTPClient, func()) {
	buildable, ok := client.(*awshttp.BuildableClient)
	if !ok {
		return client, func() {}
	}
	budget := IdleBudget(admitted)
	tr := buildable.GetTransport()
	tr.MaxIdleConnsPerHost = budget
	tr.MaxIdleConns = 2 * budget
	// Freeze supplies the SDK's timeout and redirect policy, but its opaque
	// redirect transport does not forward CloseIdleConnections. Own the clone
	// explicitly instead; install the resolved rolling read timeout as the SDK
	// does when building its client (aws-sdk-go-v2 v1.47.0).
	if timeout, set := buildable.GetReadTimeout(); set && timeout > 0 {
		dial := tr.DialContext
		if dial == nil {
			dial = buildable.GetDialer().DialContext
		}
		tr.DialContext = func(ctx context.Context, network, addr string) (net.Conn, error) {
			conn, err := dial(ctx, network, addr)
			if err != nil {
				return nil, err
			}
			return &readDeadlineConn{Conn: conn, timeout: timeout}, nil
		}
	}
	owned := *buildable.Freeze().(*http.Client)
	owned.Transport = redirectTransport{tr}
	return &owned, tr.CloseIdleConnections
}

// Forward cleanup and preserve the SDK's 301/302-without-Location handling.
// The SDK-provided CheckRedirect recognizes this sentinel and returns the
// original response rather than Go's opaque missing-Location error.
type redirectTransport struct{ *http.Transport }

func (t redirectTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	resp, err := t.Transport.RoundTrip(r)
	if err == nil && (resp.StatusCode == 301 || resp.StatusCode == 302) && resp.Header.Get("Location") == "" {
		resp.Header.Set("Location", "https://amazonaws.com/badhttpredirectlocation")
	}
	return resp, err
}

type readDeadlineConn struct {
	net.Conn
	timeout time.Duration
}

func (c *readDeadlineConn) Read(p []byte) (int, error) {
	if err := c.SetReadDeadline(time.Now().Add(c.timeout)); err != nil {
		return 0, err
	}
	n, err := c.Conn.Read(p)
	if errors.Is(err, os.ErrDeadlineExceeded) {
		return n, &awshttp.ResponseTimeoutError{TimeoutDur: c.timeout}
	}
	return n, err
}

// IdleBudget bounds retention per native service client, not active requests.
// Small deployments follow admission; unlimited/standalone callers retain 64
// per host. The ceiling bounds retained sockets even for large admission limits.
// Each destination/store owns its budget; total retention is their sum.
func IdleBudget(admitted int) int {
	if admitted <= 0 {
		return 64
	}
	return min(admitted, 256)
}
