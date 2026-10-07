# Release artifacts and verification

Use this guide to publish or verify a matching set of core, replay, and frontend
images. For service setup use the [Compose examples](../../examples/README.md);
for migrations and rollback use [Upgrades](upgrades.md). Use
[production acceptance](production-acceptance.md) to separate local checks from
published-artifact and deployment sign-off.

## Supported artifacts

The release workflow publishes core, matching `<image>-replay`, and separate
`<image>-console` images for **linux/amd64 only**. The full reusable CI suite runs on native Linux
X64 runners, including Go/race tests, backend integration tests, and built-image
smoke tests. The release then pulls each published immutable digest, checks its
architecture and embedded version/source commit, and exercises core readiness,
requests, clean shutdown, and restart. ARM64 and standalone archive downloads are
not published or covered by this release contract.

The optional replay image is version-checked by the release smoke harness. Actual
replay migrations, backup/restore, and delivery reconciliation are separate
[upgrade acceptance gates](upgrades.md). A passing image smoke test does not prove
compatibility with a previous release's database.

## CI and publication setup

[CI](../../.github/workflows/ci.yml) calls the
[reusable gate](../../.github/workflows/reusable-ci.yml) for pull requests and
pushes to `main`. Require `Checks / All checks passed` on `main`. The workflow
files own the current jobs, integration matrix, versions and scan thresholds;
avoid maintaining a second job inventory in prose.

[Release](../../.github/workflows/release.yml) is manually dispatched from `main`
and reruns that gate for its exact source commit,
then publishes matching core/replay/console images, generated release notes and
the Docker Hub description from `docs/dockerhub/README.md`. A push to `main`
does not publish, and neither does a tag push. Merge reviewed changes from `v2`
first; building/signing a release is not a prerequisite for branch hardening.
When ready to release, create a tag at the reviewed `main` commit and dispatch:

```sh
git fetch origin
git tag v1.2.3 origin/main
git push origin v1.2.3
gh workflow run release.yml --ref main -f tag=v1.2.3
```

Prerelease tags create a GitHub prerelease and do not move Docker `latest`.
Configure `DOCKERHUB_USERNAME`, `DOCKERHUB_NAMESPACE`, `DOCKERHUB_IMAGE_NAME` and
`DOCKERHUB_TOKEN` for Actions. Username identifies the token's account; namespace
identifies the image owner. Description sync requires read/write/delete token
scope and repository admin access. Use the protected `release` environment for
release authorization. The [security workflow](../../.github/workflows/security.yml)
runs scheduled/on-demand advisory checks; dependency updates go through normal CI.

## Build, sign, and retain evidence

