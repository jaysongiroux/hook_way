# Keep the pinned Go toolchain aligned with go.mod. Go 1.27.3 includes
# the standard library security fixes reported by govulncheck in October 2026.
# Update both pins when newer patches ship.
FROM --platform=$BUILDPLATFORM golang:1.27.3-alpine@sha256:f2c872bcee949b7be9c490efd02b48a8a2fbf947cdf533f1c58525ec9d8170fd AS source
WORKDIR /src
# The pinned official Go image already includes the CA trust store.
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
ARG TARGETARCH
RUN CGO_ENABLED=0 GOOS=linux GOARCH=$TARGETARCH go build -ldflags "-X main.version=${VERSION} -X main.commit=${COMMIT}" -o /out/hookway ./cmd/hookway

FROM source AS build-replay
ARG VERSION=dev
ARG COMMIT=none
ARG TARGETARCH
RUN CGO_ENABLED=0 GOOS=linux GOARCH=$TARGETARCH go build -ldflags "-X main.version=${VERSION} -X main.commit=${COMMIT}" -o /out/hookway-replay ./cmd/hookway-replay

# The optional replay service (docs/operations/replay-service.md): the failed-delivery
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
