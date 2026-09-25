package problem

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestWriteEmitsRFC9457ProblemDetails(t *testing.T) {
	rec := httptest.NewRecorder()
	Write(rec, http.StatusConflict, "run_active", "a run is already queued")
	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d", rec.Code)
	}
	for header, want := range map[string]string{
		"Content-Type": "application/problem+json", "Cache-Control": "no-store", "X-Content-Type-Options": "nosniff",
	} {
		if got := rec.Header().Get(header); got != want {
			t.Errorf("%s = %q, want %q", header, got, want)
		}
	}
	var d Details
	if err := json.Unmarshal(rec.Body.Bytes(), &d); err != nil {
		t.Fatal(err)
	}
	want := Details{Type: "about:blank", Title: "Conflict", Status: 409, Detail: "a run is already queued", Code: "run_active"}
	if d != want {
		t.Fatalf("body = %+v, want %+v", d, want)
	}
}
