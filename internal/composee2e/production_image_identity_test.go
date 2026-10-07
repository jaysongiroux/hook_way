package composee2e

import (
	"encoding/hex"
	"fmt"
	"regexp"
	"slices"
	"testing"
)

// These opt-in fixture inputs exercise local unsigned release-shaped images;
// an embedded commit is metadata, not provenance for a dirty source tree.
type productionImageBuild struct {
	platform string
	version  string
	commit   string
}

func productionImageInputs(platform, version, commit string) (productionImageBuild, error) {
	if platform != "" && platform != "linux/amd64" && platform != "linux/arm64" {
		return productionImageBuild{}, fmt.Errorf("unsupported production fixture platform %q", platform)
	}
	if version == "" {
		version = "dev"
	}
	if !regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._+-]*$`).MatchString(version) {
		return productionImageBuild{}, fmt.Errorf("invalid fixture version %q", version)
	}
	if commit == "" {
		commit = "none"
	} else if decoded, err := hex.DecodeString(commit); err != nil || len(decoded) != 20 {
		return productionImageBuild{}, fmt.Errorf("fixture commit must be a full 40-character hexadecimal source commit")
	}
	return productionImageBuild{platform: platform, version: version, commit: commit}, nil
}

func (input productionImageBuild) arguments(file, target, tag string) []string {
	args := []string{"build", "--file", file, "--tag", tag, "--build-arg", "VERSION=" + input.version, "--build-arg", "COMMIT=" + input.commit}
	if target != "" {
		args = append(args, "--target", target)
	}
	if input.platform != "" {
		args = append(args, "--platform", input.platform)
	}
	return append(args, ".")
}

func productionIdentityMismatch(want productionImageBuild, platform, version, commit string) error {
	if platform != want.platform || version != want.version || commit != want.commit {
		return fmt.Errorf("artifact identity mismatch: got platform=%s version=%s commit=%s; want platform=%s version=%s commit=%s", platform, version, commit, want.platform, want.version, want.commit)
	}
	return nil
}

func TestProductionImageInputsAndIdentity(t *testing.T) {
	const commit = "39e506734b4354561624b21869bb254027f94cf6"
	defaults, err := productionImageInputs("", "", "")
	if err != nil || defaults != (productionImageBuild{version: "dev", commit: "none"}) {
		t.Fatalf("default image inputs changed: %+v %v", defaults, err)
	}
	for _, input := range []productionImageBuild{
		{platform: "windows/amd64"}, {platform: "linux/s390x"},
		{version: "version with injected flags"}, {commit: "39e506"},
		{commit: "xxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx"},
	} {
		if _, err := productionImageInputs(input.platform, input.version, input.commit); err == nil {
			t.Fatalf("invalid image input accepted: %+v", input)
		}
	}
	want, err := productionImageInputs("linux/amd64", "phase7-local-dirty-39e506734b43", commit)
	if err != nil {
		t.Fatal(err)
	}
	if args := want.arguments("Dockerfile", "replay", "unique-replay:fixture"); !slices.Equal(args, []string{"build", "--file", "Dockerfile", "--tag", "unique-replay:fixture", "--build-arg", "VERSION=" + want.version, "--build-arg", "COMMIT=" + want.commit, "--target", "replay", "--platform", "linux/amd64", "."}) {
		t.Fatalf("release-shaped build arguments changed: %v", args)
	}
	if err := productionIdentityMismatch(want, want.platform, want.version, want.commit); err != nil {
		t.Fatal(err)
	}
	for _, actual := range []productionImageBuild{
		{platform: "linux/arm64", version: want.version, commit: want.commit},
		{platform: want.platform, version: "dev", commit: want.commit},
		{platform: want.platform, version: want.version, commit: "none"},
	} {
		if productionIdentityMismatch(want, actual.platform, actual.version, actual.commit) == nil {
			t.Fatalf("mismatched image identity accepted: %+v", actual)
		}
	}
}
