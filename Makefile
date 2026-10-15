.PHONY: deadcode web-knip test test-all test-race test-examples conformance integration-test aws-integration-test redis-integration-test postgres-integration-test mongodb-integration-test kafka-integration-test webhook-integration-test temporal-integration-test replay-integration-test replay-acceptance-test compose-e2e-test bench-replay lint validate validate-examples build wasm wasm-harness web-install web-dev web-test web-e2e web-build ui ui-check node-schema node-schema-check api-schema api-schema-check console-build console-ui console-e2e run docker-build vuln-scan release-check dev-up dev-down unit race fmt-check diff-check coverage observability-check golangci-lint govulncheck

GO ?= go
.PHONY: docs-check
CONFIG ?= examples/config.yaml
IMAGE ?= hookway:local
REPLAY_IMAGE ?= hookway-replay:local
CONSOLE_IMAGE ?= hookway-console:local

# VERSION/COMMIT are baked into the binary via -ldflags so `hookway version`
# and the hookway_build_info metric identify the build they came from. Both
# fall back to sentinel values ("dev"/"none", matching cmd/hookway/main.go's
# package-level defaults) outside a git checkout (e.g. a source tarball) or a
# shallow clone with no reachable commit.
VERSION := $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
COMMIT := $(shell git rev-parse --short HEAD 2>/dev/null || echo none)
LDFLAGS := -X main.version=$(VERSION) -X main.commit=$(COMMIT)

build:
	$(GO) build -ldflags "$(LDFLAGS)" -o bin/hookway ./cmd/hookway
	$(GO) build -ldflags "$(LDFLAGS)" -o bin/hookway-replay ./cmd/hookway-replay

docs-check:
	python3 scripts/check-doc-links.py

wasm:
	mkdir -p internal/editor/assets web/public
	GOOS=js GOARCH=wasm $(GO) build -o internal/editor/assets/hookway.wasm ./cmd/hookway-wasm
	# Downloaded Go toolchains ship a read-only shim; replace generated copies on rebuild.
	cp -f "$$($(GO) env GOROOT)/lib/wasm/wasm_exec.js" internal/editor/assets/wasm_exec.js
	cp -f internal/editor/assets/hookway.wasm internal/editor/assets/wasm_exec.js web/public/

wasm-harness: wasm
	cp -f internal/editor/assets/hookway.wasm internal/editor/assets/wasm_exec.js cmd/hookway-wasm/harness/
	@command -v python3 >/dev/null 2>&1 || { \
		echo "wasm-harness needs python3 to serve the harness over http://."; \
		echo "Opening index.html as a file:// URL will not work: the WASM fetch is blocked by CORS."; \
		echo "Install python3, or serve cmd/hookway-wasm/harness with any static file server on port 8091."; \
		exit 1; }
	@echo "Serving harness on http://localhost:8091 (Ctrl-C to stop)"
	cd cmd/hookway-wasm/harness && python3 -m http.server 8091

# Check targets generate into temporary directories and never modify sources.
node-schema:
	GO=$(GO) python3 scripts/generate-ui-contracts.py nodes

node-schema-check:
	GO=$(GO) python3 scripts/generate-ui-contracts.py nodes --check

api-schema:
	GO=$(GO) python3 scripts/generate-ui-contracts.py api

api-schema-check:
	GO=$(GO) python3 scripts/generate-ui-contracts.py api --check

web-install:
	cd web && npm ci

.PHONY: module-check web-audit release-policy-check web-hosting-check image-smoke hardening-check hardening-check-gates release-check-gates
module-check:
	$(GO) mod tidy -diff

web-audit:
	cd web && npm audit --audit-level=high

