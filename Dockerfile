FROM golang:1.25-alpine AS build
WORKDIR /src
COPY go.mod go.sum* ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -o /out/hookway ./cmd/hookway

FROM alpine:3.22
RUN adduser -D -H hookway
USER hookway
COPY --from=build /out/hookway /usr/local/bin/hookway
HEALTHCHECK --interval=30s --timeout=5s --start-period=10s --retries=3 CMD wget -qO- http://127.0.0.1:8080/healthz >/dev/null || exit 1
ENTRYPOINT ["hookway"]
