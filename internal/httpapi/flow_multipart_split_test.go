package httpapi

import (
	"bytes"
	"context"
	"encoding/base64"
	"log/slog"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/jaysongiroux/hook_way/internal/graph"
	"github.com/jaysongiroux/hook_way/internal/queue"
)

// multipartSplitYAML fans out a multipart upload: object payload to the
// archive destination, JSON envelope (fields + file metadata) to events.
const multipartSplitYAML = `
server:
  addr: ":8080"
  max_body_bytes: 8388608
destinations:
  archive:
    type: s3
    s3:
      region: us-east-1
      bucket: test-bucket
  events:
    type: webhook
    webhook:
      url: "https://example.com/info-stub"
flows:
  upload_split:
    nodes:
      - {id: in, type: http_trigger, path: /webhooks/upload-split, methods: [POST]}
      - {id: body, type: read_body, max_bytes: 8388608}
      - {id: auth, type: auth, scheme: hmac_sha256_hex, secret: testsecret, signature_header: X-Hub-Signature-256}
      - id: env
        type: envelope
        body_format: multipart
        multipart:
          max_parts: 4
          max_part_bytes: 2097152
          include_content: true
      - id: for_s3
        type: code
        timeout: 50ms
        script: |
          const files = (input.body && input.body.files) || {};
          const doc = files.document;
          if (!doc) { throw new Error('missing files.document'); }
          return {body: doc, vars: input.vars || {}};
      - id: for_info
        type: code
        timeout: 50ms
        script: |
          const files = (input.body && input.body.files) || {};
          const meta = {};
          for (const [name, file] of Object.entries(files)) {
            meta[name] = {
              filename: file.filename || '',
              content_type: file.content_type || '',
              size: file.size || 0
            };
          }
          return {
            body: {
              fields: (input.body && input.body.fields) || {},
              files: meta
            },
            vars: input.vars || {}
          };
      - {id: out_s3, type: publish, destination: archive, target: uploads, payload: object}
      - {id: out_info, type: publish, destination: events, target: uploads-meta}
    edges:
      - in>body
      - body>auth
      - auth>env
      - env>for_s3
      - env>for_info
      - for_s3>out_s3
      - for_info>out_info
`

func buildMultipartUploadWithType(t *testing.T, account string, filename string, contentType string, content []byte) (string, []byte) {
	t.Helper()
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	if err := w.WriteField("account", account); err != nil {
		t.Fatal(err)
	}
	h := make(map[string][]string)
	h["Content-Disposition"] = []string{`form-data; name="document"; filename="` + filename + `"`}
	h["Content-Type"] = []string{contentType}
	part, err := w.CreatePart(h)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := part.Write(content); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	return w.FormDataContentType(), buf.Bytes()
}

func TestMultipartSplitFansOutRawFileToS3AndInfoElsewhere(t *testing.T) {
	pub := &fakePublisher{}
	srv, err := NewFlowServer(loadFlowCfg(t, multipartSplitYAML), pub, slog.Default(), nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = srv.Close() })

	fileBytes := []byte("%PDF-1.4 fake-bytes")
	ct, body := buildMultipartUploadWithType(t, "acct_42", "report.pdf", "application/pdf", fileBytes)
	req := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/webhooks/upload-split", bytes.NewReader(body))
	req.Header.Set("Content-Type", ct)
	req.Header.Set("X-Hub-Signature-256", githubHMAC(body))
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d body = %s", rec.Code, rec.Body.String())
	}
	if len(pub.published) != 2 {
		t.Fatalf("published = %d, want 2 destinations", len(pub.published))
	}

	byDest := map[string]publishedMessage{}
	for _, p := range pub.published {
		byDest[p.destinationID] = p
	}

	s3 := byDest["archive"]
	if s3.msg.PayloadMode != "object" {
		t.Fatalf("archive PayloadMode = %q, want object", s3.msg.PayloadMode)
	}
	if !bytes.Equal(s3.msg.ObjectBytes, fileBytes) {
		t.Fatalf("archive ObjectBytes = %q, want raw file", s3.msg.ObjectBytes)
	}
	if s3.msg.ObjectContentType != "application/pdf" {
		t.Fatalf("ObjectContentType = %q", s3.msg.ObjectContentType)
	}
	if s3.msg.ObjectFilename != "report.pdf" {
		t.Fatalf("ObjectFilename = %q", s3.msg.ObjectFilename)
	}
	// Body remains the file object for observability; object mode does not
	// require consumers to decode base64 from the JSON envelope.
	doc, ok := s3.msg.Body.(map[string]any)
	if !ok {
		t.Fatalf("archive Body = %#v", s3.msg.Body)
	}
	encoded, _ := doc["content"].(string)
	decoded, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil || !bytes.Equal(decoded, fileBytes) {
		t.Fatalf("archive body.content = %q err=%v", encoded, err)
	}

	info := byDest["events"]
	if info.msg.PayloadMode != "envelope" && info.msg.PayloadMode != "" {
		t.Fatalf("events PayloadMode = %q, want envelope", info.msg.PayloadMode)
	}
	if len(info.msg.ObjectBytes) != 0 {
		t.Fatalf("events must not carry ObjectBytes: %q", info.msg.ObjectBytes)
	}
	infoBody, ok := info.msg.Body.(map[string]any)
	if !ok {
		t.Fatalf("events body = %#v", info.msg.Body)
	}
	fields, _ := infoBody["fields"].(map[string]any)
	if fields["account"] != "acct_42" {
		t.Fatalf("info fields = %#v", fields)
	}
	infoDoc, _ := infoBody["files"].(map[string]any)["document"].(map[string]any)
	if _, hasContent := infoDoc["content"]; hasContent {
		t.Fatalf("info branch must omit file bytes: %#v", infoDoc)
	}
	gotSize, ok := numericSize(infoDoc["size"])
	if !ok || gotSize != int64(len(fileBytes)) {
		t.Fatalf("info size = %#v", infoDoc["size"])
	}
}

