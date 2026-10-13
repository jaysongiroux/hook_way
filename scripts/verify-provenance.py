#!/usr/bin/env python3
"""Check BuildKit's source record, never a substring in arbitrary build arguments."""
import json
import pathlib
import sys


def verify(provenance, sbom, repository, commit):
    # Buildx returns SLSA/SPDX directly for a single platform, or a platform map.
    provenance = provenance.get('linux/amd64', provenance)
    sbom = sbom.get('linux/amd64', sbom)
    statement = provenance.get('SLSA', {})
    source = statement.get('buildDefinition', {}).get('externalParameters', {}).get('configSource')
    if source is None:
        source = statement.get('invocation', {}).get('configSource', {})
    uri = source.get('uri', '').removeprefix('git+')
    expected = 'https://github.com/' + repository + '.git'
    if uri.split('#', 1)[0] != expected or source.get('digest', {}).get('sha1') != commit:
        raise ValueError('SLSA configSource must identify the expected Git repository and commit')
    if not sbom.get('SPDX', {}).get('spdxVersion', '').startswith('SPDX-'):
        raise ValueError('missing SPDX SBOM')


if __name__ == '__main__':
    if len(sys.argv) != 5:
        raise SystemExit('usage: verify-provenance.py PROVENANCE_JSON SBOM_JSON OWNER/REPO COMMIT')
    try:
        verify(json.loads(pathlib.Path(sys.argv[1]).read_text()), json.loads(pathlib.Path(sys.argv[2]).read_text()), sys.argv[3], sys.argv[4])
    except (ValueError, AttributeError) as error:
        raise SystemExit(str(error))
