package control

import (
	"bytes"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/jaysongiroux/hook_way/internal/replaywire"
)

func TestControlFailureDiagnosticSanitizesBackendWithoutChangingOutcome(t *testing.T) {
	var logs bytes.Buffer
	s := &Server{opts: Options{Logger: slog.New(slog.NewJSONHandler(&logs, nil))}}
	err := errors.New("postgres://user:URLSECRET@database.example.test/private?token=QUERYSECRET password=DSNSECRET") // #nosec G101 -- synthetic redaction markers, never real credentials.
	w := httptest.NewRecorder()
	if outcome := s.refuse(w, replaywire.DeliverRequest{RunID: "run-correlation"}, err); outcome != OutcomeError || w.Code != http.StatusInternalServerError {
		t.Fatal("diagnostic formatting changed unknown redrive outcome")
	}
	for _, marker := range []string{"URLSECRET", "QUERYSECRET", "DSNSECRET", "/private"} {
		if strings.Contains(logs.String()+w.Body.String(), marker) {
			t.Fatalf("control diagnostic leaked%s", marker)
		}
	}
	if !strings.Contains(logs.String(), "database.example.test") || !strings.Contains(logs.String(), "run-correlation") {
		t.Fatal("control diagnostic lost safe origin or correlation")
	}
}
