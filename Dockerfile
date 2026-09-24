# Pin a patched Go toolchain. Trivy flags every stdlib CVE stamped into the
# built binary regardless of reachability, so the pin must track the latest
# 1.25.x patch. 1.25.13 clears the HIGH stdlib CVEs Trivy reported against
# 1.25.12 (CVE-2026-33818, -39821, -56853, -56858, -56859, -56860, -56862);
# 1.25.12 had in turn fixed crypto/tls (CVE-2026-42505), os
# (CVE-2026-39822/27139), net/url (CVE-2026-25679) and html/template
# (CVE-2026-27142). Bump this pin as newer patches ship.
FROM golang:1.25.13-alpine AS source
WORKDIR /src
RUN apk add --no-cache ca-certificates
COPY go.mod go.sum* ./
RUN go mod download
COPY . .

# VERSION/COMMIT default to the same "dev"/"none" sentinels as both binaries'
# package-level vars, so an image built without --build-arg (e.g. a local
# `docker build .`) still produces a working binary. The Makefile's
# docker-build target passes both from `git describe`/`git rev-parse`;
# release.yml's build-push steps pass them from the CI checkout.
FROM source AS build-core
ARG VERSION=dev
ARG COMMIT=none
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags "-X main.version=${VERSION} -X main.commit=${COMMIT}" -o /out/hookway ./cmd/hookway

FROM source AS build-replay
ARG VERSION=dev
ARG COMMIT=none
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags "-X main.version=${VERSION} -X main.commit=${COMMIT}" -o /out/hookway-replay ./cmd/hookway-replay

# The optional replay service (docs/replay-service.md): the failed-delivery
# store, its API, and the redrive workers. Build with --target replay.
FROM scratch AS replay
USER 65532:65532
COPY --from=source /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/ca-certificates.crt
COPY --from=build-replay /out/hookway-replay /usr/local/bin/hookway-replay
HEALTHCHECK --interval=30s --timeout=5s --start-period=10s --retries=3 CMD ["/usr/local/bin/hookway-replay", "healthcheck"]
ENTRYPOINT ["/usr/local/bin/hookway-replay"]
CMD ["serve"]

# Core. It stays the last stage, so a plain `docker build .` builds it.
FROM scratch AS core
USER 65532:65532
# scratch ships no trust store; without this, TLS to an amqps:// broker (e.g.
# CloudAMQP) fails with "x509: certificate signed by unknown authority" even
# for publicly-valid certs. Go reads this bundle automatically on Linux.
COPY --from=source /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/ca-certificates.crt
COPY --from=build-core /out/hookway /usr/local/bin/hookway
HEALTHCHECK --interval=30s --timeout=5s --start-period=10s --retries=3 CMD ["/usr/local/bin/hookway", "healthcheck"]
ENTRYPOINT ["/usr/local/bin/hookway"]
