# Contributing to Hookway

## Set up a checkout

Use the Go version in [`go.mod`](go.mod) and the Node/npm version used by
[CI](.github/workflows/reusable-ci.yml) for web changes. Docker is required for
integration tests and the local stack. Python 3 supports the contract generation,
documentation and verification scripts. OpenSSL generates local credentials
and signs webhook examples.

```bash
git clone --branch v2 https://github.com/jaysongiroux/hook_way.git
cd hook_way
make web-install  # npm ci restores the checked-in lockfile for web changes
```

Start the local example stack with `make dev-up`. It uses the development
configuration and defaults documented in [Local development](docs/development/local-development.md).
For persistent data and generated credentials, use its
[production-style local setup](docs/development/local-development.md#persistent-local-production-setup).
The quick start's `make dev-down` deliberately removes its volumes.

## Make and check a change

Keep a change focused, update relevant documentation and examples, and validate
the narrowest useful checks before broader ones:

```bash
make test                 # Go unit tests
make validate-examples    # validate shipped YAML examples
make test-examples        # boot and exercise shipped examples
make lint                 # go vet
make web-test             # config-builder unit tests
make web-build            # type-check and build the config builder
```

Use `make console-e2e` for the replay console and `make replay-acceptance-test`
for delivery, shared protection, and replay against disposable backends. Run
`make compose-e2e-test COMPOSE_E2E_BUILD=dockerfile` to exercise the packaged stack.
These checks need Docker; inspect their output for skipped tests.

Other backends have individual `make *-integration-test` targets. `make test-all`
runs the full Go suite, including Docker-backed tests. See the
[extension guide](docs/development/extending-hookway.md) for module layout and
generated contracts, and the [Makefile](Makefile) for the full target list.

For a broad check, run `make hardening-check`. It installs locked web dependencies
and runs Go, frontend, browser, backend and security checks; Docker is required.
It does not build application images. `make release-check` adds packaged Compose,
image smoke tests and scans. Neither command publishes or signs images.

Install Chromium before browser tests: `cd web && npx playwright install chromium`.
On a fresh Linux machine, add `--with-deps` to install its system libraries.
Native builds need only Go. Releases are started manually from `main`; see
[Releases](docs/operations/releases.md).

## Pull requests

Describe the problem, the behavioral change, and the verification you ran.
Call out configuration or migration steps, compatibility effects, and checks
you could not run. Avoid unrelated formatting or generated-file churn.

Report vulnerabilities through [SECURITY.md](SECURITY.md).

## Documentation

Write for the person using the feature: explain the task, show a runnable example,
and describe errors they may encounter. Keep field definitions in `docs/reference`,
walkthroughs in `docs/guides`, operating procedures in `docs/operations`, and
contributor guidance in `docs/development`. Link to existing guidance rather than
repeating it. Do not put implementation plans or test handoffs in user docs.

Update incoming links and the [documentation index](docs/README.md) when moving
a page. Run `make docs-check` and `git diff --check`. Keep the date, workload and
environment with published benchmark results. Product and marketing copy should
describe shipped behavior, not planned features or unsupported guarantees.
