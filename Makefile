.PHONY: test test-all test-race integration-test aws-integration-test redis-integration-test postgres-integration-test mongodb-integration-test kafka-integration-test webhook-integration-test replaystore-integration-test lint validate validate-examples build wasm wasm-harness web-install web-dev web-test web-build ui ui-check run docker-build vuln-scan release-check dev-up dev-down

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
	mkdir -p internal/editor/assets
	GOOS=js GOARCH=wasm $(GO) build -o internal/editor/assets/hookway.wasm ./cmd/hookway-wasm
	cp "$$($(GO) env GOROOT)/lib/wasm/wasm_exec.js" internal/editor/assets/wasm_exec.js

wasm-harness: wasm
	cp internal/editor/assets/hookway.wasm internal/editor/assets/wasm_exec.js cmd/hookway-wasm/harness/
	@echo "Serving harness on http://localhost:8091 (Ctrl-C to stop)"
	cd cmd/hookway-wasm/harness && python3 -m http.server 8091

web-install:
	cd web && npm install

web-dev:
	cd web && npm run dev

web-test:
	cd web && npm run test

web-build:
	cd web && npm run build

# ui assembles the embed bundle: build the WASM validator (Milestone 1) and the
# SPA, then stage both into internal/editor/webui/ for go:embed.
ui: wasm web-build
	mkdir -p internal/editor/webui
	rm -rf internal/editor/webui/assets
	cp -R web/dist/. internal/editor/webui/
	cp internal/editor/assets/hookway.wasm internal/editor/assets/wasm_exec.js internal/editor/webui/

# ui-check fails if the embed dir still holds the "not built" placeholder,
# guarding against shipping a binary without the real UI.
ui-check:
	@if grep -q "Hookway Config UI is not built" internal/editor/webui/index.html 2>/dev/null || [ ! -d internal/editor/webui/assets ]; then \
		echo "error: config UI not staged — run 'make ui' before building a release"; exit 1; \
	fi

test:
	$(GO) test ./... -run '^(Test|Example)' -skip 'RabbitMQ|AWS|Redis|Postgres|Mongo|Kafka|ReplayStore'

test-race:
	$(GO) test -race ./... -run '^(Test|Example)' -skip 'RabbitMQ|AWS|Redis|Postgres|Mongo|Kafka|ReplayStore'

integration-test:
	$(GO) test ./internal/queue -run TestRabbitMQ -count=1

aws-integration-test:
	$(GO) test ./internal/queue -run 'TestAWS' -count=1

redis-integration-test:
	$(GO) test ./internal/queue -run 'TestRedis' -count=1

postgres-integration-test:
	$(GO) test ./internal/queue -run 'TestPostgres' -count=1

mongodb-integration-test:
	$(GO) test ./internal/queue -run 'TestMongo' -count=1

kafka-integration-test:
	$(GO) test ./internal/queue -run 'TestKafka' -count=1

webhook-integration-test:
	$(GO) test ./internal/queue -run 'TestWebhookIntegration' -count=1

replaystore-integration-test:
	$(GO) test ./internal/replaystore -run 'TestReplayStore' -count=1

# test-all runs every test once: unit + RabbitMQ + AWS + Redis integration suites.
# Integration tests self-skip when Docker is unavailable. -count=1 disables the
# test cache so the container-backed suites actually run.
test-all:
	$(GO) test ./... -count=1

lint:
	$(GO) vet ./...

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
		REPLAY_DB_URL=postgres://localhost:5432/db \
		REPLAY_API_KEY=dummy; \
	for f in examples/config.yaml examples/filters.yaml examples/replay.yaml examples/destinations/*.yaml examples/auth/*.yaml; do \
		echo "validating $$f"; \
		$(GO) run ./cmd/hookway validate --config $$f || exit 1; \
	done

run:
	HOOKWAY_CONFIG=$(CONFIG) $(GO) run ./cmd/hookway

docker-build: ui
	docker build --build-arg VERSION=$(VERSION) --build-arg COMMIT=$(COMMIT) -t $(IMAGE) .

vuln-scan:
	docker run --rm -v /var/run/docker.sock:/var/run/docker.sock aquasec/trivy:latest image --exit-code 1 --severity HIGH,CRITICAL $(IMAGE)

release-check: validate-examples test test-race integration-test aws-integration-test redis-integration-test postgres-integration-test mongodb-integration-test kafka-integration-test webhook-integration-test replaystore-integration-test lint ui-check docker-build vuln-scan

dev-up:
	docker compose -f examples/docker-compose.yaml up --build

dev-down:
	docker compose -f examples/docker-compose.yaml down
