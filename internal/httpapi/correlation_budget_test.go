package httpapi

import (
	"net/http/httptest"
	"strings"
	"testing"
)

func TestRequestIDBoundAndAmbiguity(t *testing.T) {
	s := &Server{}
	for _, values := range [][]string{
		{"request-123"}, {strings.Repeat("x", 128)}, {strings.Repeat("x", 129)},
		{"first", "second"}, {"control\x00character"}, {"line\nbreak"}, {"snowman☃"}, {""}, {"space in id"},
	} {
		r := httptest.NewRequestWithContext(t.Context(), "GET", "/", nil)
		r.Header["X-Request-Id"] = values
		id := s.requestID(r)
		accepted := len(values) == 1 && validRequestID(values[0])
		if !validRequestID(id) || (accepted && id != values[0]) || (!accepted && len(id) != 36) {
			t.Fatalf("values=%q accepted=%t resultinglength=%d", values, accepted, len(id))
		}
	}
}
