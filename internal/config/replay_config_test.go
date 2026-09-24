package config

import (
	"strings"
	"testing"
)

const (
	testCaptureToken = "capture-token-0123456789abcdefghijkl"
	testControlToken = "control-token-0123456789abcdefghij"
)

func replayDoc(replay string) string {
	return `
server: {addr: ":8080", max_body_bytes: 1048576}
metrics: {enabled: true, path: /metrics, addr: ":9090"}
` + replay + `
flows: {}
`
}

const validReplay = `replay:
  url: "https://replay.internal"
  capture_token: "` + testCaptureToken + `"
  control:
    addr: ":9092"
    token: "` + testControlToken + `"
`

func decodeReplay(t *testing.T, doc string) (SharedConfig, []ValidationIssue) {
	t.Helper()
	shared, issues := DecodeSharedYAML([]byte(doc), "test", "flows")
	return shared, append(issues, shared.ValidationIssues()...)
}

func errorFields(issues []ValidationIssue) []string {
	var out []string
	for _, i := range issues {
		if i.EffectiveSeverity() == SeverityError {
			out = append(out, i.Field)
		}
	}
	return out
}

func TestNoReplayBlockMeansCaptureIsOff(t *testing.T) {
	shared, issues := decodeReplay(t, replayDoc(""))
	if len(errorFields(issues)) != 0 || shared.Replay.Configured {
		t.Fatalf("replay = %+v, issues = %+v", shared.Replay, issues)
	}
}

func TestReplayBlockDecodesWithDefaults(t *testing.T) {
	shared, issues := decodeReplay(t, replayDoc(validReplay))
	if fields := errorFields(issues); len(fields) != 0 {
		t.Fatalf("issues = %+v", issues)
	}
	want := ReplayConfig{
		Configured: true, URL: "https://replay.internal", CaptureToken: testCaptureToken,
		MaxRecordBytes: DefaultReplayMaxRecordBytes,
		Control:        ReplayControlConfig{Addr: ":9092", Token: testControlToken, MaxConcurrent: DefaultReplayControlMaxConcurrent},
	}
	if shared.Replay != want {
		t.Fatalf("replay = %+v\nwant     %+v", shared.Replay, want)
	}
}

func TestReplayBlockDecodesEveryField(t *testing.T) {
	doc := replayDoc(`replay:
  url: "http://replay:8080"
  capture_token: "` + testCaptureToken + `"
  max_record_bytes: 1024
  tls: {ca_cert: /ca.pem, client_cert: /c.pem, client_key: /k.pem, server_name: replay.internal, insecure_skip_verify: true}
  control: {addr: ":9092", token: "` + testControlToken + `", max_concurrent: 3}
`)
	shared, issues := decodeReplay(t, doc)
	if len(errorFields(issues)) != 0 {
		t.Fatalf("issues = %+v", issues)
	}
	r := shared.Replay
	if r.MaxRecordBytes != 1024 || r.Control.MaxConcurrent != 3 ||
		r.TLS != (ReplayTLSConfig{CACert: "/ca.pem", ClientCert: "/c.pem", ClientKey: "/k.pem", ServerName: "replay.internal", InsecureSkipVerify: true}) {
		t.Fatalf("replay = %+v", r)
	}
}

func TestReplayValidation(t *testing.T) {
	cases := map[string]struct{ old, new, field string }{
		"url required":        {`url: "https://replay.internal"`, `url: ""`, "replay.url"},
		"url not absolute":    {`url: "https://replay.internal"`, `url: "replay.internal"`, "replay.url"},
		"capture token short": {`capture_token: "` + testCaptureToken + `"`, `capture_token: "short"`, "replay.capture_token"},
		"control addr":        {`addr: ":9092"`, `addr: ""`, "replay.control.addr"},
		"control on server":   {`addr: ":9092"`, `addr: ":8080"`, "replay.control.addr"},
		"control on metrics":  {`addr: ":9092"`, `addr: ":9090"`, "replay.control.addr"},
		"control token short": {`token: "` + testControlToken + `"`, `token: "short"`, "replay.control.token"},
		"tokens shared":       {`token: "` + testControlToken + `"`, `token: "` + testCaptureToken + `"`, "replay.control.token"},
		"negative max bytes":  {`capture_token: "` + testCaptureToken + `"`, `capture_token: "` + testCaptureToken + `"` + "\n  max_record_bytes: -1", "replay.max_record_bytes"},
		"negative concurrent": {`token: "` + testControlToken + `"`, `token: "` + testControlToken + `"` + "\n    max_concurrent: -1", "replay.control.max_concurrent"},
		"half mutual tls":     {`capture_token: "` + testCaptureToken + `"`, `capture_token: "` + testCaptureToken + `"` + "\n  tls: {client_cert: /c.pem}", "replay.tls"},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			if !strings.Contains(validReplay, c.old) {
				t.Fatalf("fixture does not contain %q", c.old)
			}
			_, issues := decodeReplay(t, replayDoc(strings.Replace(validReplay, c.old, c.new, 1)))
			for _, issue := range issues {
				if issue.Field == c.field && issue.EffectiveSeverity() == SeverityError {
					if strings.Contains(issue.Message, testCaptureToken) || strings.Contains(issue.Message, testControlToken) {
						t.Fatalf("issue leaks a token: %q", issue.Message)
					}
					return
				}
			}
			t.Fatalf("no error on %s; issues = %+v", c.field, issues)
		})
	}
}

// validate --json and --no-env leave ${VAR} references unexpanded; their
// length says nothing about the secret they will resolve to.
func TestReplayTokensMayBeUnexpandedReferences(t *testing.T) {
	doc := strings.Replace(strings.Replace(validReplay, testCaptureToken, "${REPLAY_CAPTURE_TOKEN}", 1),
		testControlToken, "${REPLAY_CONTROL_TOKEN}", 1)
	_, issues := decodeReplay(t, replayDoc(doc))
	if fields := errorFields(issues); len(fields) != 0 {
		t.Fatalf("issues = %+v", issues)
	}
}

func TestReplayBlockRejectsUnknownFields(t *testing.T) {
	_, issues := decodeReplay(t, replayDoc(validReplay+"  bogus: 1\n"))
	for _, issue := range issues {
		if issue.Field == "replay.bogus" {
			return
		}
	}
	t.Fatalf("issues = %+v, want an unknown-field error for replay.bogus", issues)
}
