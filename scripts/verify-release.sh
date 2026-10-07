#!/usr/bin/env bash
# Verify immutable release digests before running any service.
set -euo pipefail
if [ "$#" -ne 7 ]; then
  echo "usage: $0 CORE@sha256:... REPLAY@sha256:... CONSOLE@sha256:... TAG FULL_COMMIT OWNER/REPO OUTPUT_DIR" >&2
  exit 2
fi
core=$1 replay=$2 console=$3 tag=$4 commit=$5 repository=$6 output=$7
[[ ${#tag} -le 128 && "$tag" =~ ^v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(-[0-9A-Za-z-]+(\.[0-9A-Za-z-]+)*)?$ ]] || { echo "invalid release tag (build metadata is not supported)" >&2; exit 2; }
if [[ "$tag" == *-* ]]; then
  IFS='.' read -r -a identifiers <<< "${tag#*-}"
  for identifier in "${identifiers[@]}"; do
    [[ ! "$identifier" =~ ^0[0-9]+$ ]] || { echo "numeric prerelease identifiers must not have leading zeroes" >&2; exit 2; }
  done
fi
[[ "$commit" =~ ^[a-f0-9]{40}$ ]] || { echo "expected full source commit" >&2; exit 2; }
[[ "$repository" =~ ^[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+$ ]] || { echo "invalid repository" >&2; exit 2; }
# Validate the entire set before creating evidence or invoking network tools.
# In particular, an invalid third image must not partially verify the first two.
for image in "$core" "$replay" "$console"; do
  [[ "$image" =~ ^[a-z0-9][a-z0-9._:/-]*@sha256:[a-f0-9]{64}$ ]] || { echo "expected immutable image digest, not a tag or option" >&2; exit 2; }
done
mkdir -p -- "$output"
identity="https://github.com/$repository/.github/workflows/release.yml@refs/heads/main"
for service in core replay console; do
  case "$service" in core) image=$core;; replay) image=$replay;; console) image=$console;; esac
  cosign verify --certificate-identity "$identity" \
    --certificate-oidc-issuer https://token.actions.githubusercontent.com \
    -a "source-sha=$commit" -a "release-tag=$tag" "$image" > "$output/$service-signature.json"
  # These attestations are children of the signed OCI index, not separately
  # cosign-signed payloads. Never substitute an unsigned tag when fetching them.
  docker buildx imagetools inspect "$image" --format '{{json .Provenance}}' > "$output/$service-provenance.json"
  docker buildx imagetools inspect "$image" --format '{{json .SBOM}}' > "$output/$service-sbom.json"
  python3 "$(dirname "$0")/verify-provenance.py" "$output/$service-provenance.json" "$output/$service-sbom.json" "$repository" "$commit"
done
python3 - "$core" "$replay" "$console" "$tag" "$commit" "$identity" > "$output/release.json" <<'PY'
import json,sys
print(json.dumps(dict(zip(('core','replay','console','tag','commit','certificate_identity'),sys.argv[1:])),indent=2))
PY
