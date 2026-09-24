package deploy

import (
	"bytes"
	"errors"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"

	"github.com/jaysongiroux/hook_way/internal/config"
	"github.com/jaysongiroux/hook_way/internal/flowconfig"
)

type object = map[string]any

func k8sObjects(t *testing.T) []object {
	t.Helper()
	files, err := filepath.Glob("kubernetes/*.yaml")
	if err != nil || len(files) == 0 {
		t.Fatalf("manifests: %v %v", files, err)
	}
	var out []object
	for _, f := range files {
		raw, err := os.ReadFile(filepath.Clean(f))
		if err != nil {
			t.Fatal(err)
		}
		dec := yaml.NewDecoder(bytes.NewReader(raw))
		for {
			var o object
			if err := dec.Decode(&o); errors.Is(err, io.EOF) {
				break
			} else if err != nil {
				t.Fatalf("%s: %v", f, err)
			}
			if o != nil {
				out = append(out, o)
			}
		}
	}
	return out
}

func dig(v any, path ...any) any {
	for _, p := range path {
		switch k := p.(type) {
		case string:
			m, ok := v.(object)
			if !ok {
				return nil
			}
			v = m[k]
		case int:
			s, ok := v.([]any)
			if !ok || k >= len(s) {
				return nil
			}
			v = s[k]
		}
	}
	return v
}

func find(t *testing.T, objs []object, kind, name string) object {
	t.Helper()
	for _, o := range objs {
		if o["kind"] == kind && dig(o, "metadata", "name") == name {
			return o
		}
	}
	t.Fatalf("no %s %s", kind, name)
	return nil
}

func containerPorts(c any) map[string]int {
	out := map[string]int{}
	ports, _ := dig(c, "ports").([]any)
	for _, p := range ports {
		out[dig(p, "name").(string)] = dig(p, "containerPort").(int)
	}
	return out
}

func checkHardened(t *testing.T, name string, c any) {
	t.Helper()
	sc := dig(c, "securityContext")
	if dig(sc, "runAsNonRoot") != true || dig(sc, "runAsUser") != 65532 || dig(sc, "readOnlyRootFilesystem") != true ||
		dig(sc, "allowPrivilegeEscalation") != false {
		t.Errorf("%s securityContext = %v", name, sc)
	}
}

func secretEnv(c any) map[string]string {
	out := map[string]string{}
	env, _ := dig(c, "env").([]any)
	for _, e := range env {
		if s := dig(e, "valueFrom", "secretKeyRef", "name"); s != nil {
			out[dig(e, "name").(string)] = s.(string)
		}
	}
	return out
}

// Rules that allow a port, and whom they allow it from. An empty from list
// means anyone.
func ingressFor(policy object, port int) [][]any {
	var out [][]any
	rules, _ := dig(policy, "spec", "ingress").([]any)
	for _, r := range rules {
		ports, _ := dig(r, "ports").([]any)
		for _, p := range ports {
			if dig(p, "port") == port {
				from, _ := dig(r, "from").([]any)
				out = append(out, from)
			}
		}
	}
	return out
}

