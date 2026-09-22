package config

import (
	"strings"
	"testing"
)

// filterConfig builds a Config with one enabled endpoint carrying the given
// endpoint-level filter chain, filter response, and route-level filters.
// Endpoint filter validation is reachable only through validationIssues.
func filterConfig(filters []FilterConditionConfig, status int, routeFilters []FilterConditionConfig) Config {
	return endpointConfigFor(
		map[string]DestinationConfig{"billing": {Type: "rabbitmq", RabbitMQ: RabbitMQConfig{URL: "amqp://x", Exchange: "e", ExchangeType: "direct"}}},
		EndpointConfig{
			Enabled: true, Path: "/webhooks/paddle/billing",
			Auth:           AuthConfig{Type: "no_auth", Scheme: "no_auth"},
			Filters:        filters,
			FilterResponse: FilterResponseConfig{Status: status},
			Destinations: []EndpointDestinationConfig{{
				Destination: "billing", Target: "billing-events", Filters: routeFilters,
			}},
		})
}

func TestEndpointAndRouteFiltersValidate(t *testing.T) {
	cfg := filterConfig(
		[]FilterConditionConfig{
			{Source: "header", Path: "X-GitHub-Event", Operator: "eq", Value: "push"},
			{Source: "body", Path: "repository.private", Operator: "eq", Value: false},
			{Source: "body", Path: "repository.size", Operator: "gt", Value: 100},
		},
		422,
		[]FilterConditionConfig{{Source: "body", Path: "ref", Operator: "eq", Value: "refs/heads/main"}},
	)
	if issues := validationIssues(cfg); len(issues) != 0 {
		t.Fatalf("valid filter chain rejected: %#v", issues)
	}
	ep := cfg.Endpoints["e"]
	if ep.FilterResponse.Status != 422 || ep.FilterResponseStatus() != 422 {
		t.Fatalf("filter_response = %#v", ep.FilterResponse)
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
		cfg       Config
		wantField string // substring expected in the error's field path
	}{
		"unknown operator": {
			filterConfig([]FilterConditionConfig{{Source: "body", Path: "ref", Operator: "matches", Value: "x"}}, 0, nil),
			"filters[0].operator",
		},
		"unknown source": {
			filterConfig([]FilterConditionConfig{{Source: "query", Path: "ref", Operator: "eq", Value: "x"}}, 0, nil),
			"filters[0].source",
		},
		"missing value": {
			filterConfig([]FilterConditionConfig{{Source: "body", Path: "ref", Operator: "eq"}}, 0, nil),
			"filters[0].value",
		},
		"invalid regex": {
			filterConfig([]FilterConditionConfig{{Source: "body", Path: "ref", Operator: "regex", Value: "["}}, 0, nil),
			"filters[0].value",
		},
		"gt non-numeric": {
			filterConfig([]FilterConditionConfig{{Source: "body", Path: "ref", Operator: "gt", Value: "x"}}, 0, nil),
			"filters[0].value",
		},
		"exists with value": {
			filterConfig([]FilterConditionConfig{{Source: "body", Path: "ref", Operator: "exists", Value: "x"}}, 0, nil),
			"filters[0].value",
		},
		"invalid header name": {
			filterConfig([]FilterConditionConfig{{Source: "header", Path: "Bad Header", Operator: "eq", Value: "x"}}, 0, nil),
			"filters[0].path",
		},
		"filter_response bad status": {
			filterConfig([]FilterConditionConfig{{Source: "body", Path: "ref", Operator: "eq", Value: "x"}}, 999, nil),
			"filter_response.status",
		},
		"filter_response without filters": {
			filterConfig(nil, 422, nil),
			"filter_response",
		},
		"invalid route filter path": {
			filterConfig(nil, 0, []FilterConditionConfig{{Source: "body", Path: "", Operator: "eq", Value: "x"}}),
			"destinations[0].filters[0].path",
		},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			err := ConfigValidationError{Issues: validationIssues(tc.cfg)}
			if !strings.Contains(err.Error(), tc.wantField) {
				t.Fatalf("error %q does not mention field %q", err.Error(), tc.wantField)
			}
		})
	}
}
