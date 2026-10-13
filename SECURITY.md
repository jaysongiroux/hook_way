# Security policy

## Reporting a vulnerability

Please use [GitHub private vulnerability reporting](https://github.com/jaysongiroux/hook_way/security/advisories/new)
when available. If that option is unavailable, contact a repository maintainer
privately to arrange a secure reporting channel. Do not put credentials, customer
payloads, exploit details, or private infrastructure addresses in public issues.
Include the affected release/image digest, a minimal reproduction, expected and
actual behavior, impact, and any suggested mitigation. Use synthetic payloads and
replace secrets. Coordinate disclosure with maintainers while a fix is prepared.

## Supported releases

Security fixes target the latest stable release. Upgrade to its newest patch;
older minor versions do not have a promised backport window. Development branches
and prereleases, including unreleased V2 work, are not production support promises.
No response-time SLA or security certification is implied by this policy.

## Release verification and dependency maintenance

Release images support **Linux amd64**. Verify the immutable image digest,
Sigstore workflow identity, source commit, SBOM, and provenance before deployment;
see [release verification](docs/operations/releases.md). A mutable `latest` tag is not a
trusted release identity. Report a missing or invalid signature as a release issue.

CI rejects reachable Go vulnerabilities, high/critical container vulnerabilities,
and high/critical npm dependency vulnerabilities. Scheduled checks detect newly
published advisories; Dependabot proposes Go, npm, action, and Docker pin updates.
Maintainers review these updates through normal CI. The policy checks and scanners
do not prove an application is free of vulnerabilities.

See [runtime security](docs/reference/security.md) for trust boundaries, egress controls,
secret handling, and replay access requirements.
