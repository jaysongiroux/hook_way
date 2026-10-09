# Build from the repository root: docker build -f web/Dockerfile .
# WASM and wasm_exec.js always come from the same pinned Go toolchain.
FROM --platform=$BUILDPLATFORM golang:1.26.9-alpine@sha256:cdfd4fe2da6b225d8b40c6b7a105736e548e83ff56d5d8f9394446eeb5eb84e0 AS wasm
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN mkdir -p /out && GOOS=js GOARCH=wasm go build -o /out/hookway.wasm ./cmd/hookway-wasm && cp "$(go env GOROOT)/lib/wasm/wasm_exec.js" /out/wasm_exec.js

FROM --platform=$BUILDPLATFORM node:22-alpine@sha256:0a7108bf6c7bf5de370ffb1a3ed6be93d405b43ff159f681a8d18c0e2bc2e402 AS ui
WORKDIR /web
COPY web/package.json web/package-lock.json ./
RUN npm ci
COPY web/ ./
# Builder examples are imported as raw YAML from outside web/.
COPY examples/templates/ /examples/templates/
COPY examples/showcase/ /examples/showcase/
COPY --from=wasm /out/hookway.wasm /out/wasm_exec.js ./public/
ARG VERSION=dev
ARG COMMIT=none
RUN node -e 'require("node:fs").writeFileSync("public/build-info.json", JSON.stringify({version:process.env.VERSION,commit:process.env.COMMIT})+"\n")'
RUN npm run build:console && test -s dist-console/hookway.wasm && test -s dist-console/wasm_exec.js

FROM nginxinc/nginx-unprivileged:stable-alpine@sha256:15c994d10d6d78658721c3bcafff14cb281fba2a4bdf9d5ba92c416a472516e3 AS console
USER root
# Python is used only at startup for strict URL validation/JSON serialization,
# and by the local health check. Nginx serves all HTTP requests.
RUN apk add --no-cache python3
COPY --from=ui /web/dist-console/ /usr/share/nginx/html/
COPY web/runtime_config.py /opt/hookway/runtime_config.py
USER 101:101
EXPOSE 8080
HEALTHCHECK --interval=30s --timeout=5s --start-period=5s --retries=3 CMD ["python3", "-c", "import urllib.request; urllib.request.urlopen('http://127.0.0.1:8080/healthz', timeout=3).read()"]
CMD ["sh", "-c", "python3 /opt/hookway/runtime_config.py && exec nginx -c /tmp/hookway-ui/nginx.conf -g 'daemon off;'"]
