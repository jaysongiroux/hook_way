package deploy

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

type workflow struct {
	Jobs map[string]struct {
		Needs stringList `yaml:"needs"`
		Steps []struct {
			Name string            `yaml:"name"`
			If   string            `yaml:"if"`
			Uses string            `yaml:"uses"`
			Run  string            `yaml:"run"`
			With map[string]string `yaml:"with"`
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
	raw, err := os.ReadFile(filepath.Clean(filepath.Join("..", ".github", "workflows", name)))
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
	for _, want := range []string{"docker run --rm hookway:local version", "docker run --rm hookway-replay:local version"} {
		if !strings.Contains(run, want) {
			t.Errorf("reusable-ci.yml never runs %q", want)
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
		{"web-e2e", "integration", "image"},
	}
	staged := map[string]bool{"success": true}
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
}

// A release publishes nothing until the whole CI gate has passed.
func TestReleasePublishesOnlyAfterChecks(t *testing.T) {
	jobs := loadWorkflow(t, "release.yml").Jobs
	if !jobs["publish"].Needs.has("checks") {
		t.Fatalf("release publish needs %v, want checks", jobs["publish"].Needs)
	}
}
