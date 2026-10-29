package ci

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

type workflow struct {
	Jobs map[string]struct {
		Name     string     `yaml:"name"`
		If       string     `yaml:"if"`
		Needs    stringList `yaml:"needs"`
		Strategy struct {
			FailFast *bool `yaml:"fail-fast"`
			Matrix   struct {
				Shard []int `yaml:"shard"`
			} `yaml:"matrix"`
		} `yaml:"strategy"`
		Steps []struct {
			Name string            `yaml:"name"`
			If   string            `yaml:"if"`
			Uses string            `yaml:"uses"`
			Run  string            `yaml:"run"`
			With map[string]string `yaml:"with"`
			Env  map[string]string `yaml:"env"`
		} `yaml:"steps"`
	} `yaml:"jobs"`
}

// stringList decodes a YAML value that is either one string or a list of
// strings, as `needs:` is.
type stringList []string

func (l *stringList) UnmarshalYAML(n *yaml.Node) error {
	if n.Kind == yaml.ScalarNode {
		*l = []string{n.Value}
		return nil
	}
	var list []string
	if err := n.Decode(&list); err != nil {
		return err
	}
	*l = list
	return nil
}

func (l stringList) has(name string) bool {
	for _, v := range l {
		if v == name {
			return true
		}
	}
	return false
}

func loadWorkflow(t *testing.T, name string) workflow {
	t.Helper()
	raw, err := os.ReadFile(filepath.Clean(filepath.Join("..", "..", ".github", "workflows", name)))
	if err != nil {
		t.Fatal(err)
	}
	var w workflow
	if err := yaml.Unmarshal(raw, &w); err != nil {
		t.Fatal(err)
	}
	return w
}

// trivyRefs lists the image refs a workflow scans with Trivy.
func trivyRefs(w workflow) map[string]bool {
	refs := map[string]bool{}
	for _, job := range w.Jobs {
		for _, s := range job.Steps {
			if strings.HasPrefix(s.Uses, "aquasecurity/trivy-action") {
				refs[s.With["image-ref"]] = true
			}
		}
	}
	return refs
}

func TestWorkflowsBuildAndScanBothImages(t *testing.T) {
	for _, name := range []string{"reusable-ci.yml", "security.yml"} {
		refs := trivyRefs(loadWorkflow(t, name))
		if !refs["hookway:local"] || !refs["hookway-replay:local"] {
			t.Errorf("%s scans %v, want hookway:local and hookway-replay:local", name, refs)
		}
	}
	targets := map[string]string{}
	for _, job := range loadWorkflow(t, "release.yml").Jobs {
		for _, s := range job.Steps {
			if strings.HasPrefix(s.Uses, "docker/build-push-action") {
				targets[s.With["target"]] = s.With["tags"]
			}
		}
	}
	if _, ok := targets["core"]; !ok {
		t.Errorf("release pushes targets %v, want core", targets)
	}
	if tags, ok := targets["replay"]; !ok || !strings.Contains(tags, "replay") {
		t.Errorf("release pushes targets %v, want replay with its own image tags", targets)
	}
}

// The replay image is scanned even when core's scan failed, and the two
// images' SARIF results stay separate analyses in code scanning.
func TestReplayScanRunsAfterACoreFindingAndKeepsItsOwnCategory(t *testing.T) {
	for _, name := range []string{"reusable-ci.yml", "security.yml"} {
		w := loadWorkflow(t, name)
		categories := map[string]int{}
		for _, job := range w.Jobs {
			for _, s := range job.Steps {
				if strings.HasPrefix(s.Uses, "aquasecurity/trivy-action") && s.With["image-ref"] == "hookway-replay:local" &&
					!strings.Contains(s.If, "failure()") && !strings.Contains(s.If, "always()") {
					t.Errorf("%s: the replay scan does not run after a failed core scan (if: %q)", name, s.If)
				}
				if strings.HasPrefix(s.Uses, "github/codeql-action/upload-sarif") {
					categories[s.With["category"]]++
				}
			}
		}
		for category, n := range categories {
			if category == "" || n > 1 {
				t.Errorf("%s: SARIF uploads share category %q (%d uploads)", name, category, n)
			}
		}
	}
}

// CI runs both images once, so a defect that only shows at image runtime
// (a missing binary, a bad entrypoint) fails the build.
func TestCIRunsBothImages(t *testing.T) {
	var run string
	for _, job := range loadWorkflow(t, "reusable-ci.yml").Jobs {
		for _, s := range job.Steps {
			run += s.Run + "\n"
		}
	}
	for _, want := range []string{"make image-smoke"} {
		if !strings.Contains(run, want) {
			t.Errorf("reusable-ci.yml never runs %q", want)
		}
	}
	makefile, err := os.ReadFile("../../Makefile")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`docker run --rm "$(IMAGE)" version`, `docker run --rm "$(REPLAY_IMAGE)" version`, `smoke.console_install(sys.argv[1],sys.argv[2],sys.argv[3],platform=`} {
		if !strings.Contains(string(makefile), want) {
			t.Fatalf("canonical image-smoke no longer exercises%s", want)
		}
	}
}

