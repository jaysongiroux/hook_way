package httpapi

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"log/slog"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"testing"
)

// multipartSplitYAML is the serving-test counterpart of
// examples/multipart-split.yaml: a multipart upload fans out so one publish
// carries only files (S3-shaped) and the other carries fields + file metadata
// without base64 content (info-shaped). Destinations are webhook stubs so the
// fake publisher can assert both bodies without AWS/RabbitMQ.
const multipartSplitYAML = `
server:
  addr: ":8080"
  max_body_bytes: 8388608
destinations:
  archive:
    type: webhook
    webhook:
      url: "https://example.com/s3-stub"
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
          return {
            body: {files: (input.body && input.body.files) || {}},
            vars: input.vars || {}
          };
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
      - {id: out_s3, type: publish, destination: archive, target: uploads}
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

func numericSize(v any) (int64, bool) {
	switch n := v.(type) {
	case int:
		return int64(n), true
	case int64:
		return n, true
	case float64:
		return int64(n), true
	case json.Number:
		i, err := n.Int64()
		return i, err == nil
	default:
		return 0, false
	}
}

func TestMultipartSplitFansOutFileToS3AndInfoElsewhere(t *testing.T) {
	pub := &fakePublisher{}
	srv, err := NewFlowServer(loadFlowCfg(t, multipartSplitYAML), pub, slog.Default(), nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = srv.Close() })

	fileBytes := []byte("%PDF-1.4 fake-bytes")
	ct, body := buildMultipartUploadWithType(t, "acct_42", "report.pdf", "application/pdf", fileBytes)
	req := httptest.NewRequest(http.MethodPost, "/webhooks/upload-split", bytes.NewReader(body))
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

	byDest := map[string]any{}
	for _, p := range pub.published {
		byDest[p.destinationID] = p.msg.Body
	}

	s3Body, ok := byDest["archive"].(map[string]any)
	if !ok {
		t.Fatalf("archive body = %#v, want map", byDest["archive"])
	}
	if _, hasFields := s3Body["fields"]; hasFields {
		t.Fatalf("S3 branch must not carry form fields: %#v", s3Body)
	}
	files, ok := s3Body["files"].(map[string]any)
	if !ok {
		t.Fatalf("S3 files = %#v", s3Body["files"])
	}
	doc, ok := files["document"].(map[string]any)
	if !ok {
		t.Fatalf("S3 files.document = %#v", files["document"])
	}
	if doc["filename"] != "report.pdf" || doc["content_type"] != "application/pdf" {
		t.Fatalf("S3 document metadata = %#v", doc)
	}
	encoded, _ := doc["content"].(string)
	decoded, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		t.Fatalf("S3 content is not base64: %v", err)
	}
	if !bytes.Equal(decoded, fileBytes) {
		t.Fatalf("S3 content = %q, want %q", decoded, fileBytes)
	}

	infoBody, ok := byDest["events"].(map[string]any)
	if !ok {
		t.Fatalf("events body = %#v, want map", byDest["events"])
	}
	fields, ok := infoBody["fields"].(map[string]any)
	if !ok || fields["account"] != "acct_42" {
		t.Fatalf("info fields = %#v, want account=acct_42", infoBody["fields"])
	}
	infoFiles, ok := infoBody["files"].(map[string]any)
	if !ok {
		t.Fatalf("info files = %#v", infoBody["files"])
	}
	infoDoc, ok := infoFiles["document"].(map[string]any)
	if !ok {
		t.Fatalf("info files.document = %#v", infoFiles["document"])
	}
	if _, hasContent := infoDoc["content"]; hasContent {
		t.Fatalf("info branch must omit file bytes: %#v", infoDoc)
	}
	gotSize, ok := numericSize(infoDoc["size"])
	if !ok || gotSize != int64(len(fileBytes)) {
		t.Fatalf("info size = %#v (%T), want %d", infoDoc["size"], infoDoc["size"], len(fileBytes))
	}
	if infoDoc["filename"] != "report.pdf" {
		t.Fatalf("info filename = %#v", infoDoc["filename"])
	}
}

func TestMultipartSplitExampleConfigValidates(t *testing.T) {
	// Keep the serving test YAML aligned with the operator example: both must
	// compile as a flow (code scripts, fan-out edges, multipart options).
	_ = loadFlowCfg(t, multipartSplitYAML)
}
