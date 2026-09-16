.PHONY: deadcode web-knip test test-all test-race conformance integration-test aws-integration-test redis-integration-test postgres-integration-test mongodb-integration-test kafka-integration-test webhook-integration-test temporal-integration-test replaystore-integration-test lint validate validate-examples build wasm wasm-harness web-install web-dev web-test web-e2e web-build ui ui-check node-schema node-schema-check run docker-build vuln-scan release-check dev-up dev-down

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

web-dev:
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

test:
	$(GO) test ./... -run '^(Test|Example)' -skip 'RabbitMQ|AWS|Redis|Postgres|Mongo|Kafka|ReplayStore|TemporalIntegration'

test-race:
	$(GO) test -race ./... -run '^(Test|Example)' -skip 'RabbitMQ|AWS|Redis|Postgres|Mongo|Kafka|ReplayStore|TemporalIntegration'

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

# test-all runs every test once: unit + RabbitMQ + AWS + Redis integration suites.
# Integration tests self-skip when Docker is unavailable. -count=1 disables the
# test cache so the container-backed suites actually run.
test-all:
	$(GO) test ./... -count=1

lint:
	$(GO) vet ./...

# deadcode fails the build on any unreachable function. Every package here is
# internal/ or a main, so "unreachable" means genuinely dead rather than
# exported-for-consumers.
deadcode:
	@out="$$($(GO) run golang.org/x/tools/cmd/deadcode@v0.38.0 -test ./... 2>/dev/null)"; \
	if [ -n "$$out" ]; then echo "dead code found:"; echo "$$out"; exit 1; fi
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
		HUB_API_KEY=dummy \
		DIGIFI_WEBHOOK_TOKEN=dummy \
		DIGIFI_BASE=https://example.test \
		SF_TOKEN=testtoken \
		ROUTING_TABLE=sandbox_ephemeral_routing_data \
		ROUTING_DB_URL=postgres://localhost:5432/routing \
		REPLAY_DB_URL=postgres://localhost:5432/db \
		REPLAY_API_KEY=dummy; \
	for f in examples/config.yaml examples/filters.yaml examples/replay.yaml examples/variables.yaml examples/on_error.yaml examples/destinations/*.yaml examples/auth/*.yaml; do \
		echo "validating $$f"; \
		$(GO) run ./cmd/hookway validate --config $$f || exit 1; \
	done; \
	for f in examples/showcase/*.yaml; do \
		case $$f in *invalid-*) continue ;; esac; \
		echo "validating $$f"; \
		$(GO) run ./cmd/hookway validate --config $$f || exit 1; \
	done

run:
	HOOKWAY_CONFIG=$(CONFIG) $(GO) run ./cmd/hookway

docker-build: ui
	docker build --build-arg VERSION=$(VERSION) --build-arg COMMIT=$(COMMIT) -t $(IMAGE) .

vuln-scan:
	docker run --rm -v /var/run/docker.sock:/var/run/docker.sock aquasec/trivy:latest image --exit-code 1 --severity HIGH,CRITICAL $(IMAGE)

release-check: validate-examples test test-race integration-test aws-integration-test redis-integration-test postgres-integration-test mongodb-integration-test kafka-integration-test webhook-integration-test temporal-integration-test replaystore-integration-test lint ui-check docker-build vuln-scan

dev-up:
	docker compose -f examples/docker-compose.yaml up --build

dev-down:
	docker compose -f examples/docker-compose.yaml down
