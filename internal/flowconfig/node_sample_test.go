package flowconfig

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"net/http"
	"os"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/jaysongiroux/hook_way/internal/config"
	"github.com/jaysongiroux/hook_way/internal/graph"
)

func TestSampleOptions(t *testing.T) {
	reg, err := NewRegistryWithOptions(RegistryOptions{})
	if err != nil {
		t.Fatal(err)
	}
	spec, _ := reg.Lookup("sample")
	for _, tc := range []struct {
		yaml  string
		valid bool
	}{
		{"rate: 0", true}, {"rate: 1", true}, {"rate: 0.2", true},
		{"rate: 0.001", true}, {"rate: 0.00001", true},
		{"", false}, {"rate: null", false}, {"rate: -0.1", false},
		{"rate: 1.01", false}, {"rate: .nan", false}, {"rate: .inf", false},
		{"rate: -.inf", false}, {"rate: '0.2'", false}, {"rate: nope", false},
		{"rate: true", false}, {"rate: []", false}, {"rate: {}", false},
		{"rate: 0.2\nunknown: true", false},
	} {
		t.Run(tc.yaml, func(t *testing.T) {
			_, err := spec.New(nodeConfigFromYAML(t, "sample", tc.yaml+"\n"))
			if (err == nil) != tc.valid {
				t.Fatalf("valid=%v, error=%v", tc.valid, err)
			}
		})
	}
}

func TestSampleThresholdAndUnchangedEnvelope(t *testing.T) {
	in := graph.Envelope{Body: map[string]any{"event": "created"}, Vars: map[string]string{"route": "preview"}, Branch: "matched"}
	for _, tc := range []struct {
		rate, draw  float64
		keep, draws bool
	}{
		{0, 0, false, false}, {1, 0.999, true, false},
		{0.2, 0, true, true}, {0.2, math.Nextafter(0.2, 0), true, true},
		{0.2, 0.2, false, true}, {0.2, 0.9, false, true},
	} {
		calls := 0
		n := newSampleNode("sample", SampleOptions{Rate: &tc.rate}).(sampleNode)
		n.draw = func() float64 { calls++; return tc.draw }
		out, err := n.Run(t.Context(), in)
		if err != nil {
			t.Fatal(err)
		}
		if (calls == 1) != tc.draws {
			t.Fatalf("rate=%v: draws=%d", tc.rate, calls)
		}
		if tc.keep {
			if !reflect.DeepEqual(out, in) {
				t.Fatalf("kept envelope changed: %#v", out)
			}
			// The node itself must pass through the original maps, not rebuild them.
			out.(graph.Envelope).Vars["proof"] = "same map"
			if in.Vars["proof"] != "same map" {
				t.Fatal("sample copied the envelope")
			}
		} else if term, ok := out.(graph.Terminal); !ok || term.DropReason != "sampled_out" || term.Response != nil || len(term.Deliveries) != 0 {
			t.Fatalf("drop=%#v", out)
		}
	}
}

func TestSampleDecisionsAreIndependentAndConcurrent(t *testing.T) {
	rate := 0.5
	n := newSampleNode("sample", SampleOptions{Rate: &rate}).(sampleNode)
	draws := []float64{0.1, 0.9, 0.1}
	n.draw = func() float64 { v := draws[0]; draws = draws[1:]; return v }
	for _, keep := range []bool{true, false, true} {
		out, err := n.Run(t.Context(), graph.Envelope{})
		_, kept := out.(graph.Envelope)
		if err != nil || kept != keep {
			t.Fatalf("kept=%v, error=%v", kept, err)
		}
	}
	n = newSampleNode("sample", SampleOptions{Rate: &rate}).(sampleNode)
	var wg sync.WaitGroup
	for range 32 {
		wg.Go(func() {
			for range 100 {
				out, err := n.Run(t.Context(), graph.Envelope{})
				if err != nil || out == nil {
					t.Errorf("concurrent sample: %v %v", out, err)
				}
			}
		})
	}
	wg.Wait()
}

