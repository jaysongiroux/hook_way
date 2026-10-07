# Production acceptance

## Branch hardening versus release acceptance

Hardening `v2` prepares source, configuration, tests and operator documentation
for review and merge into `main`. It does not require a published candidate,
application image builds, signatures or an ECS environment. Use
`make hardening-check` for broad source/native/browser/integration validation;
Docker is still used for backend and monitoring test fixtures. Retain results,
known limitations and the exact tested source, then review the branch and its CI.
The existing CI image checks are unpublished packaging checks, not a release.

After merge, the owner separately kicks off the release workflow manually from
`main`. Only that later process builds/publishes/signs the release set. The
release and deployment checklist below applies at that point, not as a blocker
for completing branch hardening. A missing release or cloud environment is not
a failed `v2` hardening gate. Source-level regressions remain blockers.

Production readiness belongs to a specific release, configuration and deployment,
not a passing unit suite or a historical benchmark. Use this checklist with the
[release verification](releases.md), [upgrade/rollback](upgrades.md),
[recovery](recovery.md) and [incident](observability.md) procedures. A local dirty
checkout cannot certify a later commit, published image or ECS environment.

## Freeze the release candidate after merge

Record the full reviewed source SHA, exact release tag, clean source state,
successful CI/release run URLs, three immutable image digests, Linux amd64 platform,
core/replay schema and console/WASM contract, configuration checksums and backend
versions. Keep private environment files and credentials out of the evidence.
Source/configuration changes require proportionate revalidation.

For a local rehearsal, record the dirty-source manifest and local image IDs instead;
label the version as a local build. ARM64 workstation or emulated amd64 evidence
does not replace native Linux X64 CI. Source builds and unsigned local containers
remain supported and need no publication credentials. Published-release trust
requirements are not a new restriction on local development.

