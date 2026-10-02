package capture

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net"
	"net/url"
	"reflect"
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/jaysongiroux/hook_way/internal/config"
	"github.com/jaysongiroux/hook_way/internal/graph"
	"github.com/jaysongiroux/hook_way/internal/queue"
	"github.com/jaysongiroux/hook_way/internal/replaywire"
)

// NodeConfigHash identifies a node's configuration: its type and options
// (its description is not part of it), with credentials left out the same way
// as DestinationHash — options are expanded, so a ${VAR} secret would
// otherwise be hashed. The control listener reports it for a redrive.
func NodeConfigHash(nc graph.NodeConfig) string {
	if nc.ID == "" {
		return ""
	}
	var opts any
	if err := nc.Options.Decode(&opts); err != nil {
		return ""
	}
	b, err := json.Marshal(redactValue(opts))
	if err != nil {
		return ""
	}
	sum := sha256.Sum256(append([]byte(nc.Type+"\n"), b...))
	return hex.EncodeToString(sum[:])
}

// secretField names configuration fields that hold credentials.
var secretField = regexp.MustCompile(`(?i)(password|passwd|secret|token|api_?key|access_?key|private_?key|credential|^value$)`)

// redactValue returns decoded node options with credentials removed: values
// under secret-named keys cleared, header maps dropped, and identityText
// applied to every string.
func redactValue(v any) any {
	switch t := v.(type) {
	case map[string]any:
		out := make(map[string]any, len(t))
		for k, val := range t {
			switch {
			case strings.EqualFold(k, "credential"):
				out[k] = redactValue(val) // This block contains profile references, never resolved values.
			case secretField.MatchString(k):
				out[k] = ""
			case strings.EqualFold(k, "headers"):
				out[k] = nil // header sets carry credentials
			default:
				out[k] = redactValue(val)
			}
		}
		return out
	case []any:
		out := make([]any, len(t))
		for i, val := range t {
			out[i] = redactValue(val)
		}
		return out
	case string:
		return identityText(t)
	}
	return v
}

// dsnSecret matches a key=value secret in a connection string
// ("host=db password=…").
var dsnSecret = regexp.MustCompile(`(?i)\b(password|passwd|pwd|secret|token)\s*=\s*('[^']*'|[^\s&;]+)`)

// identityText removes credentials from a configuration string: a URL's
// userinfo, query, and fragment, and key=value secrets.
func identityText(s string) string {
	if strings.Contains(s, "://") {
		s = stripURL(s)
	}
	return dsnSecret.ReplaceAllString(s, "$1=")
}

// DestinationHash identifies a destination's settings with credentials left
// out: fields named like secrets are cleared, URL userinfo is removed, and
// maps (header sets) are dropped. Rotating a secret does not change it.
func DestinationHash(d config.DestinationConfig) string {
	if d.Type == "" {
		return ""
	}
	v := reflect.New(reflect.TypeOf(d)).Elem()
	v.Set(reflect.ValueOf(d)) // a copy: only this value's fields change
	redact(v)
	b, err := json.Marshal(v.Interface())
	if err != nil {
		return ""
	}
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

func redact(v reflect.Value) {
	if v.Kind() != reflect.Struct {
		return
	}
	for i := range v.NumField() {
		f, fv := v.Type().Field(i), v.Field(i)
		if !f.IsExported() {
			continue
		}
		switch fv.Kind() {
		case reflect.String:
			if secretField.MatchString(f.Name) {
				fv.SetString("")
			} else {
				fv.SetString(identityText(fv.String()))
			}
		case reflect.Struct:
			redact(fv)
		case reflect.Map, reflect.Pointer, reflect.Interface:
			fv.Set(reflect.Zero(fv.Type())) // never follow shared references
		}
	}
}

// stripURL removes a URL's userinfo, query, and fragment — where credentials
// travel; an unparseable value is dropped rather than risk keeping a secret.
func stripURL(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return ""
	}
	u.User, u.RawQuery, u.ForceQuery, u.Fragment, u.RawFragment = nil, "", false, "", ""
	return u.String()
}

// urlInText matches a URL in free text up to whitespace or a quote, capturing
// its scheme and host.
var urlInText = regexp.MustCompile(`([a-zA-Z][a-zA-Z0-9+.-]*://)(?:[^/\s@"'?#]*@)?([^/\s"'?#]*)[^\s"']*`)

// ErrorMessage is err's text made storable: every URL cut to its scheme and
// host (paths and queries carry keys, userinfo carries passwords), invalid
// UTF-8 and NUL bytes replaced, and the length bounded.
func ErrorMessage(err error) string {
	if err == nil {
		return ""
	}
	msg := urlInText.ReplaceAllString(err.Error(), "$1$2")
	msg = strings.ReplaceAll(strings.ToValidUTF8(msg, "�"), "\x00", "")
	return truncate(msg, replaywire.MaxErrorMessageLen)
}

// Attempts is the attempt count a delivery reported, or 1.
func Attempts(err error) int { return attempts(err) }

// CleanText makes request- or config-derived text storable metadata: valid
// UTF-8, no control characters, at most 1024 bytes.
func CleanText(s string) string { return cleanText(s, maxTextLen) }

// ErrorClass classifies a failure for the record's error_class.
func ErrorClass(err error) string {
	var de *queue.DeliveryError
	var pe graph.PanicError
	var ne net.Error
	switch {
	case err == nil:
		return replaywire.ErrorClassOther
	case errors.As(err, &de):
		return classFromOutcome(de.Outcome)
	case errors.Is(err, queue.ErrWebhookQueueFull):
		return replaywire.ErrorClassQueueFull
	case errors.As(err, &pe):
		return replaywire.ErrorClassPanic
	case errors.Is(err, context.DeadlineExceeded):
		return replaywire.ErrorClassTimeout
	case errors.As(err, &ne) && ne.Timeout():
		return replaywire.ErrorClassTimeout
	case errors.As(err, &ne):
		return replaywire.ErrorClassConnection
	}
	return replaywire.ErrorClassOther
}

// classFromOutcome maps a webhook delivery outcome to an error class.
func classFromOutcome(outcome string) string {
	switch outcome {
	case replaywire.ErrorClass4xx, replaywire.ErrorClass5xx, replaywire.ErrorClass429, replaywire.ErrorClassTimeout:
		return outcome
	}
	return replaywire.ErrorClassConnection
}

// attempts reads the attempt count a synchronous webhook reported; any
// other failure made one attempt.
func attempts(err error) int {
	var de *queue.DeliveryError
	if errors.As(err, &de) && de.Attempts > 0 {
		return de.Attempts
	}
	return 1
}

// cleanText makes request- or config-derived text storable: valid UTF-8, no
// control characters, at most limit bytes.
func cleanText(s string, limit int) string {
	s = strings.ToValidUTF8(s, "�")
	s = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return -1
		}
		return r
	}, s)
	return truncate(s, limit)
}

func truncate(s string, limit int) string {
	if len(s) <= limit {
		return s
	}
	for limit > 0 && !utf8.RuneStart(s[limit]) {
		limit--
	}
	return s[:limit]
}
