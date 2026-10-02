package awsconf

import (
	"context"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awshttp "github.com/aws/aws-sdk-go-v2/aws/transport/http"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
)

func TestOwnedSDKPoolReusesTLSConnectionsAndClosesActualIdleTransport(t *testing.T) {
	const parallel = 16
	started := make(chan struct{}, parallel)
	gates := []chan struct{}{make(chan struct{}), make(chan struct{}), make(chan struct{})}
	var opened atomic.Int32
	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		wave, _ := strconv.Atoi(r.Header.Get("X-Wave"))
		started <- struct{}{}
		select {
		case <-gates[wave]:
			_, _ = io.WriteString(w, "synthetic")
		case <-r.Context().Done():
		}
	}))
	srv.Config.ConnState = func(_ net.Conn, state http.ConnState) {
		if state == http.StateNew {
			opened.Add(1)
		}
	}
	srv.StartTLS()
	defer srv.Close()
	base := awshttp.NewBuildableClient().WithTimeout(5 * time.Second).WithTransportOptions(func(tr *http.Transport) {
		tr.TLSClientConfig = srv.Client().Transport.(*http.Transport).TLSClientConfig.Clone()
		tr.ResponseHeaderTimeout = 4 * time.Second
	})
	owned, closeIdle := OwnHTTPClient(base, parallel)
	defer closeIdle()
	client := owned.(*http.Client)
	tr := client.Transport.(redirectTransport).Transport
	if client.Timeout != 5*time.Second || tr.ResponseHeaderTimeout != 4*time.Second || tr.MaxConnsPerHost != base.GetTransport().MaxConnsPerHost || tr.IdleConnTimeout != base.GetTransport().IdleConnTimeout {
		t.Fatal("SDK timeouts/active connection policy changed")
	}
	if base.GetTransport().MaxIdleConnsPerHost == parallel {
		t.Fatal("mutated caller's SDK client")
	}
	wave := func(number int, closeWhileActive bool) {
		t.Helper()
		var wg sync.WaitGroup
		for range parallel {
			wg.Add(1)
			go func() {
				defer wg.Done()
				req, _ := http.NewRequestWithContext(t.Context(), "GET", srv.URL, nil)
				req.Header.Set("X-Wave", strconv.Itoa(number))
				resp, err := owned.Do(req)
				if err != nil {
					t.Error(err)
					return
				}
				data, err := io.ReadAll(resp.Body)
				_ = resp.Body.Close()
				if err != nil || string(data) != "synthetic" {
					t.Error("TLS body changed", err)
				}
			}()
		}
		for range parallel {
			select {
			case <-started:
			case <-time.After(10 * time.Second):
				close(gates[number])
				wg.Wait()
				t.Fatal("requests did not reach TLS target")
			}
		}
		if closeWhileActive {
			closeIdle()
		}
		close(gates[number])
		wg.Wait()
	}
	wave(0, false)
	first := opened.Load()
	wave(1, true) // Closing idle pools must not abort currently active requests.
	if first != parallel || opened.Load() != first {
		t.Fatal("warm wave did not reuse connections", first, opened.Load())
	}
	closeIdle() // Cleanup must operate on the live pool, not GetTransport's clone.
	wave(2, false)
	if opened.Load() != first+parallel {
		t.Fatal("idle cleanup failed to release live connections", opened.Load())
	}
}

func TestOwnedSDKClientPreservesReadTimeoutAndCancellation(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
		w.(http.Flusher).Flush()
		<-r.Context().Done()
	}))
	defer srv.Close()
	client, closeIdle := OwnHTTPClient(awshttp.NewBuildableClient().WithReadTimeout(20*time.Millisecond), 32)
	defer closeIdle()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, "GET", srv.URL, nil)
	resp, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_, err = io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if err == nil || ctx.Err() != nil {
		t.Fatal("SDK read timeout lost", err, ctx.Err())
	}
	cancel()
	if resp, err = client.Do(req); err == nil {
		_ = resp.Body.Close()
		t.Fatal("cancelled request accepted")
	}
}

func TestInjectedClientRemainsCallerOwned(t *testing.T) {
	client := &http.Client{Timeout: 3 * time.Second}
	owned, closeIdle := OwnHTTPClient(client, 256)
	closeIdle()
	if owned != client || client.Timeout != 3*time.Second {
		t.Fatal("injected client changed")
	}
}

