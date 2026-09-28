# Contributing to Hookway

## Set up a checkout

Install the Go version declared in `go.mod`. Docker is required for integration
tests and the local stack. Install Node.js 20 when changing the config builder.

```bash
git clone https://github.com/jaysongiroux/hook_way.git
cd hook_way
make web-install  # only needed when changing the builder under web/
```

Start the local example stack with `make dev-up`. It uses the development
configuration and defaults documented in the [README](README.md#quick-start).

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

Docker-backed suites are available as individual `make *-integration-test`
targets. `make test-all` runs the full Go suite and needs Docker for its
container-backed tests. Run `make node-schema-check` after changing config
schema behavior; use `make node-schema` to regenerate the committed catalogs.

Before opening a pull request, run the checks that cover your change and review
`git diff --check`. `make release-check` runs the broad Go, integration, image,
and security checks. Run `make web-e2e` separately for builder changes; CI runs
the browser suite in its own job.

## Pull requests

Describe the problem, the behavioral change, and the verification you ran.
Call out configuration or migration steps, compatibility effects, and checks
you could not run. Avoid unrelated formatting or generated-file churn.

Report security issues through the repository's security contact once one is
published; do not include credentials, tokens, or production webhook payloads
in an issue or pull request.
