# Contributing to Hookway

## Set up a checkout

Use the Go version in [`go.mod`](go.mod) and the Node/npm version used by
[CI](.github/workflows/reusable-ci.yml) for web changes. Docker is required for
integration tests and the local stack. Python 3 supports the contract generation,
acceptance and deployment scripts; OpenSSL generates local production credentials
and signs webhook examples. The persistent production recipe itself uses Make,
ordinary YAML and Docker Compose, with no Python helper requirement.

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

Other Docker-backed suites have individual `make *-integration-test` targets. `make test-all` runs the full Go suite and needs Docker for its
container-backed tests. Use the [extension guide](docs/development/extending-hookway.md) for code ownership,
generated contracts, and the separation between native and WASM code. `Makefile` owns the
complete target list; avoid maintaining copies of it in multiple guides.

For documentation or marketing changes, run `make docs-check` to check file and
heading links. Before opening a pull request, run the checks that cover your change
and review `git diff --check`. `make hardening-check` restores locked web dependencies
and runs native, frontend/browser, backend integration and source security checks.
It does not build, publish or sign application images. Docker is needed for backend
and monitoring fixtures; unavailable integrations fail this broad gate.
`make release-check` adds packaged Compose, image smoke and image scans when those
checks are wanted; it does not publish or sign either. Existing CI retains its
unpublished image checks. Install the Playwright Chromium
browser and required OS libraries first (`cd web && npx playwright install chromium`;
on a fresh supported Linux host, use `--with-deps` when authorized to install OS packages).
Focused checks keep their existing convenient defaults; native runtime builds
still need no Node or cloud account. A passing local gate is not exact-commit CI,
signature verification or a live deployment. Harden and review `v2` before merging;
the release is kicked off manually from `main` later. See
[production acceptance](docs/operations/production-acceptance.md) for that later stage.

## Pull requests

Describe the problem, the behavioral change, and the verification you ran.
Call out configuration or migration steps, compatibility effects, and checks
you could not run. Avoid unrelated formatting or generated-file churn.

Report vulnerabilities through [SECURITY.md](SECURITY.md).

Documentation ownership and placement rules are in the [documentation index](docs/README.md#maintaining-documentation).
