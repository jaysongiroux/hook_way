package config

import (
	"fmt"
	"time"
)

func parseDuration(field, value string, fallback time.Duration) (time.Duration, ValidationIssue) {
	if value == "" {
		return fallback, ValidationIssue{}
	}
	d, err := time.ParseDuration(value)
	if err != nil {
		return 0, validationError(field, fmt.Sprintf("%q is not a valid Go duration", value), "use values like \"5s\", \"10s\", or \"5m\"")
	}
	return d, ValidationIssue{}
}

// parsePositiveDefaultDuration parses a duration that falls back to a
// (positive) default when omitted, but must itself be strictly positive when
// explicitly configured. This differs from parsePositiveDuration, whose
// omitted value is 0 (meaning "disabled"/"no override"), and from
// parseDuration, which accepts any parseable value including non-positive
// ones. It exists because some fields such as server.read_timeout have a
// meaningful positive default, yet an explicit "0s" or negative value would
// silently mean "no timeout" to the consumer (e.g. Go's http.Server) rather
// than the default the field name implies.
func parsePositiveDefaultDuration(field, value string, fallback time.Duration) (time.Duration, ValidationIssue) {
	d, issue := parseDuration(field, value, fallback)
	if issue.Field != "" {
		return d, issue
	}
	if value != "" && d <= 0 {
		return 0, validationError(field, "must be a positive duration when set", "omit the field to use the default, or set a positive value such as \"5s\"")
	}
	return d, ValidationIssue{}
}

func parsePositiveDuration(field, value string) (time.Duration, ValidationIssue) {
	if value == "" {
		return 0, ValidationIssue{}
	}
	d, err := time.ParseDuration(value)
	if err != nil {
		return 0, validationError(field, fmt.Sprintf("%q is not a valid Go duration", value), "use values like \"1h\", \"24h\", or \"168h\"")
	}
	if d <= 0 {
		return 0, validationError(field, "must be a positive duration when set", "omit it or set a value such as \"24h\"")
	}
	return d, ValidationIssue{}
}
