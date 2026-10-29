package ci

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

type policyWorkflow struct {
	Permissions map[string]string `yaml:"permissions"`
	On          map[string]struct {
		Inputs map[string]struct {
			Default any `yaml:"default"`
		} `yaml:"inputs"`
	} `yaml:"on"`
	Jobs map[string]struct {
		RunsOn      stringList        `yaml:"runs-on"`
		Permissions map[string]string `yaml:"permissions"`
		Environment string            `yaml:"environment"`
		Uses        string            `yaml:"uses"`
		With        map[string]any    `yaml:"with"`
		Strategy    struct {
			Matrix struct {
				Include []struct {
					Target string `yaml:"target"`
				} `yaml:"include"`
			} `yaml:"matrix"`
		} `yaml:"strategy"`
		Steps []struct {
			Uses string            `yaml:"uses"`
			If   string            `yaml:"if"`
			Run  string            `yaml:"run"`
			With map[string]string `yaml:"with"`
		} `yaml:"steps"`
	} `yaml:"jobs"`
}

func TestConsoleGateUsesStandaloneBrowserAndFullReplayIntegration(t *testing.T) {
	w := loadPolicy(t, "reusable-ci.yml")
	var browserCommands string
	for _, step := range w.Jobs["console-e2e"].Steps {
		browserCommands += step.Run + "\n"
	}
	if !strings.Contains(browserCommands, "make console-e2e") {
		t.Fatal("CI must exercise the standalone operational console browser suite")
	}
	var replaySuite bool
	for _, item := range w.Jobs["integration"].Strategy.Matrix.Include {
		if item.Target == "replay-integration-test" {
			replaySuite = true
		}
	}
	if !replaySuite {
		t.Fatal("CI must exercise native replay through its complete integration suite")
	}
	makefile, err := os.ReadFile("../../Makefile")
	if err != nil {
		t.Fatal(err)
	}
	for _, obsolete := range []string{"TestServeEmbeddedConsole", "HOOKWAY_CONSOLE_ACCEPTANCE", "native-console-acceptance"} {
		if strings.Contains(string(makefile), obsolete) || strings.Contains(browserCommands, obsolete) {
			t.Fatalf("obsolete embedded-console gate%q can report no-tests false green", obsolete)
		}
	}
	// The native suite must stay unfiltered so actual CLI serve/API/worker tests
	// are exercised. The console is not embedded in that binary.
	_, recipe, found := strings.Cut(string(makefile), "\nreplay-integration-test:")
	if !found {
		t.Fatal("native replay integration target missing")
	}
	recipe, _, _ = strings.Cut(recipe, "\n\n")
	if strings.Contains(recipe, " -run ") || !strings.Contains(recipe, "./cmd/hookway-replay") {
		t.Fatal("replay integration gate must run the real native CLI tests without an obsolete selector")
	}
}

func loadPolicy(t *testing.T, name string) policyWorkflow {
	t.Helper()
	data, err := os.ReadFile(filepath.Clean(filepath.Join("..", "..", ".github", "workflows", name)))
	if err != nil {
		t.Fatal(err)
	}
	var w policyWorkflow
	if err := yaml.Unmarshal(data, &w); err != nil {
		t.Fatal(err)
	}
	return w
}

