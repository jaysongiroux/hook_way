# Releases

Releases are started manually from `main`. Merging changes or pushing a tag does
not publish images. The workflow publishes a matching core, replay and console
set for **Linux amd64**; native source builds remain available for other platforms.

For installation settings, see the [Compose examples](../../examples/README.md).
For database changes and rollback, see [Upgrades](upgrades.md).

## CI and publication setup

[CI](../../.github/workflows/ci.yml) runs the
[shared checks](../../.github/workflows/reusable-ci.yml) for pull requests and
pushes to `main`. Require `Checks / All checks passed` before merging.

For publication, configure these Actions secrets:

- `DOCKERHUB_USERNAME`: the token's account.
- `DOCKERHUB_NAMESPACE`: the image owner.
- `DOCKERHUB_IMAGE_NAME`: the base image name.
- `DOCKERHUB_TOKEN`: publishing credentials; description sync needs
  read/write/delete scope and repository admin access.

Restrict the `release` environment to `main` and authorized reviewers. Release
jobs use GitHub-hosted `ubuntu-latest` runners. Keep privileged publication
isolated from untrusted pull requests. See [GitHub workflow security](https://docs.github.com/en/actions/reference/security/secure-use).

## Publish a release

After reviewing and merging the changes, tag the current `main` commit and
start the workflow:

```sh
git fetch origin
git tag v1.2.3 origin/main
git push origin v1.2.3
gh workflow run release.yml --ref main -f tag=v1.2.3
```

Keep `main` unchanged between tagging and dispatching. The tag must identify the
exact dispatched commit; a stale tag or a run from another branch/tag is rejected.
The workflow does not create or move tags. Prereleases such as `v1.2.3-rc.1`
create a GitHub prerelease and do not update Docker `latest`.

The [release workflow](../../.github/workflows/release.yml):

1. Reruns CI against the selected full source SHA.
2. Builds and publishes core, `<image>-replay` and `<image>-console` from that SHA.
3. Scans all three digests, signs them with Cosign, verifies their source and
   installation, and attaches the results to the GitHub Release.
4. Updates stable `latest` tags only after those checks pass.

A failed run can leave a version tag in the registry because signing and pull
checks happen after publication. Do not deploy that tag. Use verified immutable
digests from a fully successful run.

## Build, sign, and retain evidence

All images carry the same version and full commit. Buildx attaches SPDX SBOM and
provenance manifests; Cosign signs the image index and source/tag annotations.
Build arguments are public in provenance, so never pass secrets through them.
Pinned actions and base images reduce drift, but do not pin every runner/build
tool. See [Docker attestations](https://docs.docker.com/build/ci/github-actions/attestations/)
and [Cosign verification](https://docs.sigstore.dev/cosign/verifying/verify/).

Keep the release's digests, `release.json`, signature results, SBOM/provenance
and installation report with your deployed configuration.

## Verify before installing

Install Docker with Buildx, Python 3 and Cosign (the workflow uses v3.1.3).
Get the tag, full commit and three digests from the successful release run:

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
issuer `https://token.actions.githubusercontent.com`, source/tag annotations and
matching SLSA source record. Mutable tags or a matching build argument are not
substitutes. Run the smoke test only after signature verification: it executes
the supplied images.

The console image serves the frontend and matching WASM assets, with build
identity at `/build-info.json`. It receives a public `REPLAY_API_URL`, never
an operator token. See [console setup](replay-console.md#build-and-deploy).

## Rehearse rollback

Verify both the new and previous image sets. The smoke script accepts
`--previous-core`, `--previous-replay`, `--previous-console`,
`--previous-version` and `--previous-commit` for a small installation rollback
check. It does not migrate or restore a replay database.

Test your real configuration and database transition separately using
[Upgrades](upgrades.md) and [Backup and restore](recovery.md). Keep restored workers
offline until post-backup deliveries are reconciled.
