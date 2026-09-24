package deploy

import (
	"os"
	"regexp"
	"strings"
	"testing"
)

type dockerStage struct {
	from, name, body string
}

func dockerStages(t *testing.T) []dockerStage {
	t.Helper()
	raw, err := os.ReadFile("../Dockerfile")
	if err != nil {
		t.Fatal(err)
	}
	from := regexp.MustCompile(`(?m)^FROM\s+(\S+)(?:\s+AS\s+(\S+))?\s*$`)
	src := string(raw)
	locs := from.FindAllStringSubmatchIndex(src, -1)
	var stages []dockerStage
	for i, loc := range locs {
		end := len(src)
		if i+1 < len(locs) {
			end = locs[i+1][0]
		}
		st := dockerStage{from: src[loc[2]:loc[3]], body: src[loc[1]:end]}
		if loc[4] >= 0 {
			st.name = src[loc[4]:loc[5]]
		}
		stages = append(stages, st)
	}
	return stages
}

// Both services ship from one Dockerfile so they cannot drift: one Go pin,
// scratch runtimes, the same non-root user, a CA bundle, and a healthcheck.
func TestDockerfileBuildsCoreAndReplay(t *testing.T) {
	stages := dockerStages(t)
	byName := map[string]dockerStage{}
	var goPins []string
	for _, s := range stages {
		byName[s.name] = s
		if strings.HasPrefix(s.from, "golang:") {
			goPins = append(goPins, s.from)
		}
	}
	if len(goPins) != 1 {
		t.Fatalf("Go toolchain pins = %v, want exactly one shared by both builds", goPins)
	}
	for _, build := range []string{"build-core", "build-replay"} {
		if byName[build].from != "source" {
			t.Errorf("stage %s builds FROM %q, want the shared source stage", build, byName[build].from)
		}
	}
	if !strings.Contains(byName["build-core"].body, "./cmd/hookway ") && !strings.Contains(byName["build-core"].body, "./cmd/hookway\n") {
		t.Error("build-core does not build ./cmd/hookway")
	}
	if !strings.Contains(byName["build-replay"].body, "./cmd/hookway-replay") {
		t.Error("build-replay does not build ./cmd/hookway-replay")
	}
	for name, bin := range map[string]string{"core": "hookway", "replay": "hookway-replay"} {
		s, ok := byName[name]
		if !ok {
			t.Fatalf("no %s stage", name)
		}
		for _, want := range []string{
			"USER 65532:65532",
			"/etc/ssl/certs/ca-certificates.crt",
			`CMD ["/usr/local/bin/` + bin + `", "healthcheck"]`,
			`ENTRYPOINT ["/usr/local/bin/` + bin + `"]`,
		} {
			if !strings.Contains(s.body, want) {
				t.Errorf("stage %s lacks %s", name, want)
			}
		}
		if s.from != "scratch" {
			t.Errorf("stage %s runs FROM %q, want scratch", name, s.from)
		}
	}
	if last := stages[len(stages)-1]; last.name != "core" {
		t.Errorf("the last stage is %q; core must stay the default target of `docker build .`", last.name)
	}
}

func TestMakefileBuildsAndScansBothImages(t *testing.T) {
	raw, err := os.ReadFile("../Makefile")
	if err != nil {
		t.Fatal(err)
	}
	src := string(raw)
	for _, want := range []string{"--target core", "--target replay", "$(REPLAY_IMAGE)", "REPLAY_IMAGE ?= hookway-replay:local"} {
		if !strings.Contains(src, want) {
			t.Errorf("Makefile lacks %s", want)
		}
	}
	scan := src[strings.Index(src, "\nvuln-scan:"):]
	scan = scan[:strings.Index(scan[1:], "\n\n")+1]
	if !strings.Contains(scan, "$(IMAGE)") || !strings.Contains(scan, "$(REPLAY_IMAGE)") {
		t.Errorf("vuln-scan must scan both images:\n%s", scan)
	}
}
