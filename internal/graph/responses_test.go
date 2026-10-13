package graph

import "testing"

func TestResponsePolicyRejectsUnsafeAndUnknownSettings(t *testing.T) {
	for _, p := range []map[string]ResponsePolicy{
		{"made_up": {Status: 200}}, {"published": {Status: 199}},
		{"published": {Status: 204, Body: "not legal"}},
		{"published": {Status: 200, Headers: map[string]string{"Content-Length": "200"}}},
		{"published": {Status: 200, Headers: map[string]string{"X-Value": "value\r\nInjected: header"}}},
		{"published": {Status: 200, Headers: map[string]string{"not a header": "value"}}},
	} {
		if problems := ValidateResponses(p); len(problems) == 0 {
			t.Errorf("accepted %+v", p)
		}
	}
	if problems := ValidateResponses(map[string]ResponsePolicy{"publish_error": {Status: 200, Body: `{"status":"acknowledged"}`, Headers: map[string]string{"Content-Type": "application/json"}}}); len(problems) > 0 {
		t.Fatal(problems)
	}
}
