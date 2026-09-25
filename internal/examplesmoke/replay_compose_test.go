package examplesmoke

import (
	"net"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"gopkg.in/yaml.v3"
)

type composeService struct {
	Build *struct {
		Context    string `yaml:"context"`
		Dockerfile string `yaml:"dockerfile"`
		Target     string `yaml:"target"`
	} `yaml:"build"`
	Image       string            `yaml:"image"`
	Command     []string          `yaml:"command"`
	Environment map[string]string `yaml:"environment"`
	DependsOn   map[string]struct {
		Condition string `yaml:"condition"`
	} `yaml:"depends_on"`
	Deploy struct {
		Replicas int `yaml:"replicas"`
	} `yaml:"deploy"`
	Volumes         []string `yaml:"volumes"`
	Ports           []string `yaml:"ports"`
	Restart         string   `yaml:"restart"`
	StopGracePeriod string   `yaml:"stop_grace_period"`
}

func readYAML(t *testing.T, path string, into any) {
	t.Helper()
	raw, err := os.ReadFile(filepath.Clean(path))
	if err != nil {
		t.Fatal(err)
	}
	if err := yaml.Unmarshal(raw, into); err != nil {
		t.Fatalf("%s: %v", path, err)
	}
}

func port(t *testing.T, hostport string) string {
	t.Helper()
	_, p, err := net.SplitHostPort(hostport)
	if err != nil {
		t.Fatalf("%q: %v", hostport, err)
	}
	return p
}

// replayStack names the pieces of one compose file that runs core with the
// replay service.
type replayStack struct {
	dir, compose     string // compose file, relative to examples/
	context          string // the build context the services must use
	core             string // core's service name
	coreCfg, replCfg string // config files, relative to the compose file
}

// checkReplayStack asserts a compose stack runs as written: every name
// resolves, replay waits for its migration, the two sides agree on ports,
// and the default tokens pass validation. It returns the services.
func checkReplayStack(t *testing.T, st replayStack) map[string]composeService {
	t.Helper()
	dir := filepath.Join(examplesDir(t), st.dir)
	var compose struct {
		Services map[string]composeService `yaml:"services"`
	}
	readYAML(t, filepath.Join(dir, st.compose), &compose)
	s := compose.Services
	for _, name := range []string{"postgres", "migrate", "replay", st.core} {
		if _, ok := s[name]; !ok {
			t.Fatalf("service %q is missing", name)
		}
	}
	for name, target := range map[string]string{st.core: "core", "replay": "replay", "migrate": "replay"} {
		if b := s[name].Build; b == nil || b.Target != target || b.Context != st.context {
			t.Errorf("service %s builds %+v, want target %s from the repo root (%s)", name, b, target, st.context)
		}
	}
	if m := s["migrate"]; len(m.Command) == 0 || m.Command[0] != "migrate" || m.Restart != "no" ||
		m.DependsOn["postgres"].Condition != "service_healthy" {
		t.Errorf("migrate = %+v; want a one-shot migrate after postgres is healthy", m)
	}
	if r := s["replay"]; len(r.Command) == 0 || r.Command[0] != "serve" ||
		r.DependsOn["migrate"].Condition != "service_completed_successfully" {
		t.Errorf("replay = %+v; want serve after the migration completed", r)
	}
	defaults := regexp.MustCompile(`\$\{([A-Z_]+_TOKEN):-([^}]*)\}`)
	raw, err := os.ReadFile(filepath.Clean(filepath.Join(dir, st.compose)))
	if err != nil {
		t.Fatal(err)
	}
	found := defaults.FindAllStringSubmatch(string(raw), -1)
	if len(found) < 3 {
		t.Fatalf("token defaults = %v, want capture, control, and operator", found)
	}
	for _, m := range found {
		if len(m[2]) < 32 {
			t.Errorf("default %s is %d characters; both services require at least 32", m[1], len(m[2]))
		}
	}

	var core struct {
		Replay struct {
			URL     string `yaml:"url"`
			Control struct {
				Addr string `yaml:"addr"`
			} `yaml:"control"`
		} `yaml:"replay"`
	}
	readYAML(t, filepath.Join(dir, st.coreCfg), &core)
	var replay struct {
		Server struct {
			Addr string `yaml:"addr"`
		} `yaml:"server"`
		Core struct {
			ControlURL string `yaml:"control_url"`
		} `yaml:"core"`
	}
	readYAML(t, filepath.Join(dir, st.replCfg), &replay)
	replayURL, err := url.Parse(core.Replay.URL)
	if err != nil || replayURL.Hostname() != "replay" || replayURL.Port() != port(t, replay.Server.Addr) {
		t.Errorf("core uploads to %q; want the replay service on its server port %s", core.Replay.URL, replay.Server.Addr)
	}
	controlURL, err := url.Parse(replay.Core.ControlURL)
	if err != nil || controlURL.Hostname() != st.core || controlURL.Port() != port(t, core.Replay.Control.Addr) {
		t.Errorf("replay redrives through %q; want %s's control listener %s", replay.Core.ControlURL, st.core, core.Replay.Control.Addr)
	}
	mounts := map[string]string{st.core: st.coreCfg, "replay": st.replCfg, "migrate": st.replCfg}
	for name, cfg := range mounts {
		if !strings.Contains(strings.Join(s[name].Volumes, " "), "./"+cfg+":") {
			t.Errorf("%s does not mount ./%s: %v", name, cfg, s[name].Volumes)
		}
	}
	// docker compose stop must give core time to finish its redrives
	// (replay's execution_timeout plus core's shutdown_timeout) and replay
	// time to finish its runs.
	for name, min := range map[string]time.Duration{st.core: 60 * time.Second, "replay": 45 * time.Second} {
		if d, err := time.ParseDuration(s[name].StopGracePeriod); err != nil || d < min {
			t.Errorf("%s stop_grace_period = %q, want at least %v", name, s[name].StopGracePeriod, min)
		}
	}
	return s
}

