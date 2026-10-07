package logging

import (
	"errors"
	"fmt"
	"net/url"
	"strings"
	"testing"
)

func TestErrorTextRemovesURLAndDSNCredentials(t *testing.T) {
	for _, text := range []string{
		`Post "http://user:USERSECRET@127.0.0.1:1/private/PATHSECRET?api_key=QUERYSECRET#FRAGMENTSECRET": connection refused`,
		`Post "http://malformed@user:USERSECRET@127.0.0.1:1/private/PATHSECRET": connection refused`,
		`Post "http://user:p'USERSECRET@127.0.0.1:1/private/PATHSECRET": connection refused`,
		`connect host=127.0.0.1 password='DSNSECRET with spaces' api_key="KEYSECRET with spaces" token=TOKENSECRET: connection refused`,
		`connect "http://user:USERSECRET@%zz/private/PATHSECRET": connection refused`,
	} {
		got := ErrorText(errors.New(text))
		for _, marker := range []string{"USERSECRET", "PATHSECRET", "QUERYSECRET", "FRAGMENTSECRET", "DSNSECRET", "KEYSECRET", "TOKENSECRET"} {
			if strings.Contains(got, marker) {
				t.Fatalf("diagnostic retained %s", marker)
			}
		}
		if !strings.Contains(got, "connection refused") {
			t.Fatal("error class was lost")
		}
		if !strings.Contains(text, "%zz") && !strings.Contains(got, "127.0.0.1") {
			t.Fatal("diagnostic host was lost")
		}
	}
	if ErrorText(nil) != "" {
		t.Fatal("nil error acquired text")
	}
	if got := ErrorText(errors.New("invalid\xff\x00input")); strings.ContainsRune(got, '\x00') || !strings.Contains(got, "�") {
		t.Fatal("invalid diagnostic text was not normalized")
	}
}

func TestErrorTextHandlesQuotedTransportURLs(t *testing.T) {
	err := fmt.Errorf("capture failed: %w", &url.Error{Op: "Post", URL: `http://user:p"USERSECRET@127.0.0.1:1/PATHSECRET`, Err: errors.New("connection refused")})
	got := ErrorText(err)
	if strings.Contains(got, "USERSECRET") || strings.Contains(got, "PATHSECRET") || !strings.Contains(got, "connection refused") {
		t.Fatal("quoted transport URL did not receive safe diagnostic handling")
	}
}

func TestErrorTextHandlesEveryJoinedTransportURL(t *testing.T) {
	err := errors.Join(
		fmt.Errorf("first: %w", &url.Error{Op: "Post", URL: "https://first.example.test/FIRSTSECRET", Err: errors.New("connection refused")}),
		fmt.Errorf("second: %w", &url.Error{Op: "Post", URL: `https://user:USERSECRET@second.example.test/private?api_key=quoted"QUERYSECRET`, Err: errors.New("connection refused")}), // #nosec G101 -- synthetic redaction markers, not credentials.
	)
	got := ErrorText(err)
	for _, marker := range []string{"FIRSTSECRET", "USERSECRET", "QUERYSECRET"} {
		if strings.Contains(got, marker) {
			t.Fatalf("joined diagnostic retained %s", marker)
		}
	}
	if !strings.Contains(got, "first.example.test") || !strings.Contains(got, "second.example.test") || !strings.Contains(got, "connection refused") {
		t.Fatal("joined diagnostic lost destination hosts or error class")
	}
}

type cyclingDiagnosticError struct{}

func (e *cyclingDiagnosticError) Error() string { return "URLSECRET" }
func (e *cyclingDiagnosticError) Unwrap() error { return e }

func TestErrorTextBoundsWrappedErrorTraversal(t *testing.T) {
	if got := ErrorText(&cyclingDiagnosticError{}); got != "diagnostic error chain exceeds limit" {
		t.Fatal("cyclic diagnostic traversal did not fail closed")
	}
}
