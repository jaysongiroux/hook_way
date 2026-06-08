FROM golang:1.25-alpine AS build
WORKDIR /src
COPY go.mod go.sum* ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -o /out/hookway ./cmd/hookway

FROM scratch
USER 65532:65532
COPY --from=build /out/hookway /usr/local/bin/hookway
HEALTHCHECK --interval=30s --timeout=5s --start-period=10s --retries=3 CMD ["/usr/local/bin/hookway", "healthcheck"]
ENTRYPOINT ["/usr/local/bin/hookway"]
