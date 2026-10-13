package flowconfig

import (
	"bytes"
	"compress/gzip"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/jaysongiroux/hook_way/internal/graph"
)

func TestHTTPRequestResponseResourceBoundary(t *testing.T) {
	for _, tc := range []struct {
		name    string
		limit   int64
		size    int
		chunked bool
		status  int
	}{
		{"exact_fixed", 64, 64, false, 0},
		{"over_fixed", 64, 65, false, 502},
		{"exact_chunked", 64, 64, true, 0},
		{"over_chunked", 64, 65, true, 502},
		{"default_large_chunked", 0, 10<<20 + 1, true, 502},
		{"explicit_large_local_file", 12 << 20, 12 << 20, true, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var attempts atomic.Int32
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				attempts.Add(1)
				if !tc.chunked {
					w.Header().Set("Content-Length", strconv.Itoa(tc.size))
				}
				w.WriteHeader(http.StatusCreated)
				if tc.chunked {
					w.(http.Flusher).Flush()
				}
				chunk := bytes.Repeat([]byte{'x'}, min(tc.size, 64<<10))
				for remaining := tc.size; remaining > 0; {
					n, err := w.Write(chunk[:min(len(chunk), remaining)])
					if err != nil {
						return
					}
					remaining -= n
				}
			}))
			defer upstream.Close()
			n, err := newHTTPRequestNode("reply", HTTPRequestOptions{URL: upstream.URL, MaxResponseBytes: tc.limit, Retry: HTTPRetryOptions{MaxAttempts: 3, InitialBackoff: "1ms"}}, nil, nil)
			if err != nil {
				t.Fatal(err)
			}
			out, err := n.Run(context.Background(), retryEnvelope("{}"))
			if tc.status != 0 {
				var ne graph.NodeError
				if !errors.As(err, &ne) || ne.Status != tc.status {
					t.Fatalf("resource failure: %v", err)
				}
			} else if err != nil {
				t.Fatal(err)
			} else if got := out.(graph.Envelope).Upstream; got.Status != http.StatusCreated || len(got.Body) != tc.size {
				t.Fatalf("successful reply size/status: %d/%d", len(got.Body), got.Status)
			}
			if attempts.Load() != 1 {
				t.Fatalf("resource failure retried %d times", attempts.Load())
			}
		})
	}
}

func TestHTTPRequestDoesNotInflateCompressedResponse(t *testing.T) {
	var compressed bytes.Buffer
	w := gzip.NewWriter(&compressed)
	_, _ = w.Write(bytes.Repeat([]byte{'x'}, 12<<20))
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Accept-Encoding") != "" {
			t.Error("transport implicitly requested compression")
		}
		w.Header().Set("Content-Encoding", "gzip")
		_, _ = w.Write(compressed.Bytes())
	}))
	defer upstream.Close()
	n, err := newHTTPRequestNode("reply", HTTPRequestOptions{URL: upstream.URL, MaxResponseBytes: int64(compressed.Len())}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	out, err := n.Run(context.Background(), retryEnvelope("{}"))
	if err != nil {
		t.Fatal(err)
	}
	got := out.(graph.Envelope).Upstream
	if !bytes.Equal(got.Body, compressed.Bytes()) || got.Header.Get("Content-Encoding") != "gzip" {
		t.Fatal("compressed wire reply was expanded or encoding was removed")
	}
}

func TestResponseBindingStructureLimitRetainsSuccessfulReply(t *testing.T) {
	deep := strings.Repeat("[", 129) + "0" + strings.Repeat("]", 129)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(deep))
	}))
	defer upstream.Close()
	n, err := newHTTPRequestNode("reply", HTTPRequestOptions{URL: upstream.URL, ResponseBindings: &HTTPResponseBindings{Status: "status", Fields: map[string]string{"created_id": "id"}}}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	out, err := n.Run(context.Background(), retryEnvelope("{}"))
	if err != nil {
		t.Fatal(err)
	}
	env := out.(graph.Envelope)
	if env.Upstream.Status != http.StatusCreated || string(env.Upstream.Body) != deep || env.Upstream.SelectionError != "response_json_structure_limit" || env.Vars["status"] != "201" || env.Vars["created_id"] != "" {
		t.Fatal("selection limit discarded successful side-effect reply or selected fields")
	}
}