type coreDestinations struct {
	Destinations map[string]struct {
		Type    string `yaml:"type"`
		Webhook struct {
			URL string `yaml:"url"`
		} `yaml:"webhook"`
	} `yaml:"destinations"`
	Flows map[string]struct {
		DeliveryFailurePolicy string `yaml:"delivery_failure_policy"`
	} `yaml:"flows"`
}

// webhookHosts maps each webhook destination to its URL's host, failing when
// a host is not a compose service.
func webhookHosts(t *testing.T, cfg coreDestinations, services map[string]composeService) map[string]string {
	t.Helper()
	hosts := map[string]string{}
	for name, d := range cfg.Destinations {
		if d.Type != "webhook" {
			continue
		}
		u, err := url.Parse(d.Webhook.URL)
		if err != nil {
			t.Fatalf("destination %s url %q: %v", name, d.Webhook.URL, err)
		}
		if _, ok := services[u.Hostname()]; !ok {
			t.Errorf("destination %s points at %q, which is not a compose service", name, u.Hostname())
		}
		hosts[name] = u.Hostname()
	}
	return hosts
}

// The replay example runs two replicas of each side.
func TestReplayComposeExample(t *testing.T) {
	s := checkReplayStack(t, replayStack{
		dir: "replay", compose: "docker-compose.yaml", context: "../..",
		core: "core", coreCfg: "core.yaml", replCfg: "replay.yaml",
	})
	if s["core"].Deploy.Replicas != 2 || s["replay"].Deploy.Replicas != 2 {
		t.Errorf("replicas: core %d, replay %d; want 2 and 2", s["core"].Deploy.Replicas, s["replay"].Deploy.Replicas)
	}
	// A capture needs a partial failure: the two destinations must be able
	// to fail independently, so each gets its own sink service.
	var cfg coreDestinations
	readYAML(t, filepath.Join(examplesDir(t), "replay", "core.yaml"), &cfg)
	hosts := webhookHosts(t, cfg, s)
	distinct := map[string]bool{}
	for _, h := range hosts {
		distinct[h] = true
	}
	if len(hosts) < 2 || len(distinct) != len(hosts) {
		t.Errorf("destinations %v share a sink; stopping one could never produce a partial failure", hosts)
	}
}

// make dev-up runs the replay service by default: the GitHub flow fans out to
// RabbitMQ and an HTTP sink under skip_failed, so stopping the sink leaves a
// captured failure to retry.
func TestDevComposeRunsReplayByDefault(t *testing.T) {
	s := checkReplayStack(t, replayStack{
		dir: ".", compose: "docker-compose.yaml", context: "..",
		core: "hookway", coreCfg: "dev/core.yaml", replCfg: "dev/replay.yaml",
	})
	if s["hookway"].DependsOn["rabbitmq"].Condition != "service_healthy" {
		t.Errorf("hookway depends on %+v; want rabbitmq healthy first, or its first publish races the broker", s["hookway"].DependsOn)
	}
	var cfg coreDestinations
	readYAML(t, filepath.Join(examplesDir(t), "dev", "core.yaml"), &cfg)
	if len(webhookHosts(t, cfg, s)) == 0 {
		t.Error("no webhook destination points at a compose sink; there is nothing to stop for a partial failure")
	}
	if len(cfg.Flows) == 0 {
		t.Fatal("dev/core.yaml has no flows")
	}
	for name, f := range cfg.Flows {
		if f.DeliveryFailurePolicy != "skip_failed" {
			t.Errorf("flow %s uses %q; a sink outage is captured only under skip_failed", name, f.DeliveryFailurePolicy)
		}
	}
	// The builder is the next thing a developer opens: `hookway editor`
	// defaults to 8090 and `make wasm-harness` to 8091, so the stack must
	// leave both free.
	for name, svc := range s {
		for _, p := range svc.Ports {
			host, _, _ := strings.Cut(p, ":")
			if host == "8090" || host == "8091" {
				t.Errorf("service %s publishes %s, which `hookway editor` or `make wasm-harness` needs", name, p)
			}
		}
	}
}
