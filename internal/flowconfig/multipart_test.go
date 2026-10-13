package flowconfig

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"mime/multipart"
	"net/http"
	"strings"
	"testing"
)

// buildMultipart writes a multipart/form-data body and returns it with the
// matching Content-Type, so tests exercise a real boundary rather than a
// hand-written string that could drift from what the stdlib writer produces.
func buildMultipart(t *testing.T, fields map[string]string, files []multipartFile) (string, []byte) {
	t.Helper()
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	for name, value := range fields {
		if err := w.WriteField(name, value); err != nil {
			t.Fatal(err)
		}
	}
	for _, f := range files {
		h := make(map[string][]string)
		h["Content-Disposition"] = []string{fmt.Sprintf(`form-data; name=%q; filename=%q`, f.field, f.filename)}
		if f.contentType != "" {
			h["Content-Type"] = []string{f.contentType}
		}
		part, err := w.CreatePart(h)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := part.Write(f.content); err != nil {
			t.Fatal(err)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	return w.FormDataContentType(), buf.Bytes()
}

type multipartFile struct {
	field, filename, contentType string
	content                      []byte
}

func defaultLimits() multipartLimits {
	return multipartLimits{MaxParts: 16, MaxPartBytes: 1 << 20, IncludeContent: true}
}

func TestParseMultipartExtractsFieldsAndFiles(t *testing.T) {
	ct, body := buildMultipart(t,
		map[string]string{"event": "upload", "account": "acct_1"},
		[]multipartFile{{field: "document", filename: "report.pdf", contentType: "application/pdf", content: []byte("PDF-BYTES")}},
	)
	out, status, err := parseMultipart(ct, body, defaultLimits())
	if err != nil {
		t.Fatalf("parseMultipart = %v (status %d)", err, status)
	}
	fields, ok := out["fields"].(map[string]any)
	if !ok {
		t.Fatalf("fields = %T, want map[string]any", out["fields"])
	}
	if fields["event"] != "upload" || fields["account"] != "acct_1" {
		t.Fatalf("fields = %#v, want event=upload account=acct_1", fields)
	}
	files, ok := out["files"].(map[string]any)
	if !ok {
		t.Fatalf("files = %T, want map[string]any", out["files"])
	}
	doc, ok := files["document"].(map[string]any)
	if !ok {
		t.Fatalf("files.document = %T, want map[string]any", files["document"])
	}
	if doc["filename"] != "report.pdf" {
		t.Fatalf("filename = %v, want report.pdf", doc["filename"])
	}
	if doc["content_type"] != "application/pdf" {
		t.Fatalf("content_type = %v, want application/pdf", doc["content_type"])
	}
	if doc["size"] != int64(len("PDF-BYTES")) {
		t.Fatalf("size = %v (%T), want %d", doc["size"], doc["size"], len("PDF-BYTES"))
	}
	decoded, decErr := base64.StdEncoding.DecodeString(doc["content"].(string))
	if decErr != nil {
		t.Fatalf("content is not base64: %v", decErr)
	}
	if string(decoded) != "PDF-BYTES" {
		t.Fatalf("decoded content = %q, want PDF-BYTES", decoded)
	}
}

// The shape must be addressable by the existing dotted-path filter code, which
// cannot index a list. This test is the contract with internal/filter.
func TestParseMultipartShapeIsDottedPathAddressable(t *testing.T) {
	ct, body := buildMultipart(t, nil,
		[]multipartFile{{field: "document", filename: "a.pdf", contentType: "application/pdf", content: []byte("x")}},
	)
	out, _, err := parseMultipart(ct, body, defaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	// Mirror what filter.walkBodyPath does: successive map[string]any lookups.
	var current any = out
	for _, segment := range []string{"files", "document", "content_type"} {
		m, ok := current.(map[string]any)
		if !ok {
			t.Fatalf("segment %q: value is %T, not a map — a filter could not walk this", segment, current)
		}
		current, ok = m[segment]
		if !ok {
			t.Fatalf("segment %q missing", segment)
		}
	}
	if current != "application/pdf" {
		t.Fatalf("walked to %v, want application/pdf", current)
	}
}

func TestParseMultipartRepeatedFieldNamesAreSuffixed(t *testing.T) {
	ct, body := buildMultipart(t, nil, []multipartFile{
		{field: "attachment", filename: "one.txt", contentType: "text/plain", content: []byte("1")},
		{field: "attachment", filename: "two.txt", contentType: "text/plain", content: []byte("2")},
		{field: "attachment", filename: "three.txt", contentType: "text/plain", content: []byte("3")},
	})
	out, _, err := parseMultipart(ct, body, defaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	files := out["files"].(map[string]any)
	for key, wantName := range map[string]string{
		"attachment":   "one.txt",
		"attachment#2": "two.txt",
		"attachment#3": "three.txt",
	} {
		f, ok := files[key].(map[string]any)
		if !ok {
			t.Fatalf("files[%q] missing; got keys %v", key, mapKeys(files))
		}
		if f["filename"] != wantName {
			t.Fatalf("files[%q].filename = %v, want %v", key, f["filename"], wantName)
		}
	}
}

func TestParseMultipartRejectsTooManyParts(t *testing.T) {
	var files []multipartFile
	for i := 0; i < 5; i++ {
		files = append(files, multipartFile{field: fmt.Sprintf("f%d", i), filename: "x.txt", content: []byte("x")})
	}
	ct, body := buildMultipart(t, nil, files)
	lim := defaultLimits()
	lim.MaxParts = 3
	_, status, err := parseMultipart(ct, body, lim)
	if err == nil {
		t.Fatal("parseMultipart = nil error, want a rejection for exceeding max_parts")
	}
	if status != http.StatusRequestEntityTooLarge {
		t.Fatalf("status = %d, want 413 so an operator can tell too-big from malformed", status)
	}
	if !strings.Contains(err.Error(), "max_parts") {
		t.Fatalf("error = %v, want it to name max_parts", err)
	}
}

func TestParseMultipartRejectsOversizedPart(t *testing.T) {
	ct, body := buildMultipart(t, nil,
		[]multipartFile{{field: "big", filename: "big.bin", content: bytes.Repeat([]byte("A"), 200)}},
	)
	lim := defaultLimits()
	lim.MaxPartBytes = 100
	_, status, err := parseMultipart(ct, body, lim)
	if err == nil {
		t.Fatal("parseMultipart = nil error, want a rejection for exceeding max_part_bytes")
	}
	if status != http.StatusRequestEntityTooLarge {
		t.Fatalf("status = %d, want 413", status)
	}
	if !strings.Contains(err.Error(), "max_part_bytes") {
		t.Fatalf("error = %v, want it to name max_part_bytes", err)
	}
}

// The per-part cap must stop the read rather than buffer the whole part and
// measure afterwards, or the limit does not limit anything.
func TestParseMultipartDoesNotBufferBeyondThePartCap(t *testing.T) {
	huge := bytes.Repeat([]byte("A"), 4<<20) // 4 MiB in one part
	ct, body := buildMultipart(t, nil, []multipartFile{{field: "big", filename: "big.bin", content: huge}})
	lim := defaultLimits()
	lim.MaxPartBytes = 1024
	out, status, err := parseMultipart(ct, body, lim)
	if err == nil {
		t.Fatalf("parseMultipart = %#v, want a 413", out)
	}
	if status != http.StatusRequestEntityTooLarge {
		t.Fatalf("status = %d, want 413", status)
	}
}

func TestParseMultipartMetadataOnlyMode(t *testing.T) {
	ct, body := buildMultipart(t, nil,
		[]multipartFile{{field: "document", filename: "a.pdf", contentType: "application/pdf", content: []byte("PDF-BYTES")}},
	)
	lim := defaultLimits()
	lim.IncludeContent = false
	out, _, err := parseMultipart(ct, body, lim)
	if err != nil {
		t.Fatal(err)
	}
	doc := out["files"].(map[string]any)["document"].(map[string]any)
	if _, exists := doc["content"]; exists {
		t.Fatal("content present with IncludeContent=false")
	}
	if doc["content_omitted"] != true {
		t.Fatal("want content_omitted: true so a script can tell an omitted file from an empty one")
	}
	// Size must still be accurate: it is the reason to use this mode.
	if doc["size"] != int64(len("PDF-BYTES")) {
		t.Fatalf("size = %v, want %d even with content omitted", doc["size"], len("PDF-BYTES"))
	}
}

func TestParseMultipartRejectsMalformedInput(t *testing.T) {
	for _, tc := range []struct {
		name        string
		contentType string
		body        string
	}{
		{"not multipart", "application/json", `{"a":1}`},
		{"no boundary", "multipart/form-data", "whatever"},
		{"unparseable media type", "multipart/form-data; boundary", "whatever"},
		{"truncated body", "multipart/form-data; boundary=zzz", "--zzz\r\nContent-Disposition: form-data; name=\"a\"\r\n"},
		{"multipart/mixed is out of scope", "multipart/mixed; boundary=zzz", "--zzz--\r\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, status, err := parseMultipart(tc.contentType, []byte(tc.body), defaultLimits())
			if err == nil {
				t.Fatal("parseMultipart = nil error, want a rejection")
			}
			if status != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400 for malformed input", status)
			}
		})
	}
}

func TestParseMultipartEmptyBodyYieldsEmptyMaps(t *testing.T) {
	ct, body := buildMultipart(t, nil, nil)
	out, _, err := parseMultipart(ct, body, defaultLimits())
	if err != nil {
		t.Fatalf("parseMultipart on a boundary-only body = %v", err)
	}
	// Empty maps, not nil: a filter path into files must resolve as
	// "not found" rather than crash, and a script must see {} not null.
	if len(out["fields"].(map[string]any)) != 0 {
		t.Fatalf("fields = %#v, want empty", out["fields"])
	}
	if len(out["files"].(map[string]any)) != 0 {
		t.Fatalf("files = %#v, want empty", out["files"])
	}
}

func mapKeys(m map[string]any) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
