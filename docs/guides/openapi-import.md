# Import OpenAPI operations as MCP tools

Use `hookway import-openapi` to generate a new Hookway configuration from selected
API operations. The importer runs locally, makes no network requests, does not
expand environment variables, and never loads credential secrets. Generated tools
run through the existing [MCP gateway](mcp-gateway.md).

## Inspect, select, generate

```sh
hookway import-openapi --spec examples/openapi/api.yaml --list
hookway import-openapi --spec examples/openapi/api.yaml --list --json
hookway import-openapi --spec examples/openapi/api.yaml \
  --settings examples/openapi/settings.yaml --out core.yaml
hookway validate --config core.yaml --no-env --strict
```

Build the CLI with `make build`, or substitute `go run ./cmd/hookway` for `hookway`.
Review the generated configuration, provision its referenced secrets, then deploy
it using your normal process. The importer neither deploys nor merges into another
configuration. `--out` creates a new file and refuses to overwrite an existing one;
without it, only YAML is written to stdout. Diagnostics go to stderr. A selected
unsupported operation fails generation without partial output. Inspection reports
unsupported operations without requiring grants or upstream credentials.

The complete [source API](../../examples/openapi/api.yaml),
[import settings](../../examples/openapi/settings.yaml), and
[generated configuration](../../examples/openapi/core.yaml) demonstrate a read tool
and an explicitly authorized write tool. The upstream URL is illustrative.

## Import settings

| Field | Meaning |
|---|---|
| `server` | Hookway server settings. Omitted address defaults to loopback; request-size and other server validation still apply. |
| `endpoint` | MCP `path`, `public_url`, `auth`, `allowed_origins`, and `max_inflight`. Uses existing API-key/OAuth configuration; anonymous endpoints are refused. |
| `upstream.base_url` | Required literal HTTP(S) URL, including any base path. OpenAPI server URLs are not selected automatically. |
| `upstream.allow_private_targets` | Explicitly permit internal upstreams. Off by default. Credential profiles need their own matching permission. |
| `credentials` | Existing [outbound credential profiles](ingress-adapters.md), containing secret references. |
| `operations` | Explicit list of `method`, `path`, optional `name`, `description`, `credential`, and required `access` grants. |

Operation methods are uppercase. GET is read-only; other methods require
`access.allow_mutation: true`. Every tool needs explicit API-key or OAuth grants.
No tool retries automatically. An API's security declaration does not grant agent
access: bind its supported security requirement to an explicit credential profile.
Operator-configured profiles must supply the appropriate credential material and
upstream scopes. MCP caller credentials are never forwarded as upstream credentials.

The importer uses `operationId` for a proposed tool name, falling back to
method/path. Names are normalized to Hookway's limits; collisions require an
explicit name override. Descriptions come from the settings, API summary,
API description, or method/path. Parameter aliases are reported when names need
normalization. No AI is involved in generation.

## Supported documents

OpenAPI 3.0.x and 3.1.x, JSON or YAML, up to 8 MiB. Same-document JSON Pointer
references are supported, including recursive schemas. Structural and schema-size
limits apply. External references, multiple documents, and YAML aliases/anchors
are refused. Bundle external references before importing.

Supported operations are GET, POST, PUT, PATCH, and DELETE. Path-level parameters
are inherited and may be overridden by an operation. Parameters must be non-null
scalars: path/header `simple` and query `form` serialization. JSON bodies can be
required or optional. Content must use exactly one `application/json` media type
with a schema. Explicit numeric successful responses may contain JSON or be empty;
status ranges and a default-only success contract are insufficient.

Swagger 2, multipart/form bodies, cookie parameters, array/object parameter
serialization, callbacks, webhooks, streaming, custom schema dialects, and schema
keywords that cannot be preserved are unsupported. Diagnostics identify the
operation and document location. Unsupported unselected operations do not prevent
importing supported operations.

## Agent arguments and results

Arguments are grouped by location:

```json
{"path":{"id":"customer-1"},"query":{"details":false}}
```

A write adds a separate `body` argument. Omitted optional parameters are omitted
upstream; empty strings, zero, and false are preserved. Schema defaults are not
inserted. Imported dollar signs use YAML Unicode escapes so server startup cannot
reinterpret literal descriptions or schema values as environment references.
An omitted optional body sends no bytes; an explicit JSON null sends
`null`. Bodyless operations use `http_request.body: none`.

All generated results are objects: `{"data": ...}`. `data` preserves the API's
object, array, or scalar JSON. It is null for declared empty success responses.
Invalid JSON, undeclared empty responses, non-success statuses, and output-schema
violations produce tool errors. Cancellation or a lost response after a write can
mean an unknown outcome; inspect the upstream before attempting another mutation.

The generated graph uses native `mcp_trigger → http_request → mcp_result`
branches with capture disabled. It can be reviewed and edited in the existing
builder. `internal/openapiimport` also exposes pure `Inspect`, `DecodeSettings`,
and `Generate` entry points shared by the CLI and browser wizard.

## Builder wizard

In the builder, choose **Config → Create MCP tools from OpenAPI**.
Upload one JSON/YAML file or paste its contents.
Inspection and generation run locally in a dedicated Go WASM worker. The wizard
does not upload the document, fetch remote specifications, or resolve secrets.

Select up to 64 supported operations. Expand **Edit tool details** to change a
tool's name or description. Access setup separates **Your API**, **Agent access**,
and **Tool permissions**. Add named callers using secret references, then grant
each caller the tools it needs. API credentials, browser origins, and request
limits are available in expandable sections. The generated configuration uses the importer’s default server settings;
edit runtime settings separately in **Config → Core server** after opening it in
the builder, or in the downloaded YAML.
Nothing is selected or granted automatically. Writes require a separate mutation
permission. Unsupported operations remain visible with their diagnostics.

**Review configuration** validates the complete configuration. Expand a tool to
review its description, schemas, aliases, and grants; **View core.yaml** shows the
complete YAML. Download `core.yaml` to use it with Hookway. **Open in
builder** offers export of your current workspace and requires confirmation before
replacing all flows and settings, including replay. Imported strings retain their
literal dollar signs through editing, browser persistence, and split YAML exports.
Ordinary authored environment references remain supported.

The wizard draft is held in memory only and is discarded when you close it.
Settings-file import/export and configuration merging are not available. If a
schema contains numbers the builder cannot represent precisely, direct download
remains available and the current workspace is preserved.

After deploying the downloaded configuration and provisioning its referenced
secrets, use **Connect & test** against that running MCP endpoint. This executes
live tools; mutation calls require acknowledgment. The discovered catalog comes
from the deployment and may differ from the wizard draft. Caller tokens stay in
memory and are cleared on disconnect; they are never added to the configuration.
Browser connections require a reachable URL and an allowed browser origin; native
clients can connect where browser network policy prevents access.

If matching WASM assets cannot load, retry inspection or use the CLI above. There
is no server-side processing fallback.