func TestKubernetesManifests(t *testing.T) {
	objs := k8sObjects(t)
	for _, o := range objs {
		if o["kind"] == "CronJob" {
			t.Fatal("GC runs in-process; the manifests must not ship a CronJob")
		}
	}
	core := find(t, objs, "Deployment", "hookway-core")
	if dig(core, "spec", "replicas") != 5 {
		t.Errorf("core replicas = %v, want 5", dig(core, "spec", "replicas"))
	}
	cc := dig(core, "spec", "template", "spec", "containers", 0)
	if img, _ := dig(cc, "image").(string); !strings.HasPrefix(img, "hookway:") {
		t.Errorf("core image = %v", img)
	}
	if p := containerPorts(cc); p["http"] != 8080 || p["metrics"] != 9090 || p["control"] != 9092 {
		t.Errorf("core ports = %v", p)
	}
	checkHardened(t, "core", cc)
	if env := secretEnv(cc); env["REPLAY_CAPTURE_TOKEN"] != "hookway-replay-tokens" || env["REPLAY_CONTROL_TOKEN"] != "hookway-replay-tokens" {
		t.Errorf("core secret env = %v", env)
	}
	if dig(cc, "readinessProbe", "httpGet", "path") != "/readyz" || dig(cc, "livenessProbe", "httpGet", "path") != "/healthz" {
		t.Errorf("core probes = %v / %v", dig(cc, "readinessProbe"), dig(cc, "livenessProbe"))
	}

	replay := find(t, objs, "Deployment", "hookway-replay")
	if dig(replay, "spec", "replicas") != 2 {
		t.Errorf("replay replicas = %v, want 2", dig(replay, "spec", "replicas"))
	}
	rc := dig(replay, "spec", "template", "spec", "containers", 0)
	if img, _ := dig(rc, "image").(string); !strings.HasPrefix(img, "hookway-replay:") || dig(rc, "args", 0) != "serve" {
		t.Errorf("replay container = %v %v", img, dig(rc, "args"))
	}
	if p := containerPorts(rc); p["api"] != 8080 || p["metrics"] != 9091 {
		t.Errorf("replay ports = %v", p)
	}
	checkHardened(t, "replay", rc)
	for _, v := range []string{"REPLAY_DATABASE_URL", "REPLAY_CONTROL_TOKEN", "REPLAY_CAPTURE_TOKEN", "REPLAY_OPERATOR_TOKEN", "REPLAY_METRICS_TOKEN"} {
		if secretEnv(rc)[v] != "hookway-replay-tokens" {
			t.Errorf("replay lacks %s from the tokens Secret", v)
		}
	}

	job := find(t, objs, "Job", "hookway-replay-migrate")
	if dig(job, "spec", "template", "spec", "containers", 0, "args", 0) != "migrate" {
		t.Errorf("migrate job args = %v", dig(job, "spec", "template", "spec", "containers", 0, "args"))
	}

	control := find(t, objs, "Service", "hookway-core-control")
	if dig(control, "spec", "type") != "ClusterIP" || dig(control, "spec", "ports", 0, "port") != 9092 ||
		dig(control, "spec", "selector", "app.kubernetes.io/name") != "hookway-core" {
		t.Errorf("core-control service = %v", dig(control, "spec"))
	}
	find(t, objs, "Service", "hookway")
	find(t, objs, "Service", "hookway-replay")
	secret := find(t, objs, "Secret", "hookway-replay-tokens")
	for _, key := range []string{"capture-token", "control-token", "operator-token", "metrics-token", "database-url"} {
		if dig(secret, "stringData", key) == nil {
			t.Errorf("secret lacks %s", key)
		}
	}

	// core-control: replay pods only.
	corePolicy := find(t, objs, "NetworkPolicy", "hookway-core")
	for _, from := range ingressFor(corePolicy, 9092) {
		if len(from) != 1 || dig(from[0], "podSelector", "matchLabels", "app.kubernetes.io/name") != "hookway-replay" {
			t.Errorf("core's control port is open to %v, want replay pods only", from)
		}
	}
	if len(ingressFor(corePolicy, 9092)) == 0 {
		t.Error("the core policy never admits replay to the control port")
	}
	// The replay API (and so capture): core pods and labelled operator clients.
	replayPolicy := find(t, objs, "NetworkPolicy", "hookway-replay")
	allowed := []string{"app.kubernetes.io/name=hookway-core", "hookway-replay-client=true"}
	for _, from := range ingressFor(replayPolicy, 8080) {
		if len(from) == 0 {
			t.Fatal("the replay API is open to everyone")
		}
		for _, f := range from {
			labels, _ := dig(f, "podSelector", "matchLabels").(object)
			var got []string
			for k, v := range labels {
				got = append(got, k+"="+v.(string))
			}
			if len(got) != 1 || !slices.Contains(allowed, got[0]) {
				t.Errorf("the replay API admits %v", f)
			}
		}
	}

	// The embedded core config is a valid serving config.
	t.Setenv("REPLAY_CAPTURE_TOKEN", "capture-token-0123456789abcdefghijkl")
	t.Setenv("REPLAY_CONTROL_TOKEN", "control-token-0123456789abcdefghij")
	t.Setenv("ORDERS_WEBHOOK_SECRET", "orders-secret")
	coreCM := find(t, objs, "ConfigMap", "hookway-core")
	cfg, issues := flowconfig.LoadBytes([]byte(dig(coreCM, "data", "config.yaml").(string)), "k8s", true)
	for _, issue := range issues {
		if issue.EffectiveSeverity() == config.SeverityError {
			t.Fatalf("core config: %+v", issues)
		}
	}
	if cfg.Replay.URL != "http://hookway-replay:8080" || cfg.Replay.Control.Addr != ":9092" {
		t.Errorf("core replay block = %+v", cfg.Replay)
	}
	replayCM := find(t, objs, "ConfigMap", "hookway-replay")
	if !strings.Contains(dig(replayCM, "data", "replay.yaml").(string), `control_url: "http://hookway-core-control:9092"`) {
		t.Error("replay's control_url does not name the hookway-core-control service")
	}
}

// Every secretKeyRef names a Secret and key the manifests ship, and the
// migrate Job can be re-applied with a new image once it finished.
func TestKubernetesSecretsResolveAndTheJobCanBeReapplied(t *testing.T) {
	objs := k8sObjects(t)
	shipped := map[string]bool{}
	for _, o := range objs {
		if o["kind"] != "Secret" {
			continue
		}
		data, _ := dig(o, "stringData").(object)
		for key := range data {
			shipped[dig(o, "metadata", "name").(string)+"/"+key] = true
		}
	}
	var walk func(v any)
	walk = func(v any) {
		switch x := v.(type) {
		case object:
			if ref, ok := x["secretKeyRef"].(object); ok {
				if name := ref["name"].(string) + "/" + ref["key"].(string); !shipped[name] {
					t.Errorf("secretKeyRef %s is not in any shipped Secret", name)
				}
			}
			for _, child := range x {
				walk(child)
			}
		case []any:
			for _, child := range x {
				walk(child)
			}
		}
	}
	for _, o := range objs {
		walk(o)
	}
	job := find(t, objs, "Job", "hookway-replay-migrate")
	if dig(job, "spec", "ttlSecondsAfterFinished") == nil {
		t.Error("the migrate Job has no ttlSecondsAfterFinished; re-applying it after an upgrade fails on its immutable template")
	}
}
