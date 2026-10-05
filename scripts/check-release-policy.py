#!/usr/bin/env python3
"""Fail CI if release supply-chain guardrails are accidentally removed."""
import pathlib
import re

root = pathlib.Path(__file__).resolve().parent.parent
errors = []
for path in (root / '.github/workflows').glob('*.yml'):
    for number, line in enumerate(path.read_text().splitlines(), 1):
        match = re.search(r'\buses:\s+(\S+)', line)
        if match and not match[1].startswith('./') and not re.fullmatch(r'[\w./-]+@[a-f0-9]{40}', match[1]):
            errors.append(f'{path.name}:{number}: action must use a full commit SHA')
for name in ('Dockerfile', 'web/Dockerfile'):
    stages = {'scratch'}
    for line in (root / name).read_text().splitlines():
        if not line.upper().startswith('FROM '):
            continue
        fields = line.split()
        image = next(field for field in fields[1:] if not field.startswith('--'))
        if image not in stages and not re.search(r'@sha256:[a-f0-9]{64}\b', image):
            errors.append(f'{name}: external base image must use an immutable digest')
        if len(fields) >= 4 and fields[-2].upper() == 'AS':
            stages.add(fields[-1])
release = (root / '.github/workflows/release.yml').read_text()
for setting in ('platforms: linux/amd64', 'sbom: true', 'provenance: mode=max'):
    if release.count(setting) != 3:
        errors.append(f'release.yml: all three images must declare {setting}')
for required in ('scripts/verify-release.sh', 'scripts/release-smoke.py', 'id-token: write', 'needs: [context, checks]', 'file: web/Dockerfile', 'id: console', 'steps.console.outputs.digest', '--console "$console"', 'for image in "$core" "$replay" "$console"', '"$image-console@$CONSOLE_DIGEST"'):
    if required not in release:
        errors.append(f'release.yml: missing {required}')
if errors:
    raise SystemExit('\n'.join(errors))
print('Release actions, image pin, supported platform, attestations and artifact gates verified.')
