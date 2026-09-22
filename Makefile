.PHONY: deadcode web-knip test test-all test-race test-examples conformance integration-test aws-integration-test redis-integration-test postgres-integration-test mongodb-integration-test kafka-integration-test webhook-integration-test temporal-integration-test replaystore-integration-test lint validate validate-examples build wasm wasm-harness web-install web-dev web-test web-e2e web-build ui ui-check node-schema node-schema-check run docker-build vuln-scan release-check dev-up dev-down unit race fmt-check diff-check coverage observability-check golangci-lint govulncheck

GO ?= go
CONFIG ?= examples/config.yaml
IMAGE ?= hookway:local

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

wasm:
	mkdir -p internal/editor/assets web/public
	GOOS=js GOARCH=wasm $(GO) build -o internal/editor/assets/hookway.wasm ./cmd/hookway-wasm
	cp "$$($(GO) env GOROOT)/lib/wasm/wasm_exec.js" internal/editor/assets/wasm_exec.js
	cp internal/editor/assets/hookway.wasm internal/editor/assets/wasm_exec.js web/public/

wasm-harness: wasm
	cp internal/editor/assets/hookway.wasm internal/editor/assets/wasm_exec.js cmd/hookway-wasm/harness/
	@command -v python3 >/dev/null 2>&1 || { \
		echo "wasm-harness needs python3 to serve the harness over http://."; \
		echo "Opening index.html as a file:// URL will not work: the WASM fetch is blocked by CORS."; \
		echo "Install python3, or serve cmd/hookway-wasm/harness with any static file server on port 8091."; \
		exit 1; }
	@echo "Serving harness on http://localhost:8091 (Ctrl-C to stop)"
	cd cmd/hookway-wasm/harness && python3 -m http.server 8091

# node-schema regenerates the JSON catalogs the config UI imports. node-schema-check
# fails if the committed files drifted from `hookway schema --nodes/--destinations`.
node-schema:
	$(GO) run ./cmd/hookway schema --nodes > web/src/graph/nodeSchema.json
	$(GO) run ./cmd/hookway schema --destinations > web/src/graph/destinationSchema.json

node-schema-check: node-schema
	@git diff --exit-code -- web/src/graph/nodeSchema.json web/src/graph/destinationSchema.json

web-install:
	cd web && npm install

# Depends on wasm because the dev server otherwise 404s hookway.wasm, and
# validate.ts then silently falls back to a dead /validate fetch -- every
# in-browser validation assertion passes vacuously instead of failing.
web-dev: wasm
	cd web && npm run dev

web-test:
	cd web && npm run test

# knip fails on unused files, exports, types, and dependencies so the UI does
# not re-accumulate dead surface area.
web-knip:
	cd web && npx --yes knip@6.36.0

# web-e2e depends on wasm because the builder's live validation runs the real
# Go validator compiled to WebAssembly, served from web/public. That file is
# generated and gitignored, so without this prerequisite the dev server 404s
# it, validate.ts silently falls back to a dead /validate fetch, and every
# validation assertion passes vacuously.
web-e2e: wasm
	cd web && npm run test:e2e

web-build:
	cd web && npm run build

# ui assembles the embed bundle: build the WASM validator (Milestone 1) and the
# SPA, then stage both into internal/editor/webui/ for go:embed.
ui: wasm web-build
	mkdir -p internal/editor/webui
	find internal/editor/webui -mindepth 1 ! -name .gitkeep -delete
	cp -R web/dist/. internal/editor/webui/
	cp internal/editor/assets/hookway.wasm internal/editor/assets/wasm_exec.js internal/editor/webui/

# ui-check fails unless a real bundle is staged for go:embed, guarding against
# shipping a release binary whose editor only serves the placeholder page.
ui-check:
	@if [ ! -f internal/editor/webui/index.html ] || [ ! -d internal/editor/webui/assets ]; then \
		echo "error: config UI not staged — run 'make ui' before building a release"; exit 1; \
	fi

UNIT_FLAGS := -short -run '^(Test|Example)'

unit:
	$(GO) test $(UNIT_FLAGS) ./...

race:
	$(GO) test -race $(UNIT_FLAGS) ./...

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
COVER_PKGS := $(shell $(GO) list ./... | grep -v '/scripts/')

coverage:
	$(GO) test $(UNIT_FLAGS) -covermode=atomic -coverprofile=$(COVERAGE_PROFILE) $(COVER_PKGS)
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
	$(GO) test ./grafana/dashboards ./deploy
	$(GO) run ./scripts/promrule2groups deploy/alerts.yaml | $(PROMTOOL) check rules /dev/stdin

GOLANGCI_LINT_VERSION ?= v2.13.2
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
	$(GO) test ./internal/queue ./internal/lookup ./internal/secretref -run 'TestAWS' -count=1

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