release-policy-check:
	PYTHONDONTWRITEBYTECODE=1 python3 scripts/check-release-policy.py
	bash -n scripts/verify-release.sh
	$(GO) run github.com/rhysd/actionlint/cmd/actionlint@v1.7.7 -shellcheck= .github/workflows/*.yml

web-hosting-check:
	PYTHONDONTWRITEBYTECODE=1 python3 -m unittest discover -s web/tests -p 'test_*.py'

# The builder validates with Go/WASM, so its dev server needs the generated
# validator assets before starting.
web-dev: wasm
	cd web && npm run dev

web-test:
	cd web && npm run test

# knip fails on unused files, exports, types, and dependencies so the UI does
# not re-accumulate dead surface area.
web-knip:
	cd web && npx --yes knip@6.36.0

# Browser tests exercise the real Go validator compiled to WebAssembly.
web-e2e: wasm
	cd web && npm run test:e2e

web-build:
	cd web && npm run build

# Public website uses the existing web app; the console has its own build.
.PHONY: website-build website-dev website-preview website-test website-e2e
website-build: web-build

website-dev: wasm
	cd web && npm run dev -- --port $${WEBSITE_PORT:-5173} --strictPort

website-preview:
	cd web && npm run preview:website

website-test:
	cd web && npm run test:docs

website-e2e: wasm website-build
	cd web && npx playwright test --config playwright.website.config.ts

console-build:
	cd web && npm run build:console

console-ui: wasm
	$(MAKE) console-build

console-e2e: console-ui
	cd web && npm run test:console

# ui assembles the embed bundle: build the WASM validator (Milestone 1) and the
# SPA, then stage both into internal/editor/webui/ for go:embed.
ui: wasm web-build
	mkdir -p internal/editor/webui
	find internal/editor/webui -mindepth 1 ! -name .gitkeep -delete
	cp -R web/dist/. internal/editor/webui/
	cp -f internal/editor/assets/hookway.wasm internal/editor/assets/wasm_exec.js internal/editor/webui/

# ui-check verifies an opt-in local editor bundle before building a binary
# that serves it; runtime Docker images intentionally omit this bundle.
ui-check:
	@if [ ! -f internal/editor/webui/index.html ] || [ ! -d internal/editor/webui/assets ]; then \
		echo "error: config UI not staged — run 'make ui' before building a release"; exit 1; \
	fi

UNIT_FLAGS := -short -run '^(Test|Example)'

unit:
	$(GO) test $(UNIT_FLAGS) ./...

race:
	CGO_ENABLED=1 $(GO) test -race $(UNIT_FLAGS) ./...

# test/test-race are aliases for unit/race so existing docs keep working.
test: unit
test-race: race

fmt-check:
	@out="$$(gofmt -l .)"; if [ -n "$$out" ]; then echo "$$out"; exit 1; fi

diff-check:
	git diff --check
	git diff --cached --check

COVERAGE_PROFILE ?= coverage.out
# Statement coverage floor for `go test -short`, rounded down to one decimal.
# Raise it when coverage improves; override per-invocation with
# `make coverage COVERAGE_FLOOR=85`.
COVERAGE_FLOOR ?= 80.9
# internal/replay/store and its container harness are measured by
# replay-integration-test instead: under -short they have nothing to run.
# Instrument the whole selected set so shared code exercised by another
# package's contract/conformance tests contributes to the aggregate report.
COVER_PKGS := $(shell $(GO) list ./... | grep -v '/internal/replay/store$$' | grep -v '/internal/replay/replaytest$$')

coverage:
	$(GO) test $(UNIT_FLAGS) -covermode=atomic -coverpkg=$$(printf '%s\n' $(COVER_PKGS) | paste -sd, -) -coverprofile=$(COVERAGE_PROFILE) $(COVER_PKGS)
	@$(GO) tool cover -func=$(COVERAGE_PROFILE) | tee coverage.txt
	@total="$$($(GO) tool cover -func=$(COVERAGE_PROFILE) | awk '/^total:/ { print substr($$3, 1, length($$3)-1) }')"; \
	floor="$(COVERAGE_FLOOR)"; \
	awk -v t="$$total" -v f="$$floor" 'BEGIN { if ((t+0) < (f+0)) { printf "coverage %s%% is below floor %s%%\n", t, f; exit 1 } printf "coverage %s%% (floor %s%%)\n", t, f }'

PROM_VERSION ?= 3.14.0
PROM_OS := $(shell $(GO) env GOOS)
PROM_ARCH := $(shell $(GO) env GOARCH)
PROM_TARBALL := prometheus-$(PROM_VERSION).$(PROM_OS)-$(PROM_ARCH).tar.gz
PROM_URL := https://github.com/prometheus/prometheus/releases/download/v$(PROM_VERSION)/$(PROM_TARBALL)
PROM_DIR := .tools/prometheus-$(PROM_VERSION)
PROMTOOL := $(PROM_DIR)/promtool

$(PROMTOOL):
	mkdir -p $(PROM_DIR)
	curl -fsSL $(PROM_URL) | tar -xz --strip-components=1 -C $(PROM_DIR)

observability-check: $(PROMTOOL)
	$(GO) test ./metrics/... ./internal/ci
	$(PROMTOOL) check rules metrics/prometheus/alerts.yaml

GOLANGCI_LINT_VERSION ?= v2.14.0
golangci-lint:
	$(GO) run github.com/golangci/golangci-lint/v2/cmd/golangci-lint@$(GOLANGCI_LINT_VERSION) run ./...

GOVULNCHECK_VERSION ?= v1.8.0
govulncheck:
	$(GO) run golang.org/x/vuln/cmd/govulncheck@$(GOVULNCHECK_VERSION) ./...

conformance:
	$(GO) test ./internal/conformance/ -v

integration-test:
	$(GO) test ./internal/queue -run TestRabbitMQ -count=1

aws-integration-test:
	$(GO) test ./internal/queue ./internal/lookup ./internal/secretref ./internal/httpapi -run 'TestAWS' -count=1

redis-integration-test:
	$(GO) test ./internal/queue -run 'TestRedis' -count=1

postgres-integration-test:
	$(GO) test ./internal/queue ./internal/lookup -run 'TestPostgres' -count=1

mongodb-integration-test:
	$(GO) test ./internal/queue -run 'TestMongo' -count=1

kafka-integration-test:
	$(GO) test ./internal/queue -run 'TestKafka' -count=1

webhook-integration-test:
	$(GO) test ./internal/queue -run 'TestWebhookIntegration' -count=1

temporal-integration-test:
	$(GO) test ./internal/queue -run 'TestTemporalIntegration' -count=1

# replay-integration-test runs the replay service's container-backed tests
# (the store and serve end to end) and enforces the store's own coverage
# floor. The store is SQL that only a real PostgreSQL can exercise, so the
# -short coverage profile leaves it out (see COVER_PKGS) and this target
# measures it instead.
REPLAY_STORE_COVERAGE_FLOOR ?= 80.0
replay-integration-test:
	$(GO) test ./internal/replay/... ./cmd/hookway-replay -count=1 -covermode=atomic -coverpkg=./internal/replay/store -coverprofile=replay-store-coverage.out
	@total="$$($(GO) tool cover -func=replay-store-coverage.out | awk '/^total:/ { print substr($$3, 1, length($$3)-1) }')"; \
	awk -v t="$$total" -v f="$(REPLAY_STORE_COVERAGE_FLOOR)" 'BEGIN { if ((t+0) < (f+0)) { printf "replay store coverage %s%% is below floor %s%%\n", t, f; exit 1 } printf "replay store coverage %s%% (floor %s%%)\n", t, f }'

# replay-acceptance-test runs the real hookway and hookway-replay binaries —
# two replicas of each — against a PostgreSQL container.
replay-acceptance-test:
	HOOKWAY_ACCEPTANCE=1 $(GO) test ./internal/acceptance -count=1 -v -timeout 15m

# compose-e2e-test deploys the `make dev-up` stack (examples/docker-compose.yaml)
# in Docker, sends signed GitHub webhooks, stops a destination, and checks that
# the missed delivery is kept by the replay service and that one retry through
# its API delivers it. Binaries are compiled on the host and copied into
# scratch images; COMPOSE_E2E_BUILD=dockerfile builds the release images from
# the Dockerfile instead (CI does).
COMPOSE_E2E_BUILD ?= binaries

ifeq ($(COMPOSE_E2E_BUILD),binaries)
compose-e2e-test: console-ui
endif
compose-e2e-test:
	HOOKWAY_COMPOSE_E2E=1 HOOKWAY_COMPOSE_E2E_BUILD=$(COMPOSE_E2E_BUILD) $(GO) test ./internal/composee2e -count=1 -timeout 30m -v

# bench-replay measures core's request-path cost of failure capture and the
# replay service's ingest throughput (the latter needs Docker).
bench-replay:
	$(GO) test ./internal/httpapi -run '^$$' -bench BenchmarkCaptureOverhead -benchtime 200x -count 1
	HOOKWAY_REQUIRE_INTEGRATION=1 $(GO) test ./internal/replay/store -run '^$$' -bench BenchmarkIngest -benchtime 30x -count 1

# test-all runs every test once, with no -short, so all nine container-backed
# suites run: RabbitMQ, AWS, Redis, Postgres, MongoDB, Kafka, webhook,
# Temporal, and the replay store. Integration tests self-skip when Docker is
# unavailable.
# -count=1 disables the test cache so the container-backed suites actually run.
test-all:
	$(GO) test ./... -count=1

lint:
	$(GO) vet ./...

# deadcode fails the build on any unreachable function. Every package here is
# internal/ or a main, so "unreachable" means genuinely dead rather than
# exported-for-consumers.
# Two passes, because they catch different things.
#
# Pass 1 (-test ./...) treats tests as roots, so it finds helpers nothing calls
# at all. It CANNOT find code that is dead in production but kept reachable by
# its own test suite -- which is exactly how a whole v1 config loader survived
# here undetected.
#
# Pass 2 (./cmd/... with no -test) roots only at the real entrypoints, so
# "unreachable" means genuinely unreachable in a shipped binary. Two known
# false positives are filtered: cmd/hookway-wasm/bridge is reachable only from
# the js/wasm-tagged main, which a host-platform analysis excludes, and
# targetResolver.dynamicLen is documented as test-only.
#
# v0.52.0's SSA builder resolves promoted fields used as struct-literal
# keys. v0.38.0 panics on that Go 1.27 syntax with
# "unexpected expr: *ast.KeyValueExpr" (for example splicePipe{rfd, wfd}
# in the standard library). The tool's own go.mod still says go 1.26.0, so
# build it with this module's toolchain; otherwise go run switches to Go
# 1.26 and rejects the Go 1.27 standard library.
DEADCODE_ALLOW := cmd/hookway-wasm/bridge/|internal/queue/target_resolver.go.*dynamicLen
DEADCODE := golang.org/x/tools/cmd/deadcode@v0.52.0
MODULE_GO := $(shell awk '/^go / { print $$2; exit }' go.mod)

deadcode:
	@echo "deadcode: checking tests and entrypoints"
	@out="$$(GOTOOLCHAIN=go$(MODULE_GO) $(GO) run $(DEADCODE) -test ./...)" || exit $$?; \
	if [ -n "$$out" ]; then echo "dead code found (reachable from no test or entrypoint):"; echo "$$out"; exit 1; fi
	@echo "deadcode: checking production entrypoints"
	@out="$$(GOTOOLCHAIN=go$(MODULE_GO) $(GO) run $(DEADCODE) ./cmd/...)" || exit $$?; \
	out="$$(printf '%s\n' "$$out" | grep -Ev '$(DEADCODE_ALLOW)' || true)"; \
	if [ -n "$$out" ]; then echo "dead code found (unreachable from any entrypoint; tests keep it alive):"; echo "$$out"; exit 1; fi
	@echo "no dead code"

validate:
	$(GO) run ./cmd/hookway validate --config $(CONFIG)

# JWT example placeholders are checked offline; test-examples separately
# constructs real test keys and verifies its signed requests.
validate-examples:
	@export RABBITMQ_URL=amqp://guest:guest@localhost:5672/ \
		PADDLE_WEBHOOK_SECRET=dummy \
		GITHUB_WEBHOOK_SECRET=dummy \
		STRIPE_WEBHOOK_SECRET=dummy \
		ORDERS_WEBHOOK_SECRET=dummy \
		NOTIFY_WEBHOOK_SECRET=dummy \
		SAMPLE_WEBHOOK_SECRET=dummy \
		REVENUECAT_WEBHOOK_AUTH=dummy \
		BASIC_WEBHOOK_SECRET=dummy \
		JWT_PUBLIC_KEY=dummy \
		REDIS_URL=redis://localhost:6379 \
		POSTGRES_URL=postgres://localhost:5432/db \
		PROTECTION_DATABASE_URL=postgres://localhost:5432/protection \
		PROTECTION_REDIS_URL=redis://localhost:6379 \
		MONGODB_URI=mongodb://localhost:27017 \
		KAFKA_BROKER_1=localhost:9092 \
		KAFKA_BROKER_2=localhost:9092 \
		KAFKA_USERNAME=dummy \
		KAFKA_PASSWORD=dummy \
		WEBHOOK_SECRET=dummy \
		WEBHOOK_URL=https://example.com/hooks \
		WEBHOOK_API_KEY=dummy \
		TEMPORAL_ADDRESS=localhost:7233 \
		TEMPORAL_NAMESPACE=default \
		TEMPORAL_API_KEY=dummy \
		ARCHIVE_BUCKET=archive \
		DOWNSTREAM_API_KEY=dummy \
		VENDOR_WEBHOOK_TOKEN=dummy \
		UPSTREAM_BASE=https://example.test \
		PROXY_TOKEN=testtoken \
		ROUTING_TABLE=routing_data \
		ROUTING_DB_URL=postgres://localhost:5432/routing \
		SLACK_WEBHOOK_URL=https://hooks.slack.test/services/T000/B000/dummy \
		SLACK_SOURCE_SECRET=dummy \
		DISCORD_WEBHOOK_URL=https://discord.test/api/webhooks/000/dummy \
		DISCORD_SOURCE_SECRET=dummy \
		UPLOAD_SOURCE_SECRET=dummy \
		XML_SOURCE_SECRET=dummy \
		XML_UPSTREAM_URL=https://upstream.test/xml \
		HOOKWAY_API_KEY=dummy \
		REPLAY_CAPTURE_TOKEN=capture-token-0123456789abcdefghijkl \
		REPLAY_CONTROL_TOKEN=control-token-0123456789abcdefghij \
		REPLAY_OPERATOR_TOKEN=operator-token-0123456789abcdefghi \
		REPLAY_METRICS_TOKEN=metrics-token-0123456789abcdefghijk \
		REPLAY_CONSOLE_ORIGIN=http://localhost:8082 \
		REPLAY_MIGRATION_PASSWORD=migration-test-password-0123456789abcdef \
		REPLAY_MIGRATION_DATABASE_URL=postgres://replay_owner@localhost:5432/replay \
		REPLAY_DATABASE_URL=postgres://localhost:5432/replay; \
	for f in $$(find examples -maxdepth 1 -name '*.yaml' ! -name 'docker-compose.yaml' | sort) examples/destinations/*.yaml examples/auth/*.yaml examples/routing/*.yaml examples/proxy/*.yaml examples/credentials/*.yaml examples/adapters/*.yaml examples/templates/*.yaml examples/providers/*.yaml examples/protection/*.yaml; do \
		echo "validating $$f"; \
		case $$f in examples/auth/jwt.yaml|examples/providers/*.yaml) opts="--no-env" ;; *) opts="" ;; esac; \
		$(GO) run ./cmd/hookway validate $$opts --config $$f || exit 1; \
	done; \
	for f in examples/showcase/*.yaml; do \
		case $$f in *invalid-*) continue ;; esac; \
		echo "validating $$f"; \
		$(GO) run ./cmd/hookway validate --config $$f || exit 1; \
	done; \
	$(GO) run ./cmd/hookway validate --config examples/multi-yaml/core.yaml || exit 1; \
	for d in dev replay; do \
		echo "validating examples/$$d/core.yaml"; \
		$(GO) run ./cmd/hookway validate --config examples/$$d/core.yaml || exit 1; \
		echo "validating examples/$$d/replay.yaml"; \
		$(GO) run ./cmd/hookway-replay validate --config examples/$$d/replay.yaml || exit 1; \
	done; \
	echo "validating examples/production-local/replay.yaml"; \
	$(GO) run ./cmd/hookway-replay validate --config examples/production-local/replay.yaml || exit 1; \
	$(GO) run ./cmd/hookway-replay validate --config examples/production-local/migration/replay.yaml || exit 1; \
	for f in examples/production-local/core*.yaml; do $(GO) run ./cmd/hookway validate --config "$$f" || exit 1; done

# test-examples boots every valid example config and POSTs a signed request to
# each flow. Destinations are faked; env-driven http_request URLs hit a loopback
# server. This is stricter than validate-examples: JWT keys must parse, filters
# and allowlists must accept a representative payload, and the graph must run.
test-examples:
	$(GO) test ./internal/examplesmoke -count=1 -timeout 5m

run:
	HOOKWAY_CONFIG=$(CONFIG) $(GO) run ./cmd/hookway

# Core and replay need only Go; the standalone frontend builds its own WASM. .dockerignore excludes
# generated editor assets even when `make ui` ran in the local checkout.
docker-build:
	docker build --target core --build-arg VERSION=$(VERSION) --build-arg COMMIT=$(COMMIT) -t $(IMAGE) .
	docker build --target replay --build-arg VERSION=$(VERSION) --build-arg COMMIT=$(COMMIT) -t $(REPLAY_IMAGE) .
	docker build -f web/Dockerfile --build-arg VERSION=$(VERSION) --build-arg COMMIT=$(COMMIT) -t $(CONSOLE_IMAGE) .

vuln-scan:
	docker run --rm -v /var/run/docker.sock:/var/run/docker.sock aquasec/trivy:latest image --exit-code 1 --severity HIGH,CRITICAL $(IMAGE)
	docker run --rm -v /var/run/docker.sock:/var/run/docker.sock aquasec/trivy:latest image --exit-code 1 --severity HIGH,CRITICAL $(REPLAY_IMAGE)
	docker run --rm -v /var/run/docker.sock:/var/run/docker.sock aquasec/trivy:latest image --exit-code 1 --severity HIGH,CRITICAL $(CONSOLE_IMAGE)

image-smoke: docker-build
	docker run --rm "$(IMAGE)" version
	docker run --rm "$(REPLAY_IMAGE)" version
	PYTHONDONTWRITEBYTECODE=1 python3 -c 'import importlib.util,sys; spec=importlib.util.spec_from_file_location("smoke","scripts/release-smoke.py"); smoke=importlib.util.module_from_spec(spec); spec.loader.exec_module(smoke); smoke.console_install(sys.argv[1],sys.argv[2],sys.argv[3],platform=smoke.run("docker","image","inspect","--format","{{.Os}}/{{.Architecture}}",sys.argv[1]))' "$(CONSOLE_IMAGE)" "$(VERSION)" "$(COMMIT)"

# Branch hardening does not build, publish or sign application images. Docker
# is still needed by backend integration and monitoring fixtures. Packaged-image
# acceptance stays available separately for a later release or local rehearsal.
hardening-check: web-install
	HOOKWAY_REQUIRE_INTEGRATION=1 $(MAKE) hardening-check-gates

hardening-check-gates: module-check docs-check fmt-check diff-check lint golangci-lint govulncheck release-policy-check web-audit web-hosting-check node-schema-check api-schema-check web-build console-build web-knip validate-examples test-examples test test-race web-test coverage deadcode conformance observability-check web-e2e console-e2e integration-test aws-integration-test redis-integration-test postgres-integration-test mongodb-integration-test kafka-integration-test webhook-integration-test temporal-integration-test replay-integration-test replay-acceptance-test

# Optional packaged gate, matching the complete reusable CI including images.
# Missing Docker is a release-gate failure, never an integration-test skip.
# The recursive gate also selects the actual Dockerfiles, while ordinary native
# tests and source builds retain their convenient defaults.
release-check: web-install
	HOOKWAY_REQUIRE_INTEGRATION=1 $(MAKE) release-check-gates COMPOSE_E2E_BUILD=dockerfile

release-check-gates: hardening-check-gates compose-e2e-test image-smoke vuln-scan

dev-up:
	docker compose -f examples/docker-compose.yaml up --build

# --volumes: the broker and the replay store keep their data in anonymous
# volumes that a fresh `up` never reattaches, so leaving them only leaks disk.
dev-down:
	docker compose -f examples/docker-compose.yaml down --volumes

# Persistent local production setup. Unlike dev-down, ordinary down retains data.
# Bootstrap copies ordinary YAML once; no generator or test helper is required.
.PHONY: prod-local-bootstrap prod-local-check prod-local-role-check prod-local-credentials-upgrade prod-local-up prod-local-validate prod-local-down prod-local-restart prod-local-status prod-local-logs prod-local-reset
PROD_LOCAL_DIRECTORY ?= examples/production-local/.local
PROD_LOCAL_PROJECT ?=
PROD_LOCAL_MODE ?= core
PROD_LOCAL_PROTECTION ?= 0
PROD_LOCAL_BUILD ?= 1
PROD_LOCAL_TLS ?= 0
PROD_LOCAL_STRICT ?= 0
PROD_LOCAL_CONFIRM_RESET ?=
PROD_LOCAL_COMPOSE = HOOKWAY_LOCAL_DIRECTORY="$(abspath $(PROD_LOCAL_DIRECTORY))" docker compose --project-name "$$(sed -n 's/^COMPOSE_PROJECT_NAME=//p' "$(PROD_LOCAL_DIRECTORY)/.env")" --env-file "$(PROD_LOCAL_DIRECTORY)/.env" -f examples/production-local/docker-compose.yaml $(if $(filter 1,$(PROD_LOCAL_TLS)),-f examples/production-local/tls/docker-compose.yaml,)

prod-local-bootstrap:
	@set -eu; state="$(PROD_LOCAL_DIRECTORY)"; mode="$(PROD_LOCAL_MODE)"; protection="$(PROD_LOCAL_PROTECTION)"; project="$(PROD_LOCAL_PROJECT)"; \
	case "$$mode" in core|full) ;; *) echo "PROD_LOCAL_MODE must be core or full" >&2; exit 1 ;; esac; \
	case "$$protection" in 0|1) ;; *) echo "PROD_LOCAL_PROTECTION must be 0 or 1" >&2; exit 1 ;; esac; \
	if [ -L "$$state" ] || [ -L "$$state/.env" ] || [ -L "$$state/core.yaml" ] || [ -L "$$state/replay.yaml" ] || [ -L "$$state/migrate.yaml" ]; then echo "Refusing symlink state/configuration" >&2; exit 1; fi; \
	if [ -f "$$state/.env" ]; then \
		saved="$$(sed -n 's/^COMPOSE_PROJECT_NAME=//p' "$$state/.env")"; project="$${project:-$$saved}"; \
		if [ "$$saved" != "$$project" ] || [ "$$(sed -n 's/^HOOKWAY_LOCAL_MODE=//p' "$$state/.env")" != "$$mode" ] || [ "$$(sed -n 's/^HOOKWAY_LOCAL_PROTECTION=//p' "$$state/.env")" != "$$protection" ]; then echo "Existing selection differs; retain original options or choose a separate directory/project" >&2; exit 1; fi; \
	else \
		project="$${project:-hookway-production}"; \
		case "$$project" in ''|*[!a-z0-9_-]*|[-_]*) echo "Invalid project name (use lowercase letters/digits, _ or -)" >&2; exit 1 ;; esac; \
		api_port="$${REPLAY_HTTP_PORT:-18081}"; console_port="$${CONSOLE_HTTP_PORT:-18082}"; core_port="$${HOOKWAY_HTTP_PORT:-18080}"; rabbit_port="$${RABBITMQ_HTTP_PORT:-15673}"; \
		for port in "$$api_port" "$$console_port" "$$core_port" "$$rabbit_port"; do case "$$port" in ''|*[!0-9]*) echo "Host ports must be numbers from 0 to 65535" >&2; exit 1 ;; esac; [ "$$port" -le 65535 ] || { echo "Host port exceeds 65535" >&2; exit 1; }; done; \
		if [ "$$mode" = full ] && { { [ "$$api_port" -eq 0 ] && [ -z "$${REPLAY_API_URL:-}" ]; } || { [ "$$console_port" -eq 0 ] && [ -z "$${REPLAY_CONSOLE_ORIGIN:-}" ]; }; }; then echo "Full mode with dynamic port 0 requires explicit REPLAY_API_URL and REPLAY_CONSOLE_ORIGIN for the browser" >&2; exit 1; fi; \
		umask 077; mkdir -p "$$state"; temporary="$$(mktemp "$$state/.env.XXXXXX")"; trap 'rm -f "$$temporary"' EXIT HUP INT TERM; \
		for name in RABBITMQ_PASSWORD POSTGRES_PASSWORD REPLAY_MIGRATION_PASSWORD REPLAY_RUNTIME_PASSWORD REDIS_PASSWORD GITHUB_WEBHOOK_SECRET HOOKWAY_API_KEY REPLAY_CAPTURE_TOKEN REPLAY_CONTROL_TOKEN REPLAY_OPERATOR_TOKEN REPLAY_METRICS_TOKEN; do value="$$(openssl rand -hex 32)"; printf '%s=%s\n' "$$name" "$$value" >> "$$temporary"; done; \
		rabbit="$$(sed -n 's/^RABBITMQ_PASSWORD=//p' "$$temporary")"; migration="$$(sed -n 's/^REPLAY_MIGRATION_PASSWORD=//p' "$$temporary")"; runtime="$$(sed -n 's/^REPLAY_RUNTIME_PASSWORD=//p' "$$temporary")"; redis="$$(sed -n 's/^REDIS_PASSWORD=//p' "$$temporary")"; \
		profiles=""; [ "$$mode" != full ] || profiles=replay; [ "$$protection" != 1 ] || profiles="$${profiles:+$$profiles,}protection"; \
		printf '%s\n' "COMPOSE_PROJECT_NAME=$$project" "COMPOSE_PROFILES=$$profiles" "HOOKWAY_LOCAL_MODE=$$mode" "HOOKWAY_LOCAL_PROTECTION=$$protection" \
			"RABBITMQ_URL=amqp://hookway:$$rabbit@rabbitmq:5672/" "REPLAY_DATABASE_URL=postgres://replay_runtime:$$runtime@postgres:5432/replay?sslmode=disable" \
			"REPLAY_MIGRATION_DATABASE_URL=postgres://replay_owner:$$migration@postgres:5432/replay?sslmode=disable" "REPLAY_RUNTIME_DATABASE_URL=postgres://replay_runtime:$$runtime@postgres:5432/replay?sslmode=disable" "PROTECTION_REDIS_URL=redis://:$$redis@redis:6379/0" \
			"HOOKWAY_HTTP_PORT=$$core_port" "REPLAY_HTTP_PORT=$$api_port" "CONSOLE_HTTP_PORT=$$console_port" "RABBITMQ_HTTP_PORT=$$rabbit_port" \
			"REPLAY_API_URL=$${REPLAY_API_URL:-http://localhost:$$api_port}" "REPLAY_CONSOLE_ORIGIN=$${REPLAY_CONSOLE_ORIGIN:-http://localhost:$$console_port}" >> "$$temporary"; \
		ln "$$temporary" "$$state/.env"; \
	fi; \
	config=core; [ "$$mode" != full ] || config=core-replay; [ "$$protection" != 1 ] || config="$$config-shared"; \
	if [ ! -e "$$state/core.yaml" ]; then cp "examples/production-local/$$config.yaml" "$$state/core.yaml"; chmod 644 "$$state/core.yaml"; fi; \
	if [ ! -e "$$state/replay.yaml" ]; then cp examples/production-local/replay.yaml "$$state/replay.yaml"; chmod 644 "$$state/replay.yaml"; fi; \
	if [ ! -e "$$state/migrate.yaml" ]; then cp examples/production-local/migration/replay.yaml "$$state/migrate.yaml"; chmod 644 "$$state/migrate.yaml"; fi; \
	echo "Local production configuration ready: $$state (project $$project, $$mode). Existing files preserved."

prod-local-check:
	@set -eu; state="$(PROD_LOCAL_DIRECTORY)"; \
	if [ -L "$$state" ] || [ -L "$$state/.env" ] || [ -L "$$state/core.yaml" ] || [ -L "$$state/replay.yaml" ] || [ -L "$$state/migrate.yaml" ]; then echo "Refusing symlink state/configuration" >&2; exit 1; fi; \
	[ -f "$$state/.env" ] && [ -f "$$state/core.yaml" ] && [ -f "$$state/replay.yaml" ] || { echo "Run make prod-local-bootstrap first" >&2; exit 1; }; \
	saved="$$(sed -n 's/^COMPOSE_PROJECT_NAME=//p' "$$state/.env")"; \
	case "$$saved" in ''|*[!a-z0-9_-]*|[-_]*) echo "Invalid saved COMPOSE_PROJECT_NAME" >&2; exit 1 ;; esac; \
	[ -z "$(PROD_LOCAL_PROJECT)" ] || [ "$(PROD_LOCAL_PROJECT)" = "$$saved" ] || { echo "Project differs from bootstrapped project" >&2; exit 1; }
	@if [ "$(PROD_LOCAL_TLS)" = 1 ]; then [ -f "$(PROD_LOCAL_DIRECTORY)/localhost.crt" ] && [ -f "$(PROD_LOCAL_DIRECTORY)/localhost.key" ] || { echo "TLS overlay requires localhost.crt and private localhost.key in the state directory; see local-development.md" >&2; exit 1; }; fi

prod-local-role-check: prod-local-check
	@if [ "$$(sed -n 's/^HOOKWAY_LOCAL_MODE=//p' "$(PROD_LOCAL_DIRECTORY)/.env")" = full ]; then \
		for key in REPLAY_MIGRATION_PASSWORD REPLAY_RUNTIME_PASSWORD REPLAY_MIGRATION_DATABASE_URL REPLAY_RUNTIME_DATABASE_URL; do \
			[ -n "$$(sed -n "s/^$$key=//p" "$(PROD_LOCAL_DIRECTORY)/.env")" ] || { echo "Missing $$key; run make prod-local-credentials-upgrade with this PROD_LOCAL_DIRECTORY" >&2; exit 1; }; \
		done; \
		[ -f "$(PROD_LOCAL_DIRECTORY)/migrate.yaml" ] || { echo "Missing migrate.yaml; run make prod-local-credentials-upgrade" >&2; exit 1; }; \
	fi

# Explicitly extend an older setup's credentials; never regenerate existing keys.
prod-local-credentials-upgrade: prod-local-check
	@set -eu; state="$(PROD_LOCAL_DIRECTORY)"; umask 077; temporary="$$(mktemp "$$state/.env.XXXXXX")"; trap 'rm -f "$$temporary"' EXIT HUP INT TERM; cp "$$state/.env" "$$temporary"; chmod 600 "$$temporary"; \
	for key in REPLAY_MIGRATION_PASSWORD REPLAY_RUNTIME_PASSWORD; do \
		if ! sed -n "s/^$$key=//p" "$$temporary" | grep -q .; then value="$$(openssl rand -hex 32)"; printf '%s=%s\n' "$$key" "$$value" >> "$$temporary"; fi; \
	done; \
	migration="$$(sed -n 's/^REPLAY_MIGRATION_PASSWORD=//p' "$$temporary" | tail -n 1)"; runtime="$$(sed -n 's/^REPLAY_RUNTIME_PASSWORD=//p' "$$temporary" | tail -n 1)"; \
	if ! sed -n 's/^REPLAY_MIGRATION_DATABASE_URL=//p' "$$temporary" | grep -q .; then printf '%s\n' "REPLAY_MIGRATION_DATABASE_URL=postgres://replay_owner:$$migration@postgres:5432/replay?sslmode=disable" >> "$$temporary"; fi; \
	if ! sed -n 's/^REPLAY_RUNTIME_DATABASE_URL=//p' "$$temporary" | grep -q .; then printf '%s\n' "REPLAY_RUNTIME_DATABASE_URL=postgres://replay_runtime:$$runtime@postgres:5432/replay?sslmode=disable" >> "$$temporary"; fi; \
	if ! cmp -s "$$temporary" "$$state/.env"; then mv "$$temporary" "$$state/.env"; fi; \
	if [ ! -e "$$state/migrate.yaml" ]; then cp examples/production-local/migration/replay.yaml "$$state/migrate.yaml"; chmod 644 "$$state/migrate.yaml"; fi; \
	echo "Database credentials ready. Existing keys/configuration/data preserved. Restart the full stack to provision roles and grants."

prod-local-up: prod-local-validate
	@if [ "$$(sed -n 's/^HOOKWAY_LOCAL_MODE=//p' "$(PROD_LOCAL_DIRECTORY)/.env")" = full ]; then \
		set -e; \
		$(PROD_LOCAL_COMPOSE) up --detach --wait --wait-timeout 180 postgres; \
		$(PROD_LOCAL_COMPOSE) run --rm --no-deps db-provision; \
		$(PROD_LOCAL_COMPOSE) run --rm --no-deps $(if $(filter 0,$(PROD_LOCAL_BUILD)),--pull never,--build) migrate; \
		$(PROD_LOCAL_COMPOSE) run --rm --no-deps db-grants; \
	fi
	$(PROD_LOCAL_COMPOSE) up --detach --wait --wait-timeout 180 $(if $(filter 0,$(PROD_LOCAL_BUILD)),--no-build,--build)

prod-local-down: prod-local-check
	$(PROD_LOCAL_COMPOSE) down

# Validate before disturbing a healthy stack, then restart with the same gates
# as first installation. Ordinary restart retains all named volumes.
prod-local-restart: prod-local-role-check
	$(MAKE) --no-print-directory prod-local-validate PROD_LOCAL_BUILD=0
	$(MAKE) --no-print-directory prod-local-down
	$(MAKE) --no-print-directory prod-local-up PROD_LOCAL_BUILD=0

prod-local-status: prod-local-check
	$(PROD_LOCAL_COMPOSE) ps --all

prod-local-logs: prod-local-check
	$(PROD_LOCAL_COMPOSE) logs --tail 100

prod-local-validate: prod-local-role-check
	$(PROD_LOCAL_COMPOSE) run --rm --no-deps $(if $(filter 0,$(PROD_LOCAL_BUILD)),--pull never,--build) hookway validate --config /etc/hookway/core.yaml $(if $(filter 1,$(PROD_LOCAL_STRICT)),--strict,)
	@if [ "$$(sed -n 's/^HOOKWAY_LOCAL_MODE=//p' "$(PROD_LOCAL_DIRECTORY)/.env")" = full ]; then $(PROD_LOCAL_COMPOSE) run --rm --no-deps $(if $(filter 0,$(PROD_LOCAL_BUILD)),--pull never,--build) replay validate --config /etc/hookway/replay.yaml; fi

prod-local-reset: prod-local-check
	@project="$$(sed -n 's/^COMPOSE_PROJECT_NAME=//p' "$(PROD_LOCAL_DIRECTORY)/.env")"; \
	[ "$(PROD_LOCAL_CONFIRM_RESET)" = "$$project" ] || { echo "Reset deletes project data; set PROD_LOCAL_CONFIRM_RESET=$$project" >&2; exit 1; }; \
	echo "Deleting containers and named volumes for $$project. Credentials/configuration retained."; \
	$(PROD_LOCAL_COMPOSE) down --volumes
