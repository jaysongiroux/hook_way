package httpapi

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// This uses the real CLI and TCP listener, with synthetic files only. It does
// not call vendor infrastructure or use any broker consumer.
func TestProxyStandaloneContract(t *testing.T) {
	if testing.Short() {
		t.Skip("standalone binary and large-file acceptance")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	var calls atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.Header.Get("X-Auth") != "" {
			t.Error("Hookway auth header reached upstream")
		}
		if !strings.HasPrefix(r.URL.Path, "/") || strings.HasPrefix(r.URL.Path, "/proxy/files/") {
			t.Error("ingress prefix not removed")
		}
		if q := r.URL.Query()["q"]; len(q) != 2 || q[0] != "" || q[1] != "a+b/c" {
			t.Errorf("query = %v", r.URL.Query())
		}
		w.Header().Set("Set-Cookie", "private=1")
		w.Header().Set("Content-Disposition", `attachment; filename="sample.bin"`)
		switch r.URL.Path {
		case "/204":
			w.WriteHeader(204)
			return
		case "/304":
			w.Header().Set("Content-Length", "123")
			w.WriteHeader(304)
			return
		case "/404":
			w.WriteHeader(404)
			_, _ = io.WriteString(w, "missing")
			return
		case "/503":
			w.WriteHeader(503)
			_, _ = io.WriteString(w, "unavailable")
			return
		case "/oversize":
			_, _ = io.Copy(w, io.LimitReader(infiniteByteReader{}, (20<<20)+1))
			return
		}
		if r.Method == "HEAD" {
			w.Header().Set("Content-Length", "123")
			w.WriteHeader(200)
			return
		}
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Error(err)
			return
		}
		w.Header().Set("Content-Type", r.Header.Get("Content-Type"))
		_, _ = w.Write(body)
	}))
	defer upstream.Close()
	temp := t.TempDir()
	binary := filepath.Join(temp, "hookway")
	if output, err := exec.CommandContext(ctx, "go", "build", "-o", binary, "../../cmd/hookway").CombinedOutput(); err != nil { // #nosec G204 -- Fixed test commands and test-owned binaries; no shell or request input.
		t.Fatalf("build: %v %s", err, output)
	}
	listener, err := (&net.ListenConfig{}).Listen(ctx, "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := listener.Addr().String()
	_ = listener.Close()
	src, err := os.ReadFile("../../examples/proxy/request-contract.yaml")
	if err != nil {
		t.Fatal(err)
	}
	source := strings.Replace(string(src), `addr: ":8080"`, `addr: "`+addr+`"`, 1)
	// Large fixtures are sequential; a higher rate limit avoids coupling
	// transport assertions to the example's independent ingress rate budget.
	source = strings.Replace(source, "rpm: 1200, burst: 20", "rpm: 60000, burst: 100", 1)
	path := filepath.Join(temp, "config.yaml")
	if err := os.WriteFile(path, []byte(source), 0600); err != nil { // #nosec G703 -- Destination is inside the test-owned temporary directory.
		t.Fatal(err)
	}
	log, err := os.Create(filepath.Join(temp, "server.log")) // #nosec G304 -- Controlled fixture name or test-owned temporary path.
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = log.Close() }()
	command := exec.CommandContext(ctx, binary, "serve", "--config", path) // #nosec G204 -- Fixed test commands and test-owned binaries; no shell or request input.
	command.Env = append(os.Environ(), "UPSTREAM_BASE="+upstream.URL, "PROXY_TOKEN=synthetic")
	command.Stdout = log
	command.Stderr = log
	if err := command.Start(); err != nil {
		t.Fatal(err)
	}
	exited := make(chan error, 1)
	go func() { exited <- command.Wait() }()
	t.Cleanup(func() {
		_ = command.Process.Kill()
		select {
		case <-exited:
		case <-time.After(5 * time.Second):
			t.Error("server did not exit")
		}
	})
	client := &http.Client{Timeout: 30 * time.Second}
	base := "http://" + addr
	ready := false
	for deadline := time.Now().Add(20 * time.Second); time.Now().Before(deadline); {
		probe, probeErr := http.NewRequestWithContext(ctx, http.MethodGet, base+"/readyz", nil)
		if probeErr != nil {
			t.Fatal(probeErr)
		}
		resp, err := client.Do(probe)
		if err == nil {
			_ = resp.Body.Close()
			if resp.StatusCode == 200 {
				ready = true
				break
			}
		}
		select {
		case err := <-exited:
			exited <- err
			data, _ := os.ReadFile(log.Name())
			t.Fatalf("server exited: %v %s", err, data)
		default:
		}
		time.Sleep(50 * time.Millisecond)
	}
	if !ready {
		data, _ := os.ReadFile(log.Name())
		t.Fatalf("server not ready: %s", data)
	}
	type responseMetadata struct {
		StatusCode int
		Header     http.Header
	}
	request := func(method, route, contentType string, body []byte) (responseMetadata, []byte) {
		t.Helper()
		req, err := http.NewRequestWithContext(ctx, method, base+"/proxy/files/"+route+"?q=&q=a%2Bb%2Fc", bytes.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("X-Auth", "synthetic")
		if contentType != "" {
			req.Header.Set("Content-Type", contentType)
		}
		resp, err := client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		got, err := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		if err != nil {
			t.Fatal(err)
		}
		return responseMetadata{StatusCode: resp.StatusCode, Header: resp.Header}, got
	}
	for _, method := range []string{"GET", "HEAD", "POST", "PUT", "PATCH", "DELETE", "OPTIONS"} {
		resp, body := request(method, "empty", "", nil)
		if resp.StatusCode != 200 || len(body) != 0 {
			t.Fatalf("empty %s: %d %q", method, resp.StatusCode, body)
		}
		if method == "HEAD" && resp.Header.Get("Content-Length") != "123" {
			t.Fatal("HEAD representation length lost")
		}
	}
	large := bytes.Repeat([]byte{0xa5}, 12<<20)
	t.Run("pdf", func(t *testing.T) {
		large[0] = '%'
		large[1] = 'P'
		large[2] = 'D'
		large[3] = 'F'
		resp, body := request("POST", "pdf", "application/pdf", large)
		if resp.StatusCode != 200 || sha256.Sum256(body) != sha256.Sum256(large) {
			t.Fatal("12 MiB file changed")
		}
		if resp.Header.Get("Set-Cookie") != "" || resp.Header.Get("Content-Disposition") == "" {
			t.Fatal("response header policy changed")
		}
	})
	t.Run("xml", func(t *testing.T) {
		body := []byte("<sample>" + strings.Repeat("x", (12<<20)-17) + "</sample>")
		resp, got := request("PUT", "xml", "application/xml", body)
		if resp.StatusCode != 200 || !bytes.Equal(body, got) {
			t.Fatal("raw XML changed")
		}
	})
	t.Run("docx", func(t *testing.T) {
		var b bytes.Buffer
		z := zip.NewWriter(&b)
		entry, err := z.CreateHeader(&zip.FileHeader{Name: "word/document.xml", Method: zip.Store})
		if err != nil {
			t.Fatal(err)
		}
		_, _ = entry.Write([]byte("<sample>" + strings.Repeat("x", (12<<20)-512) + "</sample>"))
		_ = z.Close()
		resp, got := request("PATCH", "docx", "application/vnd.openxmlformats-officedocument.wordprocessingml.document", b.Bytes())
		if resp.StatusCode != 200 || sha256.Sum256(got) != sha256.Sum256(b.Bytes()) {
			t.Fatal("ZIP document bytes changed")
		}
	})
	t.Run("multipart", func(t *testing.T) {
		boundary := "synthetic-boundary"
		body := append([]byte("--"+boundary+"\r\nContent-Disposition: form-data; name=\"document\"; filename=\"sample.pdf\"\r\nContent-Type: application/pdf\r\n\r\n"), large...)
		body = append(body, []byte("\r\n--"+boundary+"--\r\n")...)
		contentType := "multipart/form-data; boundary=" + boundary
		resp, got := request("POST", "multipart", contentType, body)
		if resp.StatusCode != 200 || !bytes.Equal(body, got) || resp.Header.Get("Content-Type") != contentType {
			t.Fatal("multipart body or boundary changed")
		}
	})
	t.Run("base64-json", func(t *testing.T) {
		body := []byte(`{"content":"` + base64.StdEncoding.EncodeToString(large) + `"}`)
		resp, got := request("POST", "base64", "application/json", body)
		if resp.StatusCode != 200 || !bytes.Equal(body, got) {
			t.Fatal("base64 JSON changed")
		}
	})
	for _, code := range []int{204, 304, 404, 503} {
		before := calls.Load()
		resp, body := request("POST", fmt.Sprint(code), "", nil)
		if resp.StatusCode != code || ((code == 204 || code == 304) && len(body) > 0) || calls.Load() != before+1 {
			t.Fatalf("status %d: %d body=%q calls=%d", code, resp.StatusCode, body, calls.Load()-before)
		}
	}
	t.Run("wire-cap", func(t *testing.T) {
		for _, size := range []int{(20 << 20) - 1, 20 << 20, (20 << 20) + 1} {
			before := calls.Load()
			resp, body := request("POST", "wire", "application/octet-stream", bytes.Repeat([]byte{'x'}, size))
			if size > 20<<20 {
				if resp.StatusCode != 413 || calls.Load() != before {
					t.Fatal("wire overrun reached upstream")
				}
			} else if resp.StatusCode != 200 || len(body) != size {
				t.Fatalf("wire boundary %d: %d %d", size, resp.StatusCode, len(body))
			}
		}
	})
	resp, _ := request("POST", "oversize", "", nil)
	if resp.StatusCode != 502 {
		t.Fatalf("upstream overrun = %d", resp.StatusCode)
	}
}

type infiniteByteReader struct{}

func (infiniteByteReader) Read(p []byte) (int, error) {
	for i := range p {
		p[i] = 'x'
	}
	return len(p), nil
}
