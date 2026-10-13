# Validate JSON

`json_validate` checks an event body against a JSON Schema before the flow
delivers it. It verifies shape and values; it does not serialize a Go object
or generate application types. Place it after a JSON `envelope` and before
the nodes that depend on the checked fields.

The builder calls the same Go validator through WASM when you test a sample.
The editor API exposes the same operation for connected clients. Validation
does not fetch schemas or initialize databases, brokers, or credentials.

## Configuration

```yaml
- id: check
  type: json_validate
  schema:
    type: object
    required: [order_id, customer]
    additionalProperties: false
    properties:
      order_id: {type: integer, minimum: 1}
      customer:
        type: object
        required: [email]
        properties:
          email: {type: string, format: email}
        additionalProperties: false
```

The top-level `schema` must be a nonempty object. Nested boolean schemas,
objects, arrays, nullable unions, required properties, numeric constraints,
and format constraints follow the selected JSON Schema draft. Draft 7 is
the default. An explicit `$schema` may select Draft 4, 6, 7, 2019-09, or
2020-12. Local `$ref` references work, including definitions in the same
schema; references requiring another resource are rejected at configuration
validation. Known formats are checked even in drafts where they are normally
annotations.

Required fields must be listed in `required`. Writing a field under
`properties` alone does not make it required. Additional fields are allowed
unless the schema constrains them, for example with `additionalProperties:
false`. A `default` annotation does not insert missing values. Validation
does not strip extra fields or turn strings into numbers automatically.

See [the complete runnable example](../../examples/json-validation.yaml) for
local references and an HTTP response. Its `no_auth` node is for demonstration;
use an appropriate authentication node on a production endpoint.

## Explicit conversions

```yaml
  coercions:
    - {path: order_id, type: integer}
```

A conversion applies before validation, only at the listed dotted property
path. Each path must resolve through literal `properties` entries, and the
property must declare exactly one `type` matching the conversion. Paths
through `$ref`, arrays, union types, or `anyOf` do not authorize conversion.
Conversion paths are limited to 256 characters and 64 entries.

The existing scalar conversion rules are retained:

| Target | Accepted conversions |
| --- | --- |
| `string` | Strings stay strings; numbers become exact decimal text; booleans become `"true"` or `"false"`; null becomes an empty string. |
| `number` / `integer` | Bounded numeric strings, including supported hexadecimal float syntax, become exact numbers. Null becomes zero and booleans become zero or one. Integers require an exact integral value. |
| `boolean` | Booleans stay booleans; strings `"true"` and `"false"`, JSON number literals `1` and `0`, and null convert. Null becomes false. |
| `null` | Null, an empty string, false, and an exact numeric zero become null. |

Missing fields are not created. If a value cannot be converted, it retains
its original value and the schema decides whether it is valid. Objects and
arrays are not scalar conversions. For example, `"42"` can become an integer;
`"4.2"` cannot become an integer. No precision is lost when converting
`"9007199254740993"` to an integer.

## Failures and precision

Invalid JSON, a non-JSON envelope, schema mismatch, or a body limit violation
returns HTTP 400 by default. A normal success edge carries the validated
envelope. Wire `check:on_error>rejected` to use the existing error recovery
path, for example an explicit 422 response. The failure path retains the
original input. Neither validation nor conversion mutates another branch's
input or the raw request bytes.

JSON envelope decoding, graph cloning, validation, and explicit numeric
conversions preserve exact numeric values. Validation without conversions
also preserves the parsed values and number representations. Upstream
JavaScript or other transformations can change numeric values independently;
place validation before those steps when the original numeric value matters.

The builder stores schemas as JavaScript objects. It rejects schema numeric
literals that would change when parsed and exported, including unsafe large
integers and high-precision decimals. It also rejects ambiguous leading-zero
YAML numbers and numeric YAML syntax its parser would interpret as a string.
Use ordinary decimal notation for builder-authored schemas. For constraints
that JavaScript cannot retain exactly, author and validate runtime YAML
directly. Rejected imports leave the current graph unchanged. Invalid schema
drafts remain editable and block export until corrected or reverted.

Samples and validated output travel as JSON text, so their large integers
and decimals remain exact in the builder. The sample is retained in local
editor drafts and is excluded from exported configuration. Editing the schema,
sample, or conversions clears the previous result; late responses cannot
overwrite a newer edit.

## Limits and API contract

| Input or output | Limit |
| --- | --- |
| Encoded schema | 256 KiB, depth 64, 10,000 nodes |
| Runtime body presented to validation | 20 MiB, depth 128, 1,000,000 nodes; ingress may impose a lower body limit |
| Numeric work | 4,096 characters of numeric input and exponent magnitude at most 4,096 |
| Validation work | Expanded schema cost at most 10,000; estimated node work at most 1,000,000 and text/digit work at most 32 MiB, including conservative uniqueness comparisons |
| Sample text | 1 MiB |
| Sample request envelope | 8 MiB, allowing JSON escaping |
| Sample output | 2 MiB |
| Reported sample issues | At most 64 |

`POST /json-sample-validation` takes `schema`, optional `coercions`, and a
`sample` string. A schema/sample validation result is HTTP 200 with `valid`,
an `issues` array, and an `output` JSON string on success. Malformed request
envelopes return 400; an oversized request returns 413. The route uses the
editor's existing authentication and CORS rules. The generated
[editor API specification](../api/editor.openapi.json) defines these DTOs.

WASM capability `json_sample_validate` invokes `hookwayJSONSampleValidate`
with the same input and result. The public static builder uses WASM. The Go-hosted editor can fall back to its
editor API. An unavailable validator is shown explicitly; it does not count as
a successful sample check.
Issue paths are JSON pointers and messages identify constraints without
echoing submitted values. Property names may appear in paths.

The work estimate counts every possible schema branch, regular-expression
length, body strings/property names/numeric digits, and recursive references
through the supported body depth. It is deliberately conservative:
large schema/body combinations may be rejected even when they fit the byte and
structure limits. Repeated references cannot bypass it to generate an
exponentially large validation/error tree.

## Existing configurations

Existing `transform` nodes with `kind: json_schema`, `json_schema`, and
`coercions` use the same engine and builder controls. No migration is required.
Runtime construction propagates schema compilation errors.

Try valid and invalid samples in the builder, then export the resulting YAML.
Validate the runnable example with
`go run ./cmd/hookway validate --config examples/json-validation.yaml`.
