FROM golang:1.25-alpine AS build
WORKDIR /src
RUN apk add --no-cache ca-certificates
COPY go.mod go.sum* ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -o /out/hookway ./cmd/hookway

FROM scratch
USER 65532:65532
# scratch ships no trust store; without this, TLS to an amqps:// broker (e.g.
# CloudAMQP) fails with "x509: certificate signed by unknown authority" even
# for publicly-valid certs. Go reads this bundle automatically on Linux.
COPY --from=build /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/ca-certificates.crt
COPY --from=build /out/hookway /usr/local/bin/hookway
HEALTHCHECK --interval=30s --timeout=5s --start-period=10s --retries=3 CMD ["/usr/local/bin/hookway", "healthcheck"]
ENTRYPOINT ["/usr/local/bin/hookway"]