func TestReleaseIsManualFromMain(t *testing.T) {
	release := loadPolicy(t, "release.yml")
	if len(release.On) != 1 {
		t.Fatal("release must not publish on branch/tag pushes or pull requests")
	}
	if _, ok := release.On["workflow_dispatch"]; !ok {
		t.Fatal("release must be manually dispatched")
	}
	for _, name := range []string{"context", "publish"} {
		if got := release.Jobs[name].RunsOn; strings.Join(got, ",") != "ubuntu-latest" {
			t.Fatalf("release %s must use GitHub-hosted runners: %v", name, got)
		}
	}
	gate := release.Jobs["checks"]
	if gate.Uses != "./.github/workflows/reusable-ci.yml" || gate.With["ref"] != "${{ needs.context.outputs.sha }}" || len(gate.With) != 1 {
		t.Fatal("release must use the existing reusable gate at the selected source SHA")
	}
	reusable := loadPolicy(t, "reusable-ci.yml")
	for name, job := range reusable.Jobs {
		if strings.Join(job.RunsOn, ",") != "ubuntu-latest" {
			t.Fatalf("CI job %s must use GitHub-hosted runners", name)
		}
	}
	if release.Permissions["contents"] != "read" || release.Jobs["publish"].Environment != "release" || release.Jobs["publish"].Permissions["contents"] != "write" || release.Jobs["publish"].Permissions["id-token"] != "write" {
		t.Fatal("least-privilege release environment/signing boundary changed")
	}
	guard := release.Jobs["context"].Steps[0].Run
	for _, invalid := range []struct{ event, ref string }{
		{"push", "refs/heads/main"}, {"push", "refs/tags/v1.2.3"},
		{"workflow_dispatch", "refs/heads/v2"}, {"workflow_dispatch", "refs/tags/v1.2.3"},
	} {
		cmd := exec.CommandContext(t.Context(), "bash", "-e", "-o", "pipefail", "-c", guard) // #nosec G204 -- maintained workflow guard; fixture inputs enter environment, not shell commands.
		cmd.Env = append(os.Environ(), "EVENT="+invalid.event, "GITHUB_REF="+invalid.ref, "TAG_INPUT=v1.2.3", "PATH="+t.TempDir())
		if out, err := cmd.CombinedOutput(); err == nil || !strings.Contains(string(out), "manually dispatched from main") {
			t.Fatalf("invalid release invocation admitted: %+v error=%v output=%s", invalid, err, out)
		}
	}
	verifier, err := os.ReadFile("../../scripts/verify-release.sh")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(verifier), `identity="https://github.com/$repository/.github/workflows/release.yml@refs/heads/main"`) {
		t.Fatal("signature verification must match the manual main workflow identity")
	}
}

func TestReleaseRefGuardRejectsUnmergedOrMovedTags(t *testing.T) {
	workflow := loadPolicy(t, "release.yml")
	var sourceGuard string
	for _, step := range workflow.Jobs["context"].Steps {
		if strings.Contains(step.Run, "git merge-base") {
			sourceGuard = step.Run
		}
	}
	if sourceGuard == "" {
		t.Fatal("release context is missing source ancestry guard")
	}
	command := func(dir string, args ...string) {
		t.Helper()
		cmd := exec.CommandContext(t.Context(), "git", args...) // #nosec G204 -- static test commands operate only inside the fixture-owned temporary repository.
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("fixture git:%v %s", err, out)
		}
	}
	dir := t.TempDir()
	command(dir, "init", "--initial-branch=main")
	command(dir, "-c", "user.name=Fixture", "-c", "user.email=fixture@example.test", "commit", "--allow-empty", "-m", "reviewed")
	command(dir, "update-ref", "refs/remotes/origin/main", "HEAD")
	command(dir, "tag", "v1.2.3")
	shaCmd := exec.CommandContext(t.Context(), "git", "rev-parse", "HEAD")
	shaCmd.Dir = dir
	sha, err := shaCmd.Output()
	if err != nil {
		t.Fatal(err)
	}
	full := strings.TrimSpace(string(sha))
	runGuard := func(event string, pass bool) {
		t.Helper()
		outPath := filepath.Join(t.TempDir(), "output")
		cmd := exec.CommandContext(t.Context(), "bash", "-e", "-o", "pipefail", "-c", sourceGuard) // #nosec G204 -- executes this repository's maintained workflow guard against a fixture repository, not user-provided shell code.
		cmd.Dir = dir
		cmd.Env = append(os.Environ(), "GITHUB_SHA="+event, "GITHUB_REF=refs/heads/main", "RELEASE_TAG=v1.2.3", "GITHUB_OUTPUT="+outPath)
		out, err := cmd.CombinedOutput()
		if (err == nil) != pass {
			t.Fatalf("sourceguardpass=%t actualerror=%v output%s", pass, err, out)
		}
		if pass {
			output, err := os.ReadFile(filepath.Clean(outPath))
			if err != nil || !strings.Contains(string(output), "sha="+full) {
				t.Fatalf("release must freeze the exact dispatched main source: %v %s", err, output)
			}
		}
	}
	runGuard(full, true)
	command(dir, "-c", "user.name=Fixture", "-c", "user.email=fixture@example.test", "tag", "--annotate", "--force", "v1.2.3", "--message", "reviewed")
	annotated := exec.CommandContext(t.Context(), "git", "rev-parse", "refs/tags/v1.2.3")
	annotated.Dir = dir
	object, err := annotated.Output()
	if err != nil {
		t.Fatal(err)
	}
	runGuard(full, true)                               // annotated tag peels to the dispatched main source.
	runGuard(strings.TrimSpace(string(object)), false) // dispatch SHA must be a commit, not a tag object.
	runGuard("not-a-sha", false)
	command(dir, "switch", "-c", "unmerged")
	command(dir, "-c", "user.name=Fixture", "-c", "user.email=fixture@example.test", "commit", "--allow-empty", "-m", "unreviewed")
	runGuard(full, false) // checkout differs from event/tag.
	command(dir, "tag", "--force", "v1.2.3")
	shaCmd = exec.CommandContext(t.Context(), "git", "rev-parse", "HEAD")
	shaCmd.Dir = dir
	sha, err = shaCmd.Output()
	if err != nil {
		t.Fatal(err)
	}
	runGuard(strings.TrimSpace(string(sha)), false) // exact tag is not on main.
	command(dir, "switch", "main")
	command(dir, "tag", "--force", "v1.2.3")
	command(dir, "-c", "user.name=Fixture", "-c", "user.email=fixture@example.test", "commit", "--allow-empty", "-m", "next main")
	command(dir, "update-ref", "refs/remotes/origin/main", "HEAD")
	shaCmd = exec.CommandContext(t.Context(), "git", "rev-parse", "HEAD")
	shaCmd.Dir = dir
	sha, err = shaCmd.Output()
	if err != nil {
		t.Fatal(err)
	}
	runGuard(strings.TrimSpace(string(sha)), false) // a stale main tag must not mislabel new source.
}

