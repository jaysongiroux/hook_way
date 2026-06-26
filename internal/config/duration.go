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