Agree the [operational worksheet](observability.md#operational-targets-and-ownership):
traffic mix/rates, payload sizes/fan-out, sender response and delivery expectations,
resource allocation, RPO/RTO and incident owners. If targets are unset, report
measurements and leave deployment sign-off open; do not turn defaults, small
fixtures or provider response deadlines into product SLAs.

## Later release and deployment evidence gates

| Gate | Required evidence / rejection condition |
| --- | --- |
| Repository/review | Exact source checked, required checks enforced, authorized tag/release reviewers and credentials, isolated release execution. A YAML `environment` declaration does not establish actual protection; an inaccessible settings API is unknown, not a pass. |
| Local validation | `make release-check`, no unexpected skips, generated contracts/lockfiles unchanged, maintained native/browser/integration and packaged image checks. Failed tests or scanners remain failed until fixed and rerun. |
| Published set | Verify all three immutable digests, exact Sigstore workflow/tag/source identity and SBOM/provenance before executing images. Require a fully successful release run; never treat a partially published tag or `latest` as accepted. |
| Fresh installation | Install that verified set with generated credentials and restricted runtime roles; migrate once with owner credentials, verify readiness, signed canary, destination receipt, capture/retry and restart preservation. Source-build testing is a different evidence layer. |
| Upgrade/rollback | Retain and verify the exact compatible predecessor plus config/schema/backup. Initial-install or same-build replacement is not cross-release compatibility proof. For an initial fresh-install baseline with no predecessor, record that bounded scope and retain it for the next release. |
| Target deployment | Actual TLS/network/IAM/secret/runtime/ALB behavior, task replacement and managed backend faults with reconciled destination receipts. Compose does not prove ECS semantics. |
| Recovery | Authorized isolated off-host restore, backup decryption/key recovery, logical/artifact/grant checks and post-snapshot receipt reconciliation. Keep unreconciled restored queues offline. |
| Workload/operations | Declared sustained/burst/file/replay traffic, independent receipts, CPU/memory/admission/DB samples and agreed thresholds; actual responder firing/resolved receipt and ownership/handovers. Synthetic notification inputs do not prove real storage exhaustion. |

## Optional packaged rehearsal

Install the documented Go/Node/Python/Docker tooling and Chromium dependencies.
When packaged-image validation is wanted, the optional broad command is:

```sh
make release-check
```

It restores locked frontend dependencies, requires integration backends and uses
Dockerfile-backed Compose acceptance. This builds unsigned local application
images; it does not publish/sign and is not required to close the branch-hardening
plan. For branch hardening use `make hardening-check`; for everyday development use focused
[contribution targets](../../CONTRIBUTING.md#make-and-check-a-change); `make build`
remains Go-only. Optional monitoring, intentional `no_auth`, private destinations,
loopback HTTP and proxy-terminated TLS retain their documented contracts.

Use a separate disposable project for the
[persistent production-style recipe](../development/local-development.md#persistent-local-production-setup).
Record architecture/resource/stop limits and source/config/image identity. Test
the same acknowledgement and protection modes intended for deployment; choosing
an easier fixture mode does not qualify another configuration. Preserve existing
operator edits and secrets, and never use an unrelated project's volume reset.

Published installation uses the verified digests and the commands in
[release verification](releases.md#verify-before-installing), not a local tag
substituted into a signature-verification result. Rehearse initial migrations,
runtime grants, signed failure/retry and persistence using that exact set.

## Authorized ECS rehearsal

Select the exact AWS account/region/cluster/service/task definitions and isolated
canary destinations with the deployment owner before making changes. A working
CLI or credentials do not grant permission to deploy or fault unrelated services.
Record, without secrets:

- Matching immutable image/task-definition/configuration identities and explicit
  Linux X86_64 task architecture; public console URL and exact replay CORS origin.
- Separate task execution and application permissions, runtime versus migration
  credentials, reviewed runtime grants, mounted file permissions and secret
  rotation across all replicas. The console receives only public API configuration.
- Private control/metrics/database listeners, TLS/certificate validation, ingress
  and destination egress policy, non-root/read-only constraints, bounded writable
  paths, resource budgets and deployment-owned disk/log/backup encryption.
- Liveness versus readiness, ALB target removal and no unintended upstream retry.
  Set task stop budgets above application drains with measured margin. Fargate's
  default is 30 seconds and maximum 120; incompatible application budgets require
  a deliberate sizing/platform decision, not silent deadline clamping. See the
  [AWS stop timeout contract](https://docs.aws.amazon.com/AmazonECS/latest/APIReference/API_ContainerDefinition.html).

Follow [ECS migration ordering](upgrades.md#ecs-deployment-ordering-and-stop-budgets):
pause traffic/operator actions, settle work, stop core while replay capture remains
reachable, then stop replay, preserve state, run migration-only tasks and check
placement plus the intended container's numeric exit code. A task reaching STOPPED
does not prove success. Keep ingress paused after any failed migration/canary.

Use each in-scope provider's authorized test delivery and independently verified
destination/consumer receipts; check malformed signatures, secret rotation,
duplicates, timeouts and recovery through the actual public endpoint. The
[provider recipes](../guides/provider-compatibility.md) are local protocol evidence,
not certification of a provider account or external business effect.

Measure task withdrawal/replacement, dependency connection loss versus state loss,
retention pressure, isolated physical disk failure and recovery. Never fill a
production/shared volume for a drill. Record captured/drop/unknown outcomes and
reconcile post-snapshot effects before any retry/rollback. Backup scheduling,
retention, decryption keys, managed failover and off-host loss need explicit owners.

## Decision and handover

Retain sanitized manifests, command/log/checksum reports, scanner versions/results,
source/run links, image signatures/attestations, receipts, resource/latency samples,
restore/migration outcomes and actual notification receiver statuses. Keep secret
files, raw customer bodies and production backups in approved private storage,
not Git or public CI attachments.

Use **go** only when every applicable gate has evidence and the release owner
accepts its declared scope. Otherwise state **no-go** and list exact missing or
failed gates with owner/action. A local pass is not permission for publication or
rollout. Obtain separate authorization for tags/publication, settings/credential
changes, cloud rehearsals and real responder notifications. Keep a rollback
package and maintenance decision available; do not deploy automatically.