func TestIdleRetentionBudgetIsFinite(t *testing.T) {
	for _, tc := range []struct{ admitted, want int }{{-1, 64}, {0, 64}, {1, 1}, {4, 4}, {64, 64}, {256, 256}, {512, 256}, {1024, 256}} {
		client, cleanup := OwnHTTPClient(awshttp.NewBuildableClient(), tc.admitted)
		tr := client.(*http.Client).Transport.(redirectTransport).Transport
		if tr.MaxIdleConnsPerHost != tc.want || tr.MaxIdleConns != 2*tc.want || tr.MaxConnsPerHost != awshttp.DefaultHTTPTransportMaxConnsPerHost || tr.IdleConnTimeout <= 0 {
			t.Fatal("incorrect bounded retention or hidden active cap", tc, tr.MaxIdleConnsPerHost, tr.MaxIdleConns)
		}
		cleanup()
	}
}

func TestOwnedClientPreservesSDKRedirectResponsesAndTokenScrubbing(t *testing.T) {
	var received atomic.Int32
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		received.Add(1)
		data, _ := io.ReadAll(r.Body)
		if r.Method != "POST" || string(data) != "payload" || r.Header.Get("X-Amz-Security-Token") != "" {
			t.Error("SDK redirect method/body/token policy changed")
		}
		w.WriteHeader(201)
	}))
	defer target.Close()
	source := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		code, _ := strconv.Atoi(r.URL.Path[1:])
		if r.Header.Get("X-Test-No-Location") == "" {
			w.Header().Set("Location", target.URL)
		}
		w.WriteHeader(code)
	}))
	defer source.Close()
	base := awshttp.NewBuildableClient().WithTimeout(time.Second)
	owned, cleanup := OwnHTTPClient(base, 16)
	defer cleanup()
	for _, code := range []int{301, 302, 303, 307, 308} {
		for _, missing := range []bool{false, true} {
			var statuses []int
			var locations []string
			for _, client := range []aws.HTTPClient{base.Freeze(), owned} {
				req, _ := http.NewRequestWithContext(t.Context(), "POST", source.URL+"/"+strconv.Itoa(code), strings.NewReader("payload"))
				req.Header.Set("X-Amz-Security-Token", "synthetic-session-token")
				if missing {
					req.Header.Set("X-Test-No-Location", "yes")
				}
				resp, err := client.Do(req)
				if err != nil {
					t.Fatal("redirect behavior changed", code, missing, err)
				}
				_, _ = io.Copy(io.Discard, resp.Body)
				_ = resp.Body.Close()
				statuses = append(statuses, resp.StatusCode)
				locations = append(locations, resp.Header.Get("Location"))
			}
			if statuses[0] != statuses[1] || locations[0] != locations[1] {
				t.Fatal("SDK redirect parity failed", code, missing, statuses, locations)
			}
		}
	}
	if received.Load() != 4 {
		t.Fatal("unexpected redirects followed", received.Load())
	}
}

func TestOwnershipRunsAfterServiceTimeoutResolution(t *testing.T) {
	cfg := aws.Config{Region: "us-east-1"}
	baseline := dynamodb.NewFromConfig(cfg).Options().HTTPClient.(*awshttp.BuildableClient)
	wantRead, wantSet := baseline.GetReadTimeout()
	var resolved *awshttp.BuildableClient
	var cleanup func()
	client := dynamodb.NewFromConfig(cfg, func(o *dynamodb.Options) {
		resolved = o.HTTPClient.(*awshttp.BuildableClient)
		o.HTTPClient, cleanup = OwnHTTPClient(o.HTTPClient, 128)
	})
	defer cleanup()
	if timeout, set := resolved.GetReadTimeout(); set != wantSet || timeout != wantRead {
		t.Fatal("DynamoDB service read timeout was not resolved first", timeout, set, wantRead, wantSet)
	}
	owned := client.Options().HTTPClient.(*http.Client)
	tr := owned.Transport.(redirectTransport).Transport
	if owned.Timeout != resolved.GetTimeout() || tr.TLSHandshakeTimeout != resolved.GetTransport().TLSHandshakeTimeout || tr.TLSClientConfig.MinVersion != resolved.GetTransport().TLSClientConfig.MinVersion {
		t.Fatal("service TLS/timeout settings changed")
	}
}