func TestBranchHardeningDoesNotRequireApplicationImages(t *testing.T) {
	data, err := os.ReadFile("../../Makefile")
	if err != nil {
		t.Fatal(err)
	}
	dependencies := map[string][]string{}
	for _, line := range strings.Split(string(data), "\n") {
		if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, "\t") || strings.HasPrefix(line, " ") {
			continue
		}
		target, prerequisites, ok := strings.Cut(line, ":")
		if ok && !strings.Contains(prerequisites, "=") {
			dependencies[target] = strings.Fields(prerequisites)
		}
	}
	if len(dependencies["hardening-check-gates"]) == 0 {
		t.Fatal("branch hardening gate is missing")
	}
	visited := map[string]bool{}
	var visit func(string)
	visit = func(target string) {
		if visited[target] {
			return
		}
		visited[target] = true
		switch target {
		case "docker-build", "image-smoke", "vuln-scan", "compose-e2e-test", "release-check", "release-check-gates":
			t.Fatalf("branch hardening unexpectedly requires packaged-image gate %s", target)
		}
		for _, dependency := range dependencies[target] {
			visit(dependency)
		}
	}
	visit("hardening-check")
	visit("hardening-check-gates") // recursive make recipe, intentionally installed after npm ci.
	for _, required := range []string{"test", "test-race", "web-test", "web-e2e", "console-e2e", "replay-acceptance-test", "release-policy-check", "govulncheck", "web-audit"} {
		if !visited[required] {
			t.Fatalf("branch hardening lost %s", required)
		}
	}
}

func TestReleaseTagGrammarAndPrereleasePromotion(t *testing.T) {
	w := loadPolicy(t, "release.yml")
	guard := w.Jobs["context"].Steps[0].Run
	for _, tag := range []string{"v1.2.3", "v0.0.0", "v1.2.3-rc.1", "v1.2.3-rc-zero", "v1.2.3.extra", "v01.2.3", "v1.2.3+build", "v1.2.3-01", "v1.2.3-rc.01", "v1.2.3\nextra"} {
		valid := tag == "v1.2.3" || tag == "v0.0.0" || tag == "v1.2.3-rc.1" || tag == "v1.2.3-rc-zero"
		outPath := filepath.Join(t.TempDir(), "output")
		cmd := exec.CommandContext(t.Context(), "bash", "-e", "-o", "pipefail", "-c", guard) // #nosec G204 -- executes the maintained release tag guard; test values enter environment variables, never command text.
		cmd.Env = append(os.Environ(), "EVENT=workflow_dispatch", "TAG_INPUT="+tag, "GITHUB_REF=refs/heads/main", "GITHUB_OUTPUT="+outPath)
		if out, err := cmd.CombinedOutput(); (err == nil) != valid {
			t.Fatalf("tag%q valid%t actualerror%v output%s", tag, valid, err, out)
		}
		if valid {
			output, err := os.ReadFile(filepath.Clean(outPath))
			if err != nil {
				t.Fatal(err)
			}
			want := "prerelease=false"
			if strings.Contains(tag, "-") {
				want = "prerelease=true"
			}
			if !strings.Contains(string(output), want) {
				t.Fatal("tag promotion classification changed")
			}
		}
	}
}