func TestRawObjectPayloadKeepsContentTypeOutsideForwardedHeaderAllowlist(t *testing.T) {
	const src = `
server: {addr: ":8080", max_body_bytes: 1048576}
security:
  forwarded_header_allowlist: [X-Request-ID]
destinations:
  archive:
    type: s3
    s3: {region: us-east-1, bucket: test-bucket}
flows:
  upload:
    nodes:
      - {id: in, type: http_trigger, path: /upload, methods: [POST]}
      - {id: body, type: read_body, max_bytes: 1048576}
      - {id: auth, type: auth, scheme: hmac_sha256_hex, secret: testsecret, signature_header: X-Hub-Signature-256}
      - {id: env, type: envelope, body_format: raw}
      - {id: out, type: publish, destination: archive, target: uploads, payload: object}
    edges: [in>body, body>auth, auth>env, env>out]
`
	pub := &fakePublisher{}
	srv, err := NewFlowServer(loadFlowCfg(t, src), pub, slog.Default(), nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = srv.Close() })

	body := []byte("raw-pdf-bytes")
	req := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/upload", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/pdf")
	req.Header.Set("X-Hub-Signature-256", githubHMAC(body))
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d body = %s", rec.Code, rec.Body.String())
	}
	if len(pub.published) != 1 {
		t.Fatalf("published = %d, want 1", len(pub.published))
	}
	msg := pub.published[0].msg
	if msg.Headers.Get("Content-Type") != "" {
		t.Fatalf("Content-Type leaked through forwarded allowlist: %#v", msg.Headers)
	}
	if msg.ObjectContentType != "application/pdf" {
		t.Fatalf("ObjectContentType = %q, want original request type", msg.ObjectContentType)
	}
	if !bytes.Equal(msg.ObjectBytes, body) {
		t.Fatalf("ObjectBytes = %q, want %q", msg.ObjectBytes, body)
	}
}

func TestObjectPayloadRejectsInvalidFileContentTypeAsBadRequest(t *testing.T) {
	pub := &fakePublisher{}
	srv, err := NewFlowServer(loadFlowCfg(t, multipartSplitYAML), pub, slog.Default(), nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = srv.Close() })

	contentType, body := buildMultipartUploadWithType(t, "acct_42", "report.pdf", "not a media type", []byte("x"))
	req := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/webhooks/upload-split", bytes.NewReader(body))
	req.Header.Set("Content-Type", contentType)
	req.Header.Set("X-Hub-Signature-256", githubHMAC(body))
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d body = %s, want 400", rec.Code, rec.Body.String())
	}
	if len(pub.published) != 0 {
		// stop_on_failure follows edge order: the S3 branch fails before the
		// metadata branch, so no destination should be attempted.
		t.Fatalf("published = %d, want 0", len(pub.published))
	}
}

func TestObjectPayloadRawFallbackRequiresRawBodyFormat(t *testing.T) {
	rawBase64 := base64.StdEncoding.EncodeToString([]byte("null"))
	msg := queue.Message{RawBody: []byte("null"), RawBodyBase64: &rawBase64}
	env := graph.Envelope{BodyFormat: "json", PayloadMode: "object"}
	if _, err := attachObjectPayload(msg, env); err == nil {
		t.Fatal("JSON null was accepted as a raw object payload")
	}
}

func numericSize(v any) (int64, bool) {
	switch n := v.(type) {
	case int:
		return int64(n), true
	case int64:
		return n, true
	case float64:
		return int64(n), true
	default:
		return 0, false
	}
}
