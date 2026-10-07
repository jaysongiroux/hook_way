package flowconfig

import (
	"fmt"
	"net"

	"github.com/jaysongiroux/hook_way/internal/config"
)

// configurationPolicyIssues describes supported choices that need an operator
// decision. It never resolves secret references or changes runtime defaults.
func configurationPolicyIssues(cfg Config) []config.ValidationIssue {
	var issues []config.ValidationIssue
	if cfg.Server.MaxInflightRequests == -1 {
		issues = append(issues, cfgWarning("server.max_inflight_requests",
			"request admission is unlimited; concurrent requests can exhaust memory and connections under load",
			"set a finite max_inflight_requests sized for your workload, or retain -1 intentionally with external admission limits"))
	}
	if cfg.Security.TrustedProxyHeaders && len(cfg.Security.TrustedProxies) == 0 {
		issues = append(issues, cfgWarning("security.trusted_proxies",
			"forwarded client identity is enabled without trusted proxy ranges; every socket peer can supply its client identity",
			"set trusted_proxies to the actual proxy addresses or CIDRs, disable trusted_proxy_headers, or intentionally enforce proxy trust at a separate network boundary"))
	}
	for i, entry := range cfg.Security.TrustedProxies {
		if _, network, err := net.ParseCIDR(entry); err == nil {
			if ones, _ := network.Mask.Size(); ones == 0 {
				issues = append(issues, cfgWarning(fmt.Sprintf("security.trusted_proxies[%d]", i),
					"this proxy range trusts every address in its IP family; direct callers can influence forwarded client identity",
					"list the actual proxy addresses or CIDR ranges, or retain universal trust only behind an independently enforced proxy boundary"))
			}
		}
	}
	if cfg.Replay.Configured {
		if isSampleCredential(cfg.Replay.CaptureToken) {
			issues = append(issues, sampleCredentialWarning("replay.capture_token"))
		}
		if isSampleCredential(cfg.Replay.Control.Token) {
			issues = append(issues, sampleCredentialWarning("replay.control.token"))
		}
	}
	for _, name := range sortedKeys(cfg.Destinations) {
		dest := cfg.Destinations[name]
		if dest.Type != "webhook" {
			continue
		}
		if isSampleCredential(dest.Webhook.Auth.Secret) {
			issues = append(issues, sampleCredentialWarning("destinations."+name+".webhook.auth.secret"))
		}
		if isSampleCredential(dest.Webhook.Auth.Value) {
			issues = append(issues, sampleCredentialWarning("destinations."+name+".webhook.auth.value"))
		}
	}
	return issues
}

// Match only exact known placeholders, rather than imposing a length policy on
// provider-issued credentials or treating unresolved ${VAR} as a real secret.
func isSampleCredential(value string) bool {
	switch value {
	case "change-me", "changeme", "dev-capture-token-0123456789abcdef", "dev-control-token-0123456789abcdef":
		return true
	default:
		return false
	}
}

func sampleCredentialWarning(field string) config.ValidationIssue {
	return cfgWarning(field,
		"a known sample credential is configured; anyone with the example value can authenticate",
		"replace the example with a distinct generated credential or the provider-issued secret; disposable development examples remain supported")
}