func TestReleaseVerifiersRejectAllInputsBeforeNetworkTools(t *testing.T) {
	image := "docker.io/fixture/hookway@sha256:" + strings.Repeat("a", 64)
	commit := strings.Repeat("b", 40)
	for _, tag := range []string{"v1.2.3.extra", "v01.2.3", "v1.2.3+build", "v1.2.3-01", "v1.2.3-rc.01", "v1.2.3\nextra"} {
		cmd := exec.CommandContext(t.Context(), "bash", "../../scripts/verify-release.sh", image, image, image, tag, commit, "fixture/repo", filepath.Join(t.TempDir(), "evidence")) // #nosec G204 -- fixture values are positional arguments to the maintained verifier, never shell command text.
		cmd.Env = append(os.Environ(), "PATH="+t.TempDir())
		out, err := cmd.CombinedOutput()
		if err == nil || !strings.Contains(string(out), "invalid release tag") && !strings.Contains(string(out), "numeric prerelease") {
			t.Fatalf("tag%q not rejected before tools:%v %s", tag, err, out)
		}
	}
	for _, third := range []string{"fixture:latest", "--certificate-identity=evil@sha256:" + strings.Repeat("a", 64), image + "\n"} {
		cmd := exec.CommandContext(t.Context(), "bash", "../../scripts/verify-release.sh", image, image, third, "v1.2.3-rc.1", commit, "fixture/repo", filepath.Join(t.TempDir(), "evidence")) // #nosec G204 -- intentionally invalid fixture arguments test preflight without network access.
		cmd.Env = append(os.Environ(), "PATH="+t.TempDir())
		out, err := cmd.CombinedOutput()
		if err == nil || !strings.Contains(string(out), "expected immutable image digest") {
			t.Fatalf("invalid third image was not preflighted:%v %s", err, out)
		}
	}
	python, err := exec.LookPath("python3")
	if err != nil {
		t.Fatal("release policy regression requires python3", err)
	}
	// #nosec G204 -- discovered Python interpreter executes static maintained regression code; no user-provided executable or commands.
	cmd := exec.CommandContext(t.Context(), python, "-c", `
import importlib.util
spec=importlib.util.spec_from_file_location('smoke','../../scripts/release-smoke.py')
smoke=importlib.util.module_from_spec(spec);spec.loader.exec_module(smoke)
smoke.run=lambda *args: (_ for _ in ()).throw(AssertionError('network tool invoked before validation'))
digest='docker.io/fixture/core@sha256:'+'a'*64
for value in ('fixture:latest','--flag@sha256:'+'a'*64,digest+'\n'):
    try: smoke.checked_image(value,'v1.2.3','b'*40)
    except ValueError: pass
    else: raise AssertionError('invalid digest admitted')
for version in ('v1.2.3.extra','v01.2.3','v1.2.3+build','v1.2.3-01','v1.2.3-rc.01'):
    try: smoke.checked_image(digest,version,'b'*40)
    except ValueError: pass
    else: raise AssertionError('invalid identity admitted')
for version in ('v1.2.3','v0.0.0','v1.2.3-rc.1','v1.2.3-rc-zero'):
    smoke.validate_identity(version,'b'*40)
import json
identity='hookway v1.2.3 ('+'b'*40+')'
def docker_stub(*args):
    if args[1]=='pull': return ''
    if args[1:3]==('image','inspect'): return json.dumps([{'Os':'linux','Architecture':'amd64','Id':'fixture-image'}])
    if args[1]=='run': return identity
    raise AssertionError('unexpected docker operation')
smoke.run=docker_stub
smoke.checked_image(digest,'v1.2.3','b'*40,expected_program='hookway')
try: smoke.checked_image(digest,'v1.2.3','b'*40,expected_program='hookway-replay')
except RuntimeError: pass
else: raise AssertionError('core binary accepted in replay slot')
identity='hookway-replay v1.2.3 ('+'b'*40+')'
smoke.checked_image(digest,'v1.2.3','b'*40,expected_program='hookway-replay')
try: smoke.checked_image(digest,'v1.2.3','b'*40,expected_program='hookway')
except RuntimeError: pass
else: raise AssertionError('replay binary accepted in core slot')
`)
	cmd.Env = append(os.Environ(), "PYTHONDONTWRITEBYTECODE=1")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("smoke validation:%v %s", err, out)
	}
}

