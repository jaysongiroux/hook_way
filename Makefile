.PHONY: test test-all test-race integration-test aws-integration-test redis-integration-test postgres-integration-test mongodb-integration-test kafka-integration-test webhook-integration-test lint validate validate-examples run docker-build vuln-scan release-check dev-up dev-down

GO ?= go
CONFIG ?= examples/config.yaml
IMAGE ?= hookway:local

test:
	$(GO) test ./... -run '^(Test|Example)' -skip 'RabbitMQ|AWS|Redis|Postgres|Mongo|Kafka'

test-race:
	$(GO) test -race ./... -run '^(Test|Example)' -skip 'RabbitMQ|AWS|Redis|Postgres|Mongo|Kafka'

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
		WEBHOOK_API_KEY=dummy; \
	for f in examples/config.yaml examples/destinations/*.yaml examples/auth/*.yaml; do \
		echo "validating $$f"; \
		$(GO) run ./cmd/hookway validate --config $$f || exit 1; \
	done

run:
	HOOKWAY_CONFIG=$(CONFIG) $(GO) run ./cmd/hookway

docker-build:
	docker build -t $(IMAGE) .

vuln-scan:
	docker run --rm -v /var/run/docker.sock:/var/run/docker.sock aquasec/trivy:latest image --exit-code 1 --severity HIGH,CRITICAL $(IMAGE)

release-check: validate-examples test test-race integration-test aws-integration-test redis-integration-test postgres-integration-test mongodb-integration-test kafka-integration-test webhook-integration-test lint docker-build vuln-scan

dev-up:
	docker compose -f examples/docker-compose.yaml up --build

dev-down:
	docker compose -f examples/docker-compose.yaml down
