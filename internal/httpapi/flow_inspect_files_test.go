package httpapi

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/textproto"
	"strings"
	"testing"

	"github.com/jaysongiroux/hook_way/internal/replaywire"
)

func TestInspectMultipartFilesKeepBytesAndOmissionMetadata(t *testing.T) {
	for _, include := range []bool{true, false} {
		t.Run(fmt.Sprintf("include_content=%v", include), func(t *testing.T) {
			sink := &uploadSink{done: make(chan struct{}, 4)}
			replay := httptest.NewServer(sink)
			defer replay.Close()
			src := strings.Replace(inspectionBranchConfig(t, replay.URL), "body_format: json", fmt.Sprintf("body_format: multipart, multipart: {include_content: %v}", include), 1)
			pub := &fakePublisher{}
			srv := redriveServer(t, loadFlowCfg(t, src), pub)
			var body bytes.Buffer
			writer := multipart.NewWriter(&body)
			original := []byte{0, 1, 2, 255}
			for i, content := range [][]byte{original, {}} {
				h := textproto.MIMEHeader{}
				h.Set("Content-Disposition", fmt.Sprintf(`form-data; name="document"; filename="receipt%d.pdf"`, i))
				h.Set("Content-Type", "application/pdf")
				part, err := writer.CreatePart(h)
				if err != nil {
					t.Fatal(err)
				}
				if _, err := part.Write(content); err != nil {
					t.Fatal(err)
				}
			}
			if err := writer.Close(); err != nil {
				t.Fatal(err)
			}
			req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/webhooks/github", bytes.NewReader(body.Bytes()))
			req.Header.Set("Content-Type", writer.FormDataContentType())
			req.Header.Set("X-Hub-Signature-256", githubHMAC(body.Bytes()))
			response := httptest.NewRecorder()
			srv.ServeHTTP(response, req)
			_ = srv.Close()
			_ = srv.capture.Close()
			if response.Code != 202 || len(pub.published) != 1 {
				t.Fatalf("multipart response=%d deliveries=%d body=%s", response.Code, len(pub.published), response.Body.String())
			}
			sink.mu.Lock()
			defer sink.mu.Unlock()
			if len(sink.got) != 1 || sink.got[0].meta.Kind != replaywire.KindInspection {
				t.Fatalf("records = %+v", sink.got)
			}
			var inspection replaywire.InspectionPayload
			if err := json.Unmarshal(sink.got[0].payload, &inspection); err != nil {
				t.Fatal(err)
			}
			var parsed struct {
				Files map[string]struct {
					Filename       string `json:"filename"`
					ContentType    string `json:"content_type"`
					Size           int    `json:"size"`
					Content        string `json:"content"`
					ContentOmitted bool   `json:"content_omitted"`
				} `json:"files"`
			}
			if err := json.Unmarshal([]byte(inspection.Body.Content), &parsed); err != nil {
				t.Fatal(err)
			}
			if len(parsed.Files) != 2 || parsed.Files["document"].Filename != "receipt0.pdf" || parsed.Files["document"].ContentType != "application/pdf" || parsed.Files["document"].Size != len(original) || parsed.Files["document#2"].Size != 0 {
				t.Fatalf("captured file information = %+v", parsed.Files)
			}
			for _, file := range parsed.Files {
				if file.ContentOmitted != !include {
					t.Fatalf("omission policy = %+v", file)
				}
			}
			if include {
				decoded, err := base64.StdEncoding.DecodeString(parsed.Files["document"].Content)
				if err != nil || !bytes.Equal(decoded, original) {
					t.Fatalf("captured bytes = %v, err=%v", decoded, err)
				}
			}
		})
	}
}
