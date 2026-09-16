package graph

import (
	"bytes"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestParseEdge(t *testing.T) {
	cases := []struct {
		in      string
		want    Edge
		wantErr bool
	}{
		{in: "in>body", want: Edge{From: "in", To: "body"}},
		{in: " in > body ", want: Edge{From: "in", To: "body"}},
		{in: "route:matched>sns", want: Edge{From: "route", FromPort: "matched", To: "sns"}},
		{in: "route:default>fallback", want: Edge{From: "route", FromPort: "default", To: "fallback"}},
		{in: "", wantErr: true},
		{in: "in", wantErr: true},
		{in: "in>", wantErr: true},
		{in: ">body", wantErr: true},
		{in: "a>b>c", wantErr: true},
		{in: "a:>b", wantErr: true},
		{in: "a:x:y>b", wantErr: true},
	}
	for _, tc := range cases {
		t.Run(tc.in, func(t *testing.T) {
			got, err := ParseEdge(tc.in)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("ParseEdge(%q) = %+v, want error", tc.in, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("ParseEdge(%q) = %v, want nil", tc.in, err)
			}
			if got != tc.want {
				t.Errorf("ParseEdge(%q) = %+v, want %+v", tc.in, got, tc.want)
			}
		})
	}
}

func TestParseEdgesReportsIndexOnFailure(t *testing.T) {
	_, err := ParseEdges([]string{"a>b", "broken"})
	if err == nil {
		t.Fatal("ParseEdges() = nil, want error")
	}
	// The index must appear so an operator can find the offending entry in a
	// long edges list.
	if !contains(err.Error(), "edges[1]") {
		t.Errorf("error %q does not mention edges[1]", err.Error())
	}
}

func contains(s, sub string) bool {
	return len(s) >= len(sub) && (s == sub || len(sub) == 0 ||
		func() bool {
			for i := 0; i+len(sub) <= len(s); i++ {
				if s[i:i+len(sub)] == sub {
					return true
				}
			}
			return false
		}())
}

func TestNodeConfigUnmarshalKeepsOptions(t *testing.T) {
	const src = `
id: rl
type: rate_limit
rpm: 600
burst: 60
`
	var nc NodeConfig
	if err := yaml.Unmarshal([]byte(src), &nc); err != nil {
		t.Fatalf("Unmarshal() = %v", err)
	}
	if nc.ID != "rl" || nc.Type != "rate_limit" {
		t.Fatalf("got id=%q type=%q, want rl/rate_limit", nc.ID, nc.Type)
	}
	var opts struct {
		RPM   int `yaml:"rpm"`
		Burst int `yaml:"burst"`
	}
	if err := nc.Options.Decode(&opts); err != nil {
		t.Fatalf("Options.Decode() = %v", err)
	}
	if opts.RPM != 600 || opts.Burst != 60 {
		t.Errorf("opts = %+v, want rpm=600 burst=60", opts)
	}
}

func TestFlowConfigUnmarshal(t *testing.T) {
	const src = `
nodes:
  - {id: in, type: http_trigger, path: /webhooks/github}
  - {id: body, type: read_body, max_bytes: 1048576}
edges: [in>body]
`
	var fc FlowConfig
	if err := yaml.Unmarshal([]byte(src), &fc); err != nil {
		t.Fatalf("Unmarshal() = %v", err)
	}
	if len(fc.Nodes) != 2 || len(fc.Edges) != 1 {
		t.Fatalf("got %d nodes / %d edges, want 2/1", len(fc.Nodes), len(fc.Edges))
	}
	if fc.Nodes[1].ID != "body" {
		t.Errorf("Nodes[1].ID = %q, want body", fc.Nodes[1].ID)
	}
}

// TestNodeConfigUnmarshalSeparatesDescription pins description as node
// identity rather than a type-specific option: it must land on the struct
// field and be absent from Options, because every node type decodes its
// options with KnownFields(true) and would reject an unexpected key.
func TestNodeConfigUnmarshalSeparatesDescription(t *testing.T) {
	const src = `
id: rl
type: rate_limit
description: Shed bursts before the body is read.
rpm: 600
`
	var nc NodeConfig
	if err := yaml.Unmarshal([]byte(src), &nc); err != nil {
		t.Fatalf("Unmarshal() = %v", err)
	}
	if nc.Description != "Shed bursts before the body is read." {
		t.Errorf("Description = %q, want the description text", nc.Description)
	}
	var opts struct {
		RPM int `yaml:"rpm"`
	}
	dec := yaml.NewDecoder(bytes.NewReader(mustMarshal(t, &nc.Options)))
	dec.KnownFields(true)
	if err := dec.Decode(&opts); err != nil {
		t.Fatalf("strict Options decode = %v; description must not reach option structs", err)
	}
	if opts.RPM != 600 {
		t.Errorf("rpm = %d, want 600", opts.RPM)
	}
}

func mustMarshal(t *testing.T, node *yaml.Node) []byte {
	t.Helper()
	raw, err := yaml.Marshal(node)
	if err != nil {
		t.Fatalf("Marshal() = %v", err)
	}
	return raw
}
