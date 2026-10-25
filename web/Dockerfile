# Build from the repository root: docker build -f web/Dockerfile .
# WASM and wasm_exec.js always come from the same pinned Go toolchain.
FROM --platform=$BUILDPLATFORM golang:1.27.3-alpine@sha256:f2c872bcee949b7be9c490efd02b48a8a2fbf947cdf533f1c58525ec9d8170fd AS wasm
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN mkdir -p /out && GOOS=js GOARCH=wasm go build -o /out/hookway.wasm ./cmd/hookway-wasm && cp "$(go env GOROOT)/lib/wasm/wasm_exec.js" /out/wasm_exec.js

FROM --platform=$BUILDPLATFORM node:26.12.0-alpine@sha256:6f08a523443ddedf52941874c203475b0ad503e859ad70cb26ab8be9a517dbd5 AS ui
WORKDIR /web
COPY web/package.json web/package-lock.json ./
RUN npm ci
COPY web/ ./
# Builder examples are imported as raw YAML from outside web/.
COPY examples/ /examples/
COPY --from=wasm /out/hookway.wasm /out/wasm_exec.js ./public/
ARG VERSION=dev
ARG COMMIT=none
RUN node -e 'require("node:fs").writeFileSync("public/build-info.json", JSON.stringify({version:process.env.VERSION,commit:process.env.COMMIT})+"\n")'
RUN npm run build:console && test -s dist-console/hookway.wasm && test -s dist-console/wasm_exec.js

FROM nginxinc/nginx-unprivileged:1.31.7-alpine@sha256:b5047b9b76845eac124358373c6852377c52e4b05834a47647e955dd91cb8f1e AS console
USER root
# Python is used only at startup for strict URL validation/JSON serialization,
# and by the local health check. Nginx serves all HTTP requests.
# Upgrade the inherited TIFF library to include the CVE-2026-4775 fix.
# Require patched Expat (CVE-2026-77214) and X11 (CVE-2026-88806) libraries.
# Require patched Brotli (CVE-2026-111497) inherited from the Nginx image.
RUN apk add --no-cache python3 'tiff>=4.7.2-r0' \
    'libexpat>=2.9.0-r0' 'libx11>=1.8.14-r0' 'brotli-libs>=1.2.1-r0'
COPY --from=ui /web/dist-console/ /usr/share/nginx/html/
COPY web/runtime_config.py /opt/hookway/runtime_config.py
USER 101:101
EXPOSE 8080
HEALTHCHECK --interval=30s --timeout=5s --start-period=5s --retries=3 CMD ["python3", "-c", "import urllib.request; urllib.request.urlopen('http://127.0.0.1:8080/healthz', timeout=3).read()"]
CMD ["sh", "-c", "python3 /opt/hookway/runtime_config.py && exec nginx -c /tmp/hookway-ui/nginx.conf -g 'daemon off;'"]
