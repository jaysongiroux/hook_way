package queue

import (
	"encoding/base64"
	"net/http"
	"testing"
)

func TestObjectFromBodyFileMap(t *testing.T) {
	content := []byte("PDF-BYTES")
	body := map[string]any{
		"filename":     "report.pdf",
		"content_type": "application/pdf",
		"size":         int64(len(content)),
		"content":      base64.StdEncoding.EncodeToString(content),
	}
	data, ct, name, err := ObjectFromBody(body, nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "PDF-BYTES" || ct != "application/pdf" || name != "report.pdf" {
		t.Fatalf("got data=%q ct=%q name=%q", data, ct, name)
	}
}

func TestObjectFromBodyRawBytes(t *testing.T) {
	raw := []byte("<xml/>")
	headers := http.Header{"Content-Type": []string{"application/xml"}}
	data, ct, name, err := ObjectFromBody(nil, raw, nil, headers)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "<xml/>" || ct != "application/xml" || name != "" {
		t.Fatalf("got data=%q ct=%q name=%q", data, ct, name)
	}
}

func TestObjectFromBodyRejectsOmittedContent(t *testing.T) {
	_, _, _, err := ObjectFromBody(map[string]any{"content_omitted": true, "filename": "a.pdf"}, nil, nil, nil)
	if err == nil {
		t.Fatal("expected error for content_omitted")
	}
}

func TestSanitizeObjectFilename(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		{"report.pdf", "report.pdf"},
		{"../../etc/passwd", "passwd"},
		{"a b.pdf", "a_b.pdf"},
		{"", ""},
		{".", ""},
		{"..", ""},
	}
	for _, tc := range cases {
		if got := SanitizeObjectFilename(tc.in); got != tc.want {
			t.Fatalf("SanitizeObjectFilename(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestSupportsObjectPayload(t *testing.T) {
	if !SupportsObjectPayload("s3") || !SupportsObjectPayload("redis_kv") {
		t.Fatal("s3 and redis_kv must support object payload")
	}
	if SupportsObjectPayload("rabbitmq") || SupportsObjectPayload("webhook") {
		t.Fatal("queue/webhook destinations must not claim object payload support")
	}
}