`.github/workflows/release.yml` runs only by manual dispatch on `main`:
`gh workflow run release.yml --ref main -f tag=v1.2.3`.
It checks the event's full source commit and uses that exact Git commit as the
remote BuildKit context; local checkout changes cannot enter the published image.
The existing tag must peel to the exact dispatched `main` commit; a stale tag,
unmerged source or invocation from another branch/tag is rejected. Keep `main`
unchanged between choosing the tag and dispatching; no tag is created or moved
by the workflow. The tag grammar is checked before publication; arbitrary
version suffixes do not qualify as stable releases. This ancestry check does not
prove human review or substitute for enforced branch/tag protection.
The protected `release` environment should restrict deployment to `main` and
authorized reviewers. Provision its Docker Hub credentials and GitHub OIDC
permission on trusted, isolated runners. The existing self-hosted runner and
dependency/browser cache setup is retained; browser OS libraries are provisioned
separately and jobs do not request sudo. Runner labels alone do not isolate
privileged release work from untrusted PRs. Repository access, branch/tag rules,
runner isolation and environment review restrictions remain operator requirements
for publication, not prerequisites to harden `v2`. See GitHub's
[workflow security guidance](https://docs.github.com/en/actions/reference/security/secure-use).

All external workflow actions use full commit SHAs. The Go/Alpine base uses an
OCI index digest, and the trust store comes from that pinned image. Dependabot
proposes updates to those pins. The GitHub runner, Docker daemon, Buildx binary,
BuildKit builder image, and SBOM scanner remain build infrastructure dependencies;
the action pin does not make these transitive tools immutable. Their versions
and digests are recorded in build metadata where supported, and runner isolation
remains part of the release trust boundary. Docker Buildx publishes SPDX SBOM and maximum
provenance attestations as children of each image index. Build arguments are
public in provenance: never pass secrets as build arguments. Cosign keyless
signatures bind the image index digest, the release tag, and the full source SHA
to GitHub's OIDC workflow identity. This also binds the referenced SBOM/provenance
manifests. See Docker's [attestation documentation](https://docs.docker.com/build/ci/github-actions/attestations/)
and Sigstore's [verification documentation](https://docs.sigstore.dev/cosign/verifying/verify/).

The workflow scans all three published digests for high/critical vulnerabilities, verifies its signatures and fetches attestations, then records
`release.json`, signature verification output, SBOM/provenance JSON, and install
smoke results as workflow artifacts and GitHub Release attachments. Retain these
with deployment acceptance evidence and the configuration checksum. Docker Hub
publication necessarily precedes registry signature and pull tests: if either
fails, the workflow fails and operators must not deploy the new version tag.
The mutable `latest` tags are promoted only after these gates succeed. Deploy only
a verified immutable digest from a successfully completed release run.

## Verify before installing

Install Docker with Buildx, Python 3, and Cosign (the workflow pins Cosign v3.1.3).
Obtain the tag, full source commit, and all three immutable digests from the successful
release run. Do not trust a downloaded evidence file as a substitute for signature
verification. Set the following to that release's real values:

```sh
CORE='docker.io/your-namespace/hookway@sha256:...'
REPLAY='docker.io/your-namespace/hookway-replay@sha256:...'
CONSOLE='docker.io/your-namespace/hookway-console@sha256:...'
TAG='v1.2.3'
COMMIT='full-40-character-source-commit'
scripts/verify-release.sh "$CORE" "$REPLAY" "$CONSOLE" "$TAG" "$COMMIT" \
  jaysongiroux/hook_way ./release-evidence
python3 scripts/release-smoke.py --core "$CORE" --replay "$REPLAY" --console "$CONSOLE" \
  --version "$TAG" --commit "$COMMIT" --output ./release-evidence/install.json
```

Verification requires the exact certificate identity
`https://github.com/jaysongiroux/hook_way/.github/workflows/release.yml@refs/heads/main`,
issuer `https://token.actions.githubusercontent.com`, and the signed tag/source
annotations. Broad certificate identity regular expressions are not equivalent.
The script rejects mutable tag inputs, missing attestations, and provenance that
does not identify the expected repository and commit in the SLSA source record.
A matching build argument alone is insufficient. The smoke harness pulls and executes
the supplied images in isolated containers; run it only after signature checks.

## Rehearse rollback

First verify the candidate and previous image sets with their own tag and
source commit. Pass `--previous-core`, `--previous-replay`, `--previous-console`, `--previous-version`,
and `--previous-commit` to `release-smoke.py`. It installs/restarts the candidate,
then installs the previous core and checks the previous replay binary identity,
recording both immutable sets. This tests binary installation rollback against a
small canary config. It deliberately does not access production configurations or
a replay database. Follow [upgrades.md](upgrades.md) for quiescence, a verified
backup, migration, canary delivery, and restore/reconciliation before approving
production rollback.

The first release using these controls must demonstrate OIDC signing, registry
attestation retention, artifact installation, and the real deployment's migration
and rollback gates. Local checks cannot substitute for that release evidence.

## Console and integrated candidate checks

Native core/replay builds require only Go. The separate `web/Dockerfile` compiles
and packages WASM and the frontend automatically from the exact source commit.
All three images use the same VERSION/COMMIT build arguments. The console smoke
test starts with a public `REPLAY_API_URL`, validates `/build-info.json` version
and commit, and fetches both entrypoints and WASM assets. No API key enters the
frontend build, image or runtime environment.

The native acceptance suite builds and fingerprints `web/dist-console`
independently of the Go binaries, including WASM and its matching JS runtime.
Retain the report, checksummed logs, standalone browser results and deployment
test results. Local source/binary/asset hashes are distinct from published
immutable digests/signatures. A dirty checkout report cannot certify a later
commit or an older release. Promote all three stable images to `latest` only
after every scan, signature, provenance and smoke gate passes.
