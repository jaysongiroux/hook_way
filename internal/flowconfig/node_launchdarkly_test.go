package flowconfig

import (
	"context"
	"fmt"
	"math"
	"net/http"
	"reflect"
	"strings"
	"testing"

	"github.com/jaysongiroux/hook_way/internal/featureflags"
	"github.com/jaysongiroux/hook_way/internal/graph"
	"gopkg.in/yaml.v3"
)

const flagTestOptions = `connection: production
name: rollout
context:
  kind: organization
  key: {source: body, path: tenant.id}
  attributes:
    plan: {source: header, path: X-Plan}
    region: {source: vars, path: region}
    details: {value: {seats: 3, beta: true}}
flags:
  gate: {key: new.processor, type: boolean, fallback: false, variable: gate}
  route: {key: processor-name, type: string, fallback: legacy, variable: route}
  limit: {key: rate-limit, type: number, fallback: 0, variable: limit}
  settings: {key: settings, type: json, fallback: null, variable: settings}
`

func flagOptions(t *testing.T, raw string) LaunchDarklyOptions {
	t.Helper()
	var o LaunchDarklyOptions
	if err := yaml.Unmarshal([]byte(raw), &o); err != nil {
		t.Fatal(err)
	}
	if err := validateLaunchDarkly(o); err != nil {
		t.Fatal(err)
	}
	return o
}
func flagInput() graph.Envelope {
	return graph.Envelope{V: graph.Verified{Raw: graph.RawRequest{Head: graph.RequestHead{Header: http.Header{"X-Plan": []string{"pro"}}}}}, Body: map[string]any{"tenant": map[string]any{"id": "acme"}}, Vars: map[string]string{"region": "east", "untouched": "yes"}, Flags: map[string]any{"earlier": map[string]any{"value": true}}}
}
func TestLaunchDarklyTypesSelectorsBindingsAndImmutability(t *testing.T) {
	in := flagInput()
	results := map[string]any{"new.processor": true, "processor-name": "next", "rate-limit": 2.5, "settings": map[string]any{"limit": 4, "nested": []any{true, nil}}}
	calls := 0
	n := launchDarklyNode{id: "ld", opts: flagOptions(t, flagTestOptions), evaluate: func(ctx featureflags.Context, key, kind string, fallback any) featureflags.Result {
		calls++
		if ctx.Key != "acme" || ctx.Kind != "organization" || ctx.Attributes["plan"] != "pro" || ctx.Attributes["region"] != "east" || !reflect.DeepEqual(ctx.Attributes["details"], map[string]any{"seats": 3.0, "beta": true}) {
			t.Errorf("context=%+v", ctx)
		}
		return featureflags.Result{Value: results[key], Reason: "FALLTHROUGH"}
	}}
	out, err := n.Run(context.Background(), in)
	if err != nil {
		t.Fatal(err)
	}
	env := out.(graph.Envelope)
	if calls != 4 || env.Vars["gate"] != "true" || env.Vars["route"] != "next" || env.Vars["limit"] != "2.5" || env.Vars["settings"] != `{"limit":4,"nested":[true,null]}` {
		t.Fatalf("bindings=%v calls=%d", env.Vars, calls)
	}
	if !reflect.DeepEqual(in, flagInput()) {
		t.Fatal("input changed")
	}
	results["settings"].(map[string]any)["limit"] = 99
	if env.Flags["rollout"].(map[string]any)["settings"].(map[string]any)["value"].(map[string]any)["limit"] != 4.0 {
		t.Fatal("SDK result was aliased")
	}
	if !reflect.DeepEqual(env.Body, in.Body) {
		t.Fatal("body changed")
	}
}
func TestLaunchDarklyFallbacksAndInvalidContexts(t *testing.T) {
	for _, tc := range []struct {
		name      string
		invalid   bool
		evaluate  featureflags.Evaluator
		errorKind string
	}{
		{name: "not ready", errorKind: "CLIENT_NOT_READY"},
		{name: "invalid context", invalid: true, errorKind: "CONTEXT_INVALID", evaluate: func(featureflags.Context, string, string, any) featureflags.Result {
			t.Fatal("invalid context reached SDK")
			return featureflags.Result{}
		}},
		{name: "missing flag", errorKind: "FLAG_NOT_FOUND", evaluate: func(_ featureflags.Context, _, _ string, v any) featureflags.Result {
			return featureflags.Result{Value: v, UsedFallback: true, Reason: "ERROR", ErrorKind: "FLAG_NOT_FOUND"}
		}},
		{name: "wrong type", errorKind: "WRONG_TYPE", evaluate: func(featureflags.Context, string, string, any) featureflags.Result {
			return featureflags.Result{Value: math.Inf(1), Reason: "FALLTHROUGH"}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			in := flagInput()
			if tc.invalid {
				in.Body = nil
			}
			n := launchDarklyNode{id: "ld", opts: flagOptions(t, flagTestOptions), evaluate: tc.evaluate}
			out, err := n.Run(context.Background(), in)
			if err != nil {
				t.Fatal(err)
			}
			env := out.(graph.Envelope)
			for _, v := range env.Flags["rollout"].(map[string]any) {
				r := v.(map[string]any)
				if r["used_fallback"] != true || r["error_kind"] != tc.errorKind {
					t.Fatalf("result=%v", r)
				}
			}
			if env.Vars["gate"] != "false" || env.Vars["limit"] != "0" || env.Vars["settings"] != "null" {
				t.Fatalf("fallbacks=%v", env.Vars)
			}
		})
	}
}
func TestLaunchDarklyFalseOffIsNotFallback(t *testing.T) {
	o := flagOptions(t, "connection: production\nname: rollout\ncontext: {key: {value: tenant}}\nflags: {gate: {key: gate, type: boolean, fallback: true}}")
	n := launchDarklyNode{opts: o, evaluate: func(featureflags.Context, string, string, any) featureflags.Result {
		return featureflags.Result{Value: false, Reason: "OFF"}
	}}
	out, err := n.Run(context.Background(), graph.Envelope{})
	if err != nil {
		t.Fatal(err)
	}
	r := out.(graph.Envelope).Flags["rollout"].(map[string]any)["gate"].(map[string]any)
	if r["value"] != false || r["used_fallback"] != false || r["reason"] != "OFF" {
		t.Fatal(r)
	}
}
func TestLaunchDarklyStrictValidation(t *testing.T) {
	for _, raw := range []string{
		strings.Replace(flagTestOptions, "fallback: false", "variable: x", 1),
		strings.Replace(flagTestOptions, "fallback: false", "fallback: null", 1),
		strings.Replace(flagTestOptions, "fallback: false", "fallback: 'false'", 1),
		strings.Replace(flagTestOptions, "type: number", "type: integer", 1),
		strings.Replace(flagTestOptions, "name: rollout", "name: has.dot", 1),
		strings.Replace(flagTestOptions, "source: body, path: tenant.id", "source: query, path: tenant.id", 1),
		strings.Replace(flagTestOptions, "source: body, path: tenant.id", "source: body, path: tenant.id, value: acme", 1),
		strings.Replace(flagTestOptions, "fallback: false", "fallback: false, future: true", 1),
		strings.Replace(flagTestOptions, "source: body, path: tenant.id", "source: body, path: tenant.id, future: true", 1),
		strings.Replace(flagTestOptions, "kind: organization", "kind: multi", 1),
		strings.Replace(flagTestOptions, "path: tenant.id", "path: tenant..id", 1),
		strings.Replace(flagTestOptions, "path: tenant.id", "path: 'tenant[0].id'", 1),
		strings.Replace(flagTestOptions, "path: region", "path: region.invalid", 1),
		strings.Replace(flagTestOptions, "path: X-Plan", "path: 'X Plan'", 1),
	} {
		var o LaunchDarklyOptions
		err := yaml.Unmarshal([]byte(raw), &o)
		if err == nil {
			err = validateLaunchDarkly(o)
		}
		if err == nil {
			t.Errorf("accepted invalid options: %s", raw)
		}
	}
	for _, fallback := range []string{"false", "0", "''", "null"} {
		kind := map[string]string{"false": "boolean", "0": "number", "''": "string", "null": "json"}[fallback]
		flagOptions(t, fmt.Sprintf("connection: p\nname: r\ncontext: {key: {value: tenant}}\nflags: {flag: {key: f, type: %s, fallback: %s}}", kind, fallback))
	}
}
func TestLaunchDarklyConfigurationReferences(t *testing.T) {
	o := nodeConfigFromYAML(t, "launchdarkly", flagTestOptions)
	cfg := Config{LaunchDarkly: map[string]LaunchDarklyConnection{"production": {SDKKeyEnv: "LD_KEY"}}, Flows: map[string]graph.FlowConfig{"flow": {Nodes: []graph.NodeConfig{o}}}}
	if issues := LaunchDarklyConfigurationIssues(cfg); len(issues) != 0 {
		t.Fatal(issues)
	}
	cfg.Flows["flow"] = graph.FlowConfig{Nodes: []graph.NodeConfig{o, o}}
	if issues := LaunchDarklyConfigurationIssues(cfg); len(issues) == 0 {
		t.Fatal("duplicate accepted")
	}
	cfg.LaunchDarkly = nil
	if issues := LaunchDarklyConfigurationIssues(cfg); len(issues) == 0 {
		t.Fatal("unknown connection accepted")
	}
}
func TestFlagConditionsAcrossNodes(t *testing.T) {
	flags := map[string]any{"rollout": map[string]any{"gate": map[string]any{"value": true, "used_fallback": false}, "settings": map[string]any{"value": map[string]any{"limit": 3.0, "null": nil}}}}
	env := graph.Envelope{Flags: flags}
	cond := FilterConditionOptions{Source: "flags", Path: "rollout.gate.value", Operator: "eq", Value: true}
	nodes := []graph.Node{newFilterNode("f", FilterOptions{Conditions: []FilterConditionOptions{cond}}), newIfElseNode("i", IfElseOptions{Conditions: []FilterConditionOptions{cond}}), newSwitchNode("s", SwitchOptions{Cases: []SwitchCaseOptions{{Port: "next", When: &cond}}, Default: "old"})}
	for _, n := range nodes {
		out, err := n.Run(context.Background(), env)
		if err != nil {
			t.Fatal(err)
		}
		if _, ok := out.(graph.Envelope); !ok {
			t.Fatalf("unexpected %T", out)
		}
	}
	cond = FilterConditionOptions{Source: "flags", Path: "rollout.settings.value.limit", Operator: "gte", Value: 2}
	out, err := newIfElseNode("i", IfElseOptions{Conditions: []FilterConditionOptions{cond}}).Run(context.Background(), env)
	if err != nil || out.(graph.Envelope).Branch != "true" {
		t.Fatalf("numeric routing: %v %v", out, err)
	}
	cond = FilterConditionOptions{Source: "flags", Path: "rollout.settings.value.null", Operator: "exists"}
	out, err = newIfElseNode("i", IfElseOptions{Conditions: []FilterConditionOptions{cond}}).Run(context.Background(), env)
	if err != nil || out.(graph.Envelope).Branch != "true" {
		t.Fatal("null must be present")
	}
}
