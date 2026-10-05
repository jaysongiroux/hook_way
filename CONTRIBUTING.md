# Contributing to Hookway

## Set up a checkout

Use the Go version in [`go.mod`](go.mod) and the Node/npm version used by
[CI](.github/workflows/reusable-ci.yml) for web changes. Docker is required for
integration tests and the local stack. Python 3 supports the contract generation,
acceptance and deployment scripts; OpenSSL signs the local webhook examples.

```bash
git clone --branch v2 https://github.com/jaysongiroux/hook_way.git
cd hook_way
make web-install  # needed for frontend changes under web/
```

Start the local example stack with `make dev-up`. It uses the development
configuration and defaults documented in [Local development](docs/development/local-development.md).

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
and review `git diff --check`. `make release-check` runs the broad Go, integration, image,
and security checks. Run `make web-e2e` separately for builder changes; CI runs
the browser suite in its own job.

## Pull requests

Describe the problem, the behavioral change, and the verification you ran.
Call out configuration or migration steps, compatibility effects, and checks
you could not run. Avoid unrelated formatting or generated-file churn.

Report vulnerabilities through [SECURITY.md](SECURITY.md).

Documentation ownership and placement rules are in the [documentation index](docs/README.md#maintaining-documentation).
