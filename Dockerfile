# Pin a patched Go toolchain. The floating golang:1.25-alpine tag resolved to
# 1.25.4, whose crypto/tls, crypto/x509, net/http and net/url stdlib carry HIGH
# CVEs (GO-2026-4337/4340/4870/4918/4946/4947/4971/5037/5039, GO-2025-4155/4175,
# GO-2026-4341/4601) that Trivy flags on the built binary. All are fixed by
# 1.25.11 (verified with govulncheck). Bump this pin as newer patches ship.
FROM golang:1.25.11-alpine AS build
WORKDIR /src
RUN apk add --no-cache ca-certificates
COPY go.mod go.sum* ./
RUN go mod download
COPY . .
# VERSION/COMMIT default to the same "dev"/"none" sentinels as
# cmd/hookway/main.go's package-level vars, so an image built without
# --build-arg (e.g. a local `docker build .`) still produces a working binary.
# The Makefile's docker-build target passes both from `git describe`/`git
# rev-parse`; release.yml's build-push step passes them from the CI checkout.
ARG VERSION=dev
ARG COMMIT=none
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags "-X main.version=${VERSION} -X main.commit=${COMMIT}" -o /out/hookway ./cmd/hookway

FROM scratch
USER 65532:65532
# scratch ships no trust store; without this, TLS to an amqps:// broker (e.g.
# CloudAMQP) fails with "x509: certificate signed by unknown authority" even
# for publicly-valid certs. Go reads this bundle automatically on Linux.
COPY --from=build /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/ca-certificates.crt
COPY --from=build /out/hookway /usr/local/bin/hookway
HEALTHCHECK --interval=30s --timeout=5s --start-period=10s --retries=3 CMD ["/usr/local/bin/hookway", "healthcheck"]
ENTRYPOINT ["/usr/local/bin/hookway"]
