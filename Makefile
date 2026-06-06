.PHONY: test test-race integration-test lint validate run docker-build vuln-scan release-check dev-up dev-down

GO ?= go
CONFIG ?= examples/config.yaml
IMAGE ?= hookway:local

test:
	$(GO) test ./... -run '^(Test|Example)' -skip RabbitMQ

test-race:
	$(GO) test -race ./... -run '^(Test|Example)' -skip RabbitMQ

integration-test:
	$(GO) test ./internal/queue -run TestRabbitMQ -count=1

lint:
	$(GO) vet ./...

validate:
	$(GO) run ./cmd/hookway validate --config $(CONFIG)

run:
	HOOKWAY_CONFIG=$(CONFIG) $(GO) run ./cmd/hookway

docker-build:
	docker build -t $(IMAGE) .

vuln-scan:
	docker run --rm -v /var/run/docker.sock:/var/run/docker.sock aquasec/trivy:latest image --exit-code 1 --severity HIGH,CRITICAL $(IMAGE)

release-check: test test-race integration-test lint validate docker-build vuln-scan

dev-up:
	docker compose -f examples/docker-compose.yaml up --build

dev-down:
	docker compose -f examples/docker-compose.yaml down
