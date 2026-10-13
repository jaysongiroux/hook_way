package flowconfig

import (
	"errors"
	"fmt"
	"net/url"
	"strings"

	"github.com/jaysongiroux/hook_way/internal/egress"
	"github.com/jaysongiroux/hook_way/internal/vars"
)

func validateTemplate(field, value string) error {
	if _, err := vars.Parse(value); err != nil {
		return fmt.Errorf("%s: %w", field, err)
	}
	return nil
}

func validateHTTPURL(raw string) error {
	_, err := parseHTTPURL(raw)
	return err
}

func parseHTTPURL(raw string) (*url.URL, error) {
	u, err := url.Parse(raw)
	if err != nil {
		return nil, fmt.Errorf("invalid URL: %w", err)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return nil, errors.New("URL scheme must be http or https")
	}
	if u.Host == "" {
		return nil, errors.New("URL must include a host")
	}
	return u, nil
}

// validateEgressURL is the runtime check on a rendered http_request target.
// It is validateHTTPURL plus, when the egress policy applies, a refusal to
// address anything on the gateway's own host or private network.
//
// The policy applies when the url template's authority contains a
// {placeholder}, because only then is the destination host chosen by
// request-derived data (env.Vars is seeded from the trigger's path variables
// and extended by lookup and transform nodes, and Template.Render substitutes
// raw text). A fully literal authority is the operator's own explicit
// decision -- proxying to an internal service is a legitimate configuration,
// and a literal "http://10.0.0.5:8080" in the config is not an SSRF.
func validateEgressURL(raw string, enforce bool) error {
	u, err := parseHTTPURL(raw)
	if err != nil {
		return err
	}
	if !enforce {
		return nil
	}
	if reason := internalHostReason(u.Hostname()); reason != "" {
		return fmt.Errorf("URL host %q is not an allowed egress target (%s); set allow_private_targets: true on this node if the upstream is deliberately internal", u.Hostname(), reason)
	}
	return nil
}

// validateURLHostTemplate requires a {variable} in the url authority to be
// followed by a literal dotted domain suffix, so a request can pick at most
// one label under a fixed domain and never the whole host. This reimplements
// for flow configs the contract v1 enforced for webhook destination URLs
// (internal/config's webhookURLTemplateIssues), including its wording.
//
// The gate is "the parsed authority has placeholders", not "the authority
// text contains a brace": vars.Parse treats "${VAR}" as literal text, so an
// unexpanded environment reference such as "${UPSTREAM_BASE}/{rest}" is not a
// templated host. It becomes a literal authority once the environment is
// expanded at load.
func validateURLHostTemplate(raw string) error {
	authTpl, err := vars.Parse(urlAuthority(raw))
	if err != nil {
		return fmt.Errorf("url host template is malformed: %w", err)
	}
	if !authTpl.HasPlaceholders() {
		return nil
	}
	if !hostTemplateHasLiteralSuffix(authTpl) {
		return errors.New(`a {variable} in the url host must be followed by a literal domain suffix: ` +
			`write the host as "{envName}-api.example.com" so requests cannot choose an arbitrary host`)
	}
	return nil
}

func hostTemplateHasLiteralSuffix(authTpl vars.Template) bool {
	host := authTpl.LiteralSuffix()
	if colon := strings.LastIndexByte(host, ':'); colon >= 0 {
		host = host[:colon]
	}
	return strings.Contains(host, ".")
}

// urlAuthorityIsTemplated reports whether the host of a url template is
// chosen, even partly, by a request-derived variable.
func urlAuthorityIsTemplated(raw string) bool {
	authTpl, err := vars.Parse(urlAuthority(raw))
	if err != nil {
		// A malformed template is rejected elsewhere; assume the unsafe case.
		return true
	}
	return authTpl.HasPlaceholders()
}

// validateAuthorityVariables confines request-derived authority substitutions
// to single DNS labels. In particular, URL delimiters must not turn the fixed
// domain suffix into a path, query, fragment, or userinfo component. This also
// applies when private targets are explicitly allowed.
func validateAuthorityVariables(tpl vars.Template, values map[string]string) error {
	return egress.ValidateAuthorityVariables(tpl, values)
}

// urlAuthority returns the authority (host[:port]) of a raw, possibly
// templated URL without parsing it, so placeholders survive intact.
func urlAuthority(rawURL string) string {
	return egress.Authority(rawURL)
}

// internalHostReason returns a short reason when host must not be addressed,
// or "" when it is an acceptable egress target.
func internalHostReason(host string) string {
	return egress.BlockedHostReason(host)
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
