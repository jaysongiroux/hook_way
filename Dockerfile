# Pin a patched Go toolchain. Trivy flags every stdlib CVE stamped into the
# built binary regardless of reachability, so the pin must track the latest
# 1.25.x patch. 1.25.12 (2026-07-07) fixes fresh HIGH stdlib CVEs in crypto/tls
# (CVE-2026-42505), os (CVE-2026-39822/27139), net/url (CVE-2026-25679) and
# html/template (CVE-2026-27142) that 1.25.11 still carried. Verified clean of
# stdlib findings with govulncheck against the 1.25.12 toolchain. Bump this pin
# as newer patches ship.
FROM golang:1.25.12-alpine AS build
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