func TestConsoleSmokeLocalPlatformDoesNotWidenReleaseContract(t *testing.T) {
	makefile, err := os.ReadFile("../../Makefile")
	if err != nil {
		t.Fatal(err)
	}
	_, recipe, found := strings.Cut(string(makefile), "\nimage-smoke:")
	if !found {
		t.Fatal("local image smoke target missing")
	}
	recipe, _, _ = strings.Cut(recipe, "\n\n")
	if !strings.Contains(recipe, `platform=smoke.run("docker","image","inspect","--format","{{.Os}}/{{.Architecture}}",sys.argv[1])`) {
		t.Fatal("unsigned local console smoke must select the inspected image platform")
	}
	cmd := exec.CommandContext(t.Context(), "python3", "-c", `
import importlib.util,json
spec=importlib.util.spec_from_file_location('smoke','../../scripts/release-smoke.py')
smoke=importlib.util.module_from_spec(spec);spec.loader.exec_module(smoke)
class LaunchObserved(Exception): pass
launches=[]
def observe_launch(*args):
    launches.append(args)
    raise LaunchObserved()
smoke.run=observe_launch
for kwargs,expected in (({},'linux/amd64'),({'platform':'linux/arm64'},'linux/arm64')):
    try: smoke.console_install('fixture:local','dev','none',**kwargs)
    except LaunchObserved: pass
    else: raise AssertionError('launch sentinel not reached')
    args=launches[-1]
    assert args[:3]==('docker','run','-d')
    assert args[args.index('--platform')+1]==expected
# The launch sentinel fires before container creation, HTTP probes or cleanup.
digest='docker.io/fixture/console@sha256:'+'a'*64
architecture='arm64';pulls=[];installs=[]
def docker_stub(*args):
    if args[1]=='pull':
        pulls.append(args)
        return ''
    if args[1:3]==('image','inspect'):
        return json.dumps([{'Os':'linux','Architecture':architecture,'Id':'fixture-image'}])
    raise AssertionError('unexpected docker operation')
def install_stub(image,version,commit,**kwargs):
    installs.append(kwargs)
    return {'health':'fixture-only'}
smoke.run=docker_stub;smoke.console_install=install_stub
try: smoke.checked_image(digest,'v1.2.3','b'*40,static=True)
except RuntimeError as error:
    assert 'linux/amd64' in str(error)
else: raise AssertionError('arm64 published console admitted')
assert not installs
assert pulls[-1][pulls[-1].index('--platform')+1]=='linux/amd64'
architecture='amd64'
report=smoke.checked_image(digest,'v1.2.3','b'*40,static=True)
assert installs==[{'platform':'linux/amd64'}]
assert report['platform']=='linux/amd64'
`)
	cmd.Env = append(os.Environ(), "PYTHONDONTWRITEBYTECODE=1")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("console platform boundary:%v %s", err, out)
	}
}

func TestProvenanceUsesSourceRecordNotMatchingBuildArgument(t *testing.T) {
	cmd := exec.CommandContext(t.Context(), "python3", "-c", `
import importlib.util
spec=importlib.util.spec_from_file_location('provenance','../../scripts/verify-provenance.py')
module=importlib.util.module_from_spec(spec);spec.loader.exec_module(module)
repo='fixture/repo';commit='a'*40;sbom={'SPDX':{'spdxVersion':'SPDX-2.3'}}
source={'uri':'git+https://github.com/'+repo+'.git#'+commit,'digest':{'sha1':commit}}
module.verify({'SLSA':{'invocation':{'configSource':source}}},sbom,repo,commit)
module.verify({'linux/amd64':{'SLSA':{'buildDefinition':{'externalParameters':{'configSource':source}}}}},{'linux/amd64':sbom},repo,commit)
for bad in ({},{'SLSA':{'invocation':{'parameters':{'COMMIT':commit,'repository':repo}}}},{'SLSA':{'invocation':{'configSource':{'uri':'https://github.com/evil/repo.git','digest':{'sha1':commit}}}}},{'SLSA':{'invocation':{'configSource':{'uri':source['uri'],'digest':{'sha1':'b'*40}}}}}):
    try: module.verify(bad,sbom,repo,commit)
    except ValueError: pass
    else: raise AssertionError('missing or wrong configSource admitted')
try: module.verify({'SLSA':{'invocation':{'configSource':source}}},{},repo,commit)
except ValueError: pass
else: raise AssertionError('missing SBOM admitted')
`)
	cmd.Env = append(os.Environ(), "PYTHONDONTWRITEBYTECODE=1")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("provenance guard:%v %s", err, out)
	}
}
