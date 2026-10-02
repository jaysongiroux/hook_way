package flowconfig

import (
	"bytes"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"strings"
)

// multipartLimits bounds a parse. A multipart body is an attacker-controlled
// part count and per-part size hiding behind one Content-Length, so both are
// enforced while streaming rather than measured after buffering.
type multipartLimits struct {
	MaxTotalBytes  int64
	MaxParts       int
	MaxPartBytes   int64
	IncludeContent bool
}

// parseMultipart decodes a multipart/form-data body into the shape the rest of
// the flow works with:
//
//	{
//	  "fields": {"event": "upload"},
//	  "files":  {"document": {filename, content_type, size, content}}
//	}
//
// Every value is a string, int64, bool, or map[string]any, which is what makes
// this shape free to integrate: graph's branch cloning already deep-copies
// nested maps, filter's walkBodyPath already walks them by dotted key, and the
// code node's cloneJSONValue already round-trips them through JSON.
//
// Files are keyed by form field name rather than collected into a list because
// walkBodyPath cannot index a list — a list would make the feature
// inspectable by scripts only. A repeated field name gets a "#2", "#3" suffix
// in document order; "#" is not the path separator, so files."document#2"
// stays addressable.
//
// It returns the parsed shape, the HTTP status to report when err is non-nil
// (400 for malformed input, 413 for a limit), and the error. On success the
// status is 0.
func parseMultipart(contentType string, body []byte, lim multipartLimits) (map[string]any, int, error) {
	mediaType, params, err := mime.ParseMediaType(contentType)
	if err != nil {
		return nil, http.StatusBadRequest, fmt.Errorf("content-type %q is not parseable: %w", contentType, err)
	}
	// Only form-data. multipart/mixed and multipart/related are not what
	// webhook vendors send and are deliberately out of scope.
	if !strings.EqualFold(mediaType, "multipart/form-data") {
		return nil, http.StatusBadRequest, fmt.Errorf("body_format: multipart needs content-type multipart/form-data, got %q", mediaType)
	}
	boundary := params["boundary"]
	if boundary == "" {
		return nil, http.StatusBadRequest, errors.New("multipart/form-data content-type has no boundary parameter")
	}

	// A truncated body is not an error to mime/multipart: NextPart reports a
	// cut-off part as io.EOF, indistinguishable from a clean end, so the parse
	// would silently yield no parts at all. RFC 2046 requires the close
	// delimiter, and every conforming writer emits it, so its absence is the
	// one cheap signal that the body was cut short. Used only to reject:
	// finding the sequence inside part content can only let a body through,
	// never wrongly fail one.
	if !bytes.Contains(body, []byte("--"+boundary+"--")) {
		return nil, http.StatusBadRequest, errors.New("multipart body is malformed: no closing boundary, the body is truncated")
	}

	fields := map[string]any{}
	files := map[string]any{}
	reader := multipart.NewReader(bytes.NewReader(body), boundary)
	seen := map[string]int{}
	count := 0
	var total int64
	for {
		part, err := reader.NextPart()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, http.StatusBadRequest, fmt.Errorf("multipart body is malformed: %w", err)
		}
		// Counted before the part is read, so a body with a thousand parts is
		// rejected after reading three rather than after reading all thousand.
		count++
		if count > lim.MaxParts {
			_ = part.Close()
			return nil, http.StatusRequestEntityTooLarge, fmt.Errorf("multipart body has more than max_parts (%d) parts", lim.MaxParts)
		}
		name := part.FormName()
		filename := part.FileName()

		// LimitReader with max+1 is how read_body and http_request both detect
		// an overrun: read one byte past the cap and compare, so the parser
		// never holds more than cap+1 bytes of a part.
		limited := io.LimitReader(part, lim.MaxPartBytes+1)
		content, readErr := io.ReadAll(limited)
		_ = part.Close()
		if readErr != nil {
			return nil, http.StatusBadRequest, fmt.Errorf("multipart part %q: %w", name, readErr)
		}
		if int64(len(content)) > lim.MaxPartBytes {
			return nil, http.StatusRequestEntityTooLarge, fmt.Errorf("multipart part %q exceeds max_part_bytes (%d)", name, lim.MaxPartBytes)
		}

		total += int64(len(content))
		if lim.MaxTotalBytes > 0 && total > lim.MaxTotalBytes {
			return nil, http.StatusRequestEntityTooLarge, fmt.Errorf("multipart body exceeds max_total_bytes (%d)", lim.MaxTotalBytes)
		}
		if filename == "" {
			// A part with no filename is a form field. A repeated field name
			// keeps the last value, matching net/http's own form semantics.
			fields[name] = string(content)
			continue
		}
		key := name
		if n := seen[name]; n > 0 {
			key = fmt.Sprintf("%s#%d", name, n+1)
		}
		seen[name]++

		file := map[string]any{
			// filename is the vendor's claim, stored as data. Nothing in
			// Hookway passes it to a filesystem call or a URL.
			"filename": filename,
			// content_type is likewise what the vendor claimed in the part
			// headers, verbatim: no magic-byte sniffing, because a sniffed
			// value could disagree with what a filter matched on.
			"content_type": part.Header.Get("Content-Type"),
			"size":         int64(len(content)),
		}
		if lim.IncludeContent {
			file["content"] = base64.StdEncoding.EncodeToString(content)
		} else {
			// A marker rather than a missing key, so a script can tell "no
			// content carried" from "the file was empty".
			file["content_omitted"] = true
		}
		files[key] = file
	}
	return map[string]any{"fields": fields, "files": files}, 0, nil
}