replaystore-integration-test:
	$(GO) test ./internal/replaystore -run 'TestReplayStore' -count=1

# test-all runs every test once, with no -short, so all nine container-backed
# suites run: RabbitMQ, AWS, Redis, Postgres, MongoDB, Kafka, webhook, Temporal,
# and the replay store. Integration tests self-skip when Docker is unavailable.
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
DEADCODE_ALLOW := cmd/hookway-wasm/bridge/|internal/queue/target_resolver.go.*dynamicLen

deadcode:
	@out="$$($(GO) run golang.org/x/tools/cmd/deadcode@v0.38.0 -test ./... 2>/dev/null)"; \
	if [ -n "$$out" ]; then echo "dead code found (reachable from no test or entrypoint):"; echo "$$out"; exit 1; fi
	@out="$$($(GO) run golang.org/x/tools/cmd/deadcode@v0.38.0 ./cmd/... 2>/dev/null | grep -Ev '$(DEADCODE_ALLOW)')"; \
	if [ -n "$$out" ]; then echo "dead code found (unreachable from any entrypoint; tests keep it alive):"; echo "$$out"; exit 1; fi
	@echo "no dead code"

validate:
	$(GO) run ./cmd/hookway validate --config $(CONFIG)

validate-examples:
	@export RABBITMQ_URL=amqp://guest:guest@localhost:5672/ \
		PADDLE_WEBHOOK_SECRET=dummy \
		GITHUB_WEBHOOK_SECRET=dummy \
		STRIPE_WEBHOOK_SECRET=dummy \
		ORDERS_WEBHOOK_SECRET=dummy \
		NOTIFY_WEBHOOK_SECRET=dummy \
		REVENUECAT_WEBHOOK_AUTH=dummy \
		BASIC_WEBHOOK_SECRET=dummy \
		JWT_PUBLIC_KEY=dummy \
		REDIS_URL=redis://localhost:6379 \
		POSTGRES_URL=postgres://localhost:5432/db \
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
		REPLAY_DB_URL=postgres://localhost:5432/db \
		REPLAY_API_KEY=dummy \
		SLACK_WEBHOOK_URL=https://hooks.slack.test/services/T000/B000/dummy \
		SLACK_SOURCE_SECRET=dummy \
		DISCORD_WEBHOOK_URL=https://discord.test/api/webhooks/000/dummy \
		DISCORD_SOURCE_SECRET=dummy \
		UPLOAD_SOURCE_SECRET=dummy \
		XML_SOURCE_SECRET=dummy \
		XML_UPSTREAM_URL=https://upstream.test/xml \
		HOOKWAY_API_KEY=dummy; \
	for f in $$(find examples -maxdepth 1 -name '*.yaml' ! -name 'docker-compose.yaml' | sort) examples/destinations/*.yaml examples/auth/*.yaml examples/templates/*.yaml; do \
		echo "validating $$f"; \
		$(GO) run ./cmd/hookway validate --config $$f || exit 1; \
	done; \
	for f in examples/showcase/*.yaml; do \
		case $$f in *invalid-*) continue ;; esac; \
		echo "validating $$f"; \
		$(GO) run ./cmd/hookway validate --config $$f || exit 1; \
	done

# test-examples boots every valid example config and POSTs a signed request to
# each flow. Destinations are faked; env-driven http_request URLs hit a loopback
# server. This is stricter than validate-examples: JWT keys must parse, filters
# and allowlists must accept a representative payload, and the graph must run.
test-examples:
	$(GO) test ./internal/examplesmoke -count=1 -timeout 5m

run:
	HOOKWAY_CONFIG=$(CONFIG) $(GO) run ./cmd/hookway

docker-build: ui
	docker build --build-arg VERSION=$(VERSION) --build-arg COMMIT=$(COMMIT) -t $(IMAGE) .

vuln-scan:
	docker run --rm -v /var/run/docker.sock:/var/run/docker.sock aquasec/trivy:latest image --exit-code 1 --severity HIGH,CRITICAL $(IMAGE)

# Mirrors the required CI gate (.github/workflows/reusable-ci.yml `all-checks-passed`).
# Keep these two lists in sync: a green release-check should predict a green CI.
release-check: validate-examples test-examples test test-race integration-test aws-integration-test redis-integration-test postgres-integration-test mongodb-integration-test kafka-integration-test webhook-integration-test temporal-integration-test replaystore-integration-test lint golangci-lint govulncheck coverage fmt-check deadcode conformance observability-check ui-check docker-build vuln-scan

dev-up:
	docker compose -f examples/docker-compose.yaml up --build

dev-down:
	docker compose -f examples/docker-compose.yaml down
