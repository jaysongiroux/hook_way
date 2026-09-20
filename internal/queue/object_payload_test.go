package queue

import (
	"encoding/base64"
	"strings"
	"testing"
	"unicode/utf8"
)

func TestObjectFromBodyFileMap(t *testing.T) {
	content := []byte("PDF-BYTES")
	body := map[string]any{
		"filename":     "report.pdf",
		"content_type": "application/pdf",
		"size":         int64(len(content)),
		"content":      base64.StdEncoding.EncodeToString(content),
	}
	data, ct, name, err := ObjectFromBody(body, nil, nil, "")
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "PDF-BYTES" || ct != "application/pdf" || name != "report.pdf" {
		t.Fatalf("got data=%q ct=%q name=%q", data, ct, name)
	}
}

func TestObjectFromBodyRawBytes(t *testing.T) {
	raw := []byte("<xml/>")
	data, ct, name, err := ObjectFromBody(nil, raw, nil, "application/xml")
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "<xml/>" || ct != "application/xml" || name != "" {
		t.Fatalf("got data=%q ct=%q name=%q", data, ct, name)
	}
}

func TestObjectFromBodyAllowsEmptyObjects(t *testing.T) {
	t.Run("file map", func(t *testing.T) {
		data, ct, _, err := ObjectFromBody(map[string]any{"content": ""}, nil, nil, "")
		if err != nil {
			t.Fatal(err)
		}
		if len(data) != 0 || ct != "application/octet-stream" {
			t.Fatalf("data=%q content-type=%q", data, ct)
		}
	})
	t.Run("raw body", func(t *testing.T) {
		empty := ""
		data, ct, _, err := ObjectFromBody(nil, nil, &empty, "application/octet-stream")
		if err != nil {
			t.Fatal(err)
		}
		if len(data) != 0 || ct != "application/octet-stream" {
			t.Fatalf("data=%q content-type=%q", data, ct)
		}
	})
}

func TestObjectFromBodyRejectsInvalidMetadata(t *testing.T) {
	for _, body := range []map[string]any{
		{"content": "YQ==", "filename": 42},
		{"content": "YQ==", "content_type": []string{"application/pdf"}},
		{"content": "YQ==", "content_type": "text/plain\r\nX-Evil: yes"},
		{"content": "YQ==", "content_type": "not a media type"},
	} {
		if _, _, _, err := ObjectFromBody(body, nil, nil, ""); err == nil {
			t.Fatalf("ObjectFromBody(%#v) succeeded, want metadata error", body)
		}
	}
	if _, _, _, err := ObjectFromBody(nil, []byte("x"), nil, "not a media type"); err == nil {
		t.Fatal("ObjectFromBody(raw) accepted an invalid Content-Type")
	}
}

func TestObjectFromBodyRejectsOmittedContent(t *testing.T) {
	_, _, _, err := ObjectFromBody(map[string]any{"content_omitted": true, "filename": "a.pdf"}, nil, nil, "")
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
		{"safe\u202egnp.exe", "safegnp.exe"},
		{"", ""},
		{".", ""},
		{"..", ""},
		{strings.Repeat("é", 101), strings.Repeat("é", 100)},
	}
	for _, tc := range cases {
		got := SanitizeObjectFilename(tc.in)
		if got != tc.want {
			t.Fatalf("SanitizeObjectFilename(%q) = %q, want %q", tc.in, got, tc.want)
		}
		if !utf8.ValidString(got) || len(got) > 200 {
			t.Fatalf("SanitizeObjectFilename(%q) returned invalid/oversize %q", tc.in, got)
		}
	}
}
