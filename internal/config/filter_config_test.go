package config

import (
	"strings"
	"testing"
)

// withFilters injects an endpoint-level filters block (and optional extra YAML)
// into the paddle_billing endpoint of validYAML, right before its auth block.
func withFilters(extra string) string {
	return strings.Replace(validYAML(), "    auth:\n      type: \"hmac_sha256\"", extra+"    auth:\n      type: \"hmac_sha256\"", 1)
}

func TestLoadParsesEndpointAndRouteFilters(t *testing.T) {
	t.Setenv("RABBITMQ_URL", "amqp://guest:guest@localhost:5672/")
	t.Setenv("PADDLE_SECRET", "pdl_secret")

	body := withFilters(`    filters:
      - source: header
        path: X-GitHub-Event
        operator: eq
        value: "push"
      - source: body
        path: repository.private
        operator: eq
        value: false
      - source: body
        path: repository.size
        operator: gt
        value: 100
    filter_response:
      status: 422
`)
	// Add a route-level filter to the existing destination.
	body = strings.Replace(body, `        target: "billing-events"
        create: true`, `        target: "billing-events"
        create: true
        filters:
          - source: body
            path: ref
            operator: eq
            value: "refs/heads/main"`, 1)

	cfg, err := Load(writeConfig(t, body))
	if err != nil {
		t.Fatalf("Load returned error: %v", err)
	}
	ep := cfg.Endpoints["paddle_billing"]
	if len(ep.Filters) != 3 {
		t.Fatalf("endpoint filters = %#v", ep.Filters)
	}
	// YAML scalar types are preserved into Value.
	if ep.Filters[0].Value != "push" {
		t.Fatalf("filter[0].Value = %#v, want string push", ep.Filters[0].Value)
	}
	if ep.Filters[1].Value != false {
		t.Fatalf("filter[1].Value = %#v, want bool false", ep.Filters[1].Value)
	}
	if ep.Filters[2].Value != 100 {
		t.Fatalf("filter[2].Value = %#v, want int 100", ep.Filters[2].Value)
	}
	if ep.FilterResponse.Status != 422 || ep.FilterResponseStatus() != 422 {
		t.Fatalf("filter_response = %#v", ep.FilterResponse)
	}
	if len(ep.Destinations[0].Filters) != 1 || ep.Destinations[0].Filters[0].Path != "ref" {
		t.Fatalf("route filters = %#v", ep.Destinations[0].Filters)
	}
}

func TestFilterResponseDefaultsTo202(t *testing.T) {
	var ep EndpointConfig
	if got := ep.FilterResponseStatus(); got != 202 {
		t.Fatalf("default filter response status = %d, want 202", got)
	}
}

func TestLoadRejectsInvalidFilters(t *testing.T) {
	tests := map[string]struct {
		yaml      string
		wantField string // substring expected in the error's field path
	}{
		"unknown operator": {withFilters(`    filters:
      - source: body
        path: ref
        operator: matches
        value: "x"
`), "filters[0].operator"},
		"unknown source": {withFilters(`    filters:
      - source: query
        path: ref
        operator: eq
        value: "x"
`), "filters[0].source"},
		"missing value": {withFilters(`    filters:
      - source: body
        path: ref
        operator: eq
`), "filters[0].value"},
		"invalid regex": {withFilters(`    filters:
      - source: body
        path: ref
        operator: regex
        value: "["
`), "filters[0].value"},
		"gt non-numeric": {withFilters(`    filters:
      - source: body
        path: ref
        operator: gt
        value: "x"
`), "filters[0].value"},
		"exists with value": {withFilters(`    filters:
      - source: body
        path: ref
        operator: exists
        value: "x"
`), "filters[0].value"},
		"invalid header name": {withFilters(`    filters:
      - source: header
        path: "Bad Header"
        operator: eq
        value: "x"
`), "filters[0].path"},
		"filter_response bad status": {withFilters(`    filters:
      - source: body
        path: ref
        operator: eq
        value: "x"
    filter_response:
      status: 999
`), "filter_response.status"},
		"filter_response without filters": {withFilters(`    filter_response:
      status: 422
`), "filter_response"},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Setenv("RABBITMQ_URL", "amqp://guest:guest@localhost:5672/")
			t.Setenv("PADDLE_SECRET", "pdl_secret")
			_, err := Load(writeConfig(t, tc.yaml))
			if err == nil {
				t.Fatal("expected validation error")
			}
			if !strings.Contains(err.Error(), tc.wantField) {
				t.Fatalf("error %q does not mention field %q", err.Error(), tc.wantField)
			}
		})
	}
}

func TestLoadRejectsInvalidRouteFilter(t *testing.T) {
	t.Setenv("RABBITMQ_URL", "amqp://guest:guest@localhost:5672/")
	t.Setenv("PADDLE_SECRET", "pdl_secret")
	body := strings.Replace(validYAML(), `        target: "billing-events"
        create: true`, `        target: "billing-events"
        create: true
        filters:
          - source: body
            path: ""
            operator: eq
            value: "x"`, 1)
	_, err := Load(writeConfig(t, body))
	if err == nil {
		t.Fatal("expected validation error for empty route filter path")
	}
	if !strings.Contains(err.Error(), "destinations[0].filters[0].path") {
		t.Fatalf("unexpected error: %v", err)
	}
}
