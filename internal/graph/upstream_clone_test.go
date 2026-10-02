package graph

import "testing"

func TestSelectedUpstreamValuesHaveBranchOwnership(t *testing.T) {
	input := Envelope{Upstream: &UpstreamResponse{Values: map[string]string{"created_id": "sample"}, SelectionError: "missing_field"}}
	left, err := clonePayload(input)
	if err != nil {
		t.Fatal(err)
	}
	right, err := clonePayload(input)
	if err != nil {
		t.Fatal(err)
	}
	left.(Envelope).Upstream.Values["created_id"] = "left"
	if right.(Envelope).Upstream.Values["created_id"] != "sample" || input.Upstream.Values["created_id"] != "sample" || right.(Envelope).Upstream.SelectionError != "missing_field" {
		t.Fatal("selected response state crossed branches")
	}
}
