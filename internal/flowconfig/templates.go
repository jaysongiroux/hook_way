package flowconfig

import (
	"errors"
	"fmt"
	"net/url"
	"strings"

	"github.com/jasongiroux/hookway/internal/vars"
)

func validateTemplate(field, value string) error {
	if _, err := vars.Parse(value); err != nil {
		return fmt.Errorf("%s: %w", field, err)
	}
	return nil
}

func validateHTTPURL(raw string) error {
	u, err := url.Parse(raw)
	if err != nil {
		return fmt.Errorf("invalid URL: %w", err)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return errors.New("URL scheme must be http or https")
	}
	if u.Host == "" {
		return errors.New("URL must include a host")
	}
	return nil
}

// isBodyPathKey reports the lookup-only {body.a.b} dotted-path spelling.
// vars.Parse rejects the dots, but lookupKey still resolves those keys from
// the envelope body.
func isBodyPathKey(key string) bool {
	if !strings.HasPrefix(key, "{body.") || !strings.HasSuffix(key, "}") {
		return false
	}
	path := strings.TrimSuffix(strings.TrimPrefix(key, "{body."), "}")
	if path == "" {
		return false
	}
	for _, part := range strings.Split(path, ".") {
		if !vars.ValidName(part) {
			return false
		}
	}
	return true
}