// CI runs in stages: no test runs until every lint job passes, and nothing
// slow (browsers, containers, images) starts until every test job passes. A
// failed stage skips the rest, and the summary job counts a skip as a failure.
func TestCIRunsInStages(t *testing.T) {
	w := loadWorkflow(t, "reusable-ci.yml")
	stages := [][]string{
		{"formatting", "golangci", "govulncheck", "static", "web-lint"},
		{"go", "go-race", "examples", "web", "coverage", "observability"},
		{"web-e2e", "website-e2e", "console-e2e", "integration", "image"},
	}
	staged := map[string]bool{"success": true, "browser-reports": true}
	var previous []string // the stage before; none for the first
	for _, stage := range stages {
		for _, name := range stage {
			job, ok := w.Jobs[name]
			if !ok {
				t.Errorf("reusable-ci.yml has no job %q", name)
				continue
			}
			staged[name] = true
			if previous == nil && len(job.Needs) > 0 {
				t.Errorf("lint job %q waits for %v; the first stage starts at once", name, job.Needs)
			}
			for _, prev := range previous {
				if !job.Needs.has(prev) {
					t.Errorf("job %q does not wait for %q", name, prev)
				}
			}
		}
		previous = stage
	}
	for name := range w.Jobs {
		if !staged[name] {
			t.Errorf("job %q belongs to no stage", name)
		}
		if name != "success" && !w.Jobs["success"].Needs.has(name) {
			t.Errorf("the summary check does not wait for %q", name)
		}
	}
	if !w.Jobs["browser-reports"].Needs.has("web-e2e") {
		t.Fatal("browser report merge must wait for every builder shard")
	}
}

// A release publishes nothing until the whole CI gate has passed.
func TestReleasePublishesOnlyAfterChecks(t *testing.T) {
	jobs := loadWorkflow(t, "release.yml").Jobs
	if !jobs["publish"].Needs.has("checks") {
		t.Fatalf("release publish needs %v, want checks", jobs["publish"].Needs)
	}
}

func TestCIIncludesMCPRuntimeAndBrowserTests(t *testing.T) {
	w := loadWorkflow(t, "reusable-ci.yml")
	for job, command := range map[string]string{"go": "make unit", "go-race": "make race", "web": "make web-test", "web-e2e": "make web-e2e"} {
		found := false
		for _, step := range w.Jobs[job].Steps {
			found = found || step.Run == command
		}
		if !found {
			t.Errorf("%s no longer runs the complete suite containing MCP tests: %s", job, command)
		}
	}
	hasGo, discoversMCP := false, false
	for _, step := range w.Jobs["web-e2e"].Steps {
		hasGo = hasGo || strings.HasPrefix(step.Uses, "actions/setup-go@")
		discoversMCP = discoversMCP || strings.Contains(step.Run, "--list e2e/mcp-gateway.spec.ts")
	}
	if !hasGo || !discoversMCP {
		t.Fatal("MCP browser tests require Go for the real gateway fixture and explicit test discovery")
	}
}

func TestBrowserShardsPreserveCompleteSuitesAndReports(t *testing.T) {
	w := loadWorkflow(t, "reusable-ci.yml")
	builder := w.Jobs["web-e2e"]
	if !reflect.DeepEqual(builder.Strategy.Matrix.Shard, []int{1, 2, 3, 4}) || builder.Strategy.FailFast == nil || *builder.Strategy.FailFast {
		t.Fatal("all four builder shards must run, including after another shard fails")
	}
	for name, command := range map[string]string{"web-e2e": "make web-e2e", "website-e2e": "make website-e2e", "console-e2e": "make console-e2e"} {
		job := w.Jobs[name]
		found, report := false, false
		for _, step := range job.Steps {
			if step.Run == command {
				found = true
				want := "--reporter=github,html"
				if name == "web-e2e" {
					want = "--shard=${{ matrix.shard }}/4 --reporter=github,blob"
				}
				if step.Env["PLAYWRIGHT_ARGS"] != want {
					t.Errorf("%s must run its complete suite with only shard/report arguments", name)
				}
			}
			if strings.HasPrefix(step.Uses, "actions/upload-artifact@") && step.If == "${{ !cancelled() }}" && step.With["if-no-files-found"] == "error" {
				wantName, wantPath := "playwright-"+strings.TrimSuffix(name, "-e2e")+"-report", "web/playwright-report/"
				if name == "web-e2e" {
					wantName, wantPath = "playwright-builder-blob-${{ matrix.shard }}", "web/blob-report/"
				}
				report = step.With["name"] == wantName && step.With["path"] == wantPath
			}
		}
		if !found || !report {
			t.Errorf("%s must run the complete browser suite and preserve its own failure report", name)
		}
		for _, other := range []string{"web-e2e", "website-e2e", "console-e2e"} {
			if job.Needs.has(other) {
				t.Errorf("%s must not wait behind browser suite %s", name, other)
			}
		}
	}
	merge := w.Jobs["browser-reports"]
	if merge.If != "${{ !cancelled() && needs.web-e2e.result != 'skipped' }}" {
		t.Fatal("merge must retain diagnostics after failed tests without running after skipped stages")
	}
	download, merges, upload := false, false, false
	for _, step := range merge.Steps {
		download = download || (strings.HasPrefix(step.Uses, "actions/download-artifact@") && step.With["pattern"] == "playwright-builder-blob-*" && step.With["merge-multiple"] == "true")
		merges = merges || strings.Contains(step.Run, "playwright merge-reports --reporter=html ./all-blob-reports")
		upload = upload || (strings.HasPrefix(step.Uses, "actions/upload-artifact@") && step.With["name"] == "playwright-report")
	}
	if !download || !merges || !upload {
		t.Fatal("all shard reports must merge into the existing playwright-report artifact")
	}
	if w.Jobs["success"].Name != "All checks passed" {
		t.Fatal("the branch protection check name must remain stable")
	}
}