func TestSampleRejectsNonEnvelope(t *testing.T) {
	rate := 1.0
	n := newSampleNode("sample", SampleOptions{Rate: &rate})
	_, err := n.Run(t.Context(), graph.RequestHead{})
	var ne graph.NodeError
	if !errors.As(err, &ne) || ne.Status != http.StatusInternalServerError || ne.NodeID != "sample" {
		t.Fatalf("non-envelope error=%v", err)
	}
}

func TestSampleSchema(t *testing.T) {
	meta := NodeSchemas()["sample"]
	assertRequired(t, meta.Schema, "rate")
	prop, _ := meta.Schema.Properties.Get("rate")
	if prop.Type != "number" || prop.Minimum != json.Number("0") || prop.Maximum != json.Number("1") || prop.Default != nil {
		t.Fatalf("rate schema=%+v", prop)
	}
	if meta.Input != "Envelope" || meta.Output != "Envelope" || meta.OnError || meta.Detached || meta.BatchChildOrder <= 0 {
		t.Fatalf("metadata=%+v", meta)
	}
}

func TestSamplePlacementAndBodyFormats(t *testing.T) {
	fixture, err := os.ReadFile("../../examples/sample.yaml")
	if err != nil {
		t.Fatal(err)
	}
	for _, format := range []string{"json", "xml", "raw", "multipart"} {
		src := strings.Replace(string(fixture), "body_format: json", "body_format: "+format, 1)
		for _, issue := range ValidateBytes([]byte(src)) {
			if issue.EffectiveSeverity() == config.SeverityError {
				t.Fatalf("%s: %+v", format, issue)
			}
		}
	}
	bad := strings.Replace(string(fixture),
		"in>body, body>auth, auth>env, env>sample_events, sample_events>send",
		"in>sample_events, sample_events>body, body>auth, auth>env, env>send", 1)
	for _, issue := range ValidateBytes([]byte(bad)) {
		if issue.EffectiveSeverity() == config.SeverityError && strings.Contains(issue.Message, "type mismatch") {
			return
		}
	}
	t.Fatal("sample before read_body accepted")
}

func TestSampleStopsOnlyItsBranchWithoutFailureCapture(t *testing.T) {
	for _, sibling := range []bool{false, true} {
		pub := batchPublisherFunc(func(_ context.Context, _, target string, _ graph.Envelope) error {
			if target != "sibling" {
				t.Errorf("sampled-out branch reached %q", target)
			}
			return nil
		})
		reg, err := NewRegistryWithOptions(RegistryOptions{Publishers: staticResolver{pub: pub}})
		if err != nil {
			t.Fatal(err)
		}
		var fc graph.FlowConfig
		src := `nodes:
  - {id: in, type: http_trigger, path: /sample}
  - {id: body, type: read_body, max_bytes: 1024}
  - {id: auth, type: no_auth}
  - {id: env, type: envelope}
  - {id: sample, type: sample, rate: 0}
  - {id: send, type: publish, destination: primary, target: sampled}
edges: [in>body, body>auth, auth>env, env>sample, sample>send]
`
		if err := yaml.Unmarshal([]byte(src), &fc); err != nil {
			t.Fatal(err)
		}
		if sibling {
			fc.Nodes = append(fc.Nodes, nodeConfigFromYAML(t, "publish", "destination: primary\ntarget: sibling\n"))
			fc.Nodes[len(fc.Nodes)-1].ID = "sibling"
			fc.Edges = append(fc.Edges, "env>sibling")
		}
		flow, err := graph.Compile("sample", fc, reg)
		if err != nil {
			t.Fatal(err)
		}
		dropped, failed := false, false
		term, err := flow.Run(WithBodyReader(t.Context(), strings.NewReader(`{"event":"created"}`)), graph.RequestHead{}, graph.RunOptions{
			PublishTimeout: time.Second,
			OnNode: func(ev graph.NodeEvent) {
				if ev.NodeType == "sample" {
					dropped = ev.Dropped && ev.DropReason == "sampled_out" && ev.Err == nil
				}
			},
			OnFailure: func(graph.FailureEvent) { failed = true },
		})
		if err != nil {
			t.Fatal(err)
		}
		want := 0
		if sibling {
			want = 1
		}
		if !dropped || failed || len(term.Deliveries) != want || graph.NeedsCompensation(term, err) {
			t.Fatalf("drop=%v capture=%v terminal=%+v", dropped, failed, term)
		}
	}
}
