import { Alert, Select, Stack, Text } from "@mantine/core";
import { NODE_SCHEMAS, schemaProperties, type JSONSchema } from "./nodeSchema";
import { nodeFieldHelp } from "./help";
import { SchemaForm } from "./SchemaForm";
import {
  changeMode,
  Fields,
  InactiveSettings,
  object,
  type OptionsProps,
} from "./FormPresentation";

const REQUEST_FIELDS = [
  "timestamp_header",
  "timestamp_unit",
  "timestamp_policy",
  "timestamp_tolerance",
  "body_representation",
  "signature_label",
];
const JWT_FIELDS = ["expected_issuer", "expected_audience", "body_hash_claim", "tenant_claim"];
const LABELS: Record<string, string> = {
  hmac_sha1_hex: "HMAC SHA-1 · hexadecimal",
  hmac_sha256_ts_colon: "Timestamp + HMAC SHA-256 · labeled header",
  exact: "Exact value",
  basic: "HTTP Basic",
  hmac_sha256_hex: "HMAC SHA-256 · hexadecimal",
  hmac_sha256_base64: "HMAC SHA-256 · Base64",
  hmac_sha512_hex: "HMAC SHA-512 · hexadecimal",
  hmac_sha512_base64: "HMAC SHA-512 · Base64",
  jwt_hs256: "JWT · HS256",
  jwt_rs256: "JWT · RS256",
  jwt_es256: "JWT · ES256",
  hmac_sha256_ts_dot: "Timestamp + HMAC SHA-256",
  hmac_sha256_ts_dot_multi: "Timestamp + multiple signatures",
  hmac_sha256_slack_v0: "Slack · timestamp + HMAC SHA-256",
  hmac_sha256_headers_dot: "Separate timestamp + HMAC SHA-256",
  ecdsa_p256_sha256_headers: "Separate timestamp + ECDSA P-256",
};
export function AuthOptionsForm(props: OptionsProps) {
  const { value, onChange, scope } = props;
  const scheme = String(value.scheme ?? "");
  const capability = NODE_SCHEMAS.auth.auth_schemes?.[scheme];
  const jwt = capability?.jwt ?? scheme.startsWith("jwt_");
  const request = capability?.request_signature ?? false;
  const separate = capability?.separate_timestamp ?? false;
  const keyFormat = capability?.key_format ?? "shared_secret";
  const schemes = Object.keys(NODE_SCHEMAS.auth.auth_schemes ?? {});
  const source = object(value.source);
  const sourceMode = source.kind === "query" ? "query" : "header";
  const secretMode = value.secret_ref !== undefined ? "reference" : "literal";
  const tolerance =
    jwt ||
    request ||
    ["hmac_sha256_ts_colon", "hmac_sha256_ts_dot"].includes(scheme);
  const compatible = [
    ...(tolerance ? ["timestamp_tolerance"] : []),
    ...(jwt ? JWT_FIELDS : []),
    ...(request
      ? REQUEST_FIELDS.filter(
          (k) =>
            (k !== "timestamp_header" || separate) &&
            (k !== "body_representation" ||
              scheme === "hmac_sha256_headers_dot") &&
            (k !== "signature_label" || scheme === "hmac_sha256_ts_dot_multi"),
        )
      : []),
    ...(!request ? ["remove_bearer"] : []),
    ...(scheme === "exact" ? ["remove_prefix"] : []),
  ];
  const inactive = [
    ...REQUEST_FIELDS,
    ...JWT_FIELDS,
    "remove_prefix",
    "remove_bearer",
  ].filter((k) => !compatible.includes(k));
  const selectScheme = (next: string) => {
    const cap = NODE_SCHEMAS.auth.auth_schemes?.[next];
    const keep = [
      ...(cap?.jwt ||
      cap?.request_signature ||
      ["hmac_sha256_ts_colon", "hmac_sha256_ts_dot"].includes(next)
        ? ["timestamp_tolerance"]
        : []),
      ...(cap?.jwt ? JWT_FIELDS : []),
      ...(cap?.request_signature
        ? REQUEST_FIELDS.filter(
            (k) =>
              (k !== "timestamp_header" || cap.separate_timestamp) &&
              (k !== "body_representation" ||
                next === "hmac_sha256_headers_dot") &&
              (k !== "signature_label" || next === "hmac_sha256_ts_dot_multi"),
          )
        : ["remove_bearer"]),
      ...(next === "exact" ? ["remove_prefix"] : []),
    ];
    changeMode(
      value,
      [
        ...REQUEST_FIELDS,
        ...JWT_FIELDS,
        "remove_prefix",
        "remove_bearer",
      ].filter((k) => !keep.includes(k)),
      {
        scheme: next,
        ...(cap?.separate_timestamp && source.kind === "query"
          ? { source: { kind: "header", key: "" } }
          : {}),
      },
      onChange,
      scope,
    );
  };
  const ref = object(value.secret_ref);
  const refBase = schemaProperties(NODE_SCHEMAS.auth.schema).secret_ref;
  const refProperties = schemaProperties(refBase);
  const refSchema: JSONSchema = {
    type: "object",
    properties: Object.fromEntries(
      [
        "name",
        "json_field",
        ...(ref.provider === "aws_secrets_manager" ? ["selection"] : []),
        "refresh",
        "overlap",
      ].map((k) => [k, refProperties[k]]),
    ),
    required: ["name"],
  };
  return (
    <Stack gap="sm">
      <Select
        label="Authentication strategy"
        description="Choose how the sender authenticates each request."
        required
        searchable
        allowDeselect={false}
        value={scheme || null}
        data={[
          ...schemes,
          ...(scheme && !schemes.includes(scheme) ? [scheme] : []),
        ].map((s) => ({ value: s, label: LABELS[s] ?? s.replace(/_/g, " ") }))}
        error={
          scheme && !schemes.includes(scheme)
            ? "Unsupported strategy. Choose a supported strategy."
            : !scheme
              ? "Choose a strategy."
              : undefined
        }
        onChange={(v) => v && selectScheme(v)}
      />
      {jwt && (
        <Fields
          {...props}
          fields={[
            "body_hash_claim",
            "expected_issuer",
            "expected_audience",
            "timestamp_tolerance",
            "tenant_claim",
          ]}
          order={[
            "body_hash_claim",
            "expected_issuer",
            "expected_audience",
            "timestamp_tolerance",
            "tenant_claim",
          ]}
          required={["body_hash_claim"]}
          labels={{
            body_hash_claim: "Raw body SHA-256 claim",
            tenant_claim: "JWT tenant claim",
            timestamp_tolerance: "JWT clock tolerance",
          }}
          fieldHelp={(path) =>
            path === "timestamp_tolerance"
              ? "JWT clock leeway as a Go duration. Omitted uses 5m."
              : nodeFieldHelp("auth", path)
          }
        />
      )}
      <Fields {...props} fields={["credential_id"]}
        labels={{ credential_id: "Stable credential ID" }}
        fieldHelp={() => "Optional label for this authenticated credential, such as partner-acme. Required by verified identity quotas. Keep it stable during secret rotation; never enter the secret itself."} />
      <InactiveSettings
        value={value}
        fields={inactive}
        onRemove={() => changeMode(value, inactive, {}, onChange, scope)}
      />
      {source.kind !== undefined &&
        !["header", "query"].includes(String(source.kind)) && (
          <Alert color="red">
            Imported credential source {String(source.kind)} is unsupported.
            Choose Request header or Query parameter to repair it.
          </Alert>
        )}
      <Select
        label="Credential location"
        value={sourceMode}
        allowDeselect={false}
        data={[
          { value: "header", label: "Request header" },
          ...(!separate ? [{ value: "query", label: "Query parameter" }] : []),
        ]}
        onChange={(v) =>
          changeMode(
            value,
            ["signature_header", "source"],
            {
              source: {
                kind: v ?? "header",
                key: source.key ?? value.signature_header ?? "",
              },
            },
            onChange,
            scope,
          )
        }
      />
      {value.source !== undefined ? (
        <Fields
          {...props}
          fields={["source"]}
          required={["source"]}
          override={{
            source: {
              type: "object",
              properties: { key: { type: "string" } },
              required: ["key"],
              additionalProperties: false,
            },
          }}
          labels={{
            "source.key":
              sourceMode === "query"
                ? "Query parameter name"
                : "Signature header name",
          }}
          optionalBlocks={false}
        />
      ) : (
        <Fields
          {...props}
          fields={["signature_header"]}
          required={["signature_header"]}
          labels={{ signature_header: "Signature header name" }}
        />
      )}
      {value.source !== undefined && value.signature_header !== undefined && (
        <InactiveSettings
          value={value}
          fields={["signature_header"]}
          onRemove={() =>
            changeMode(value, ["signature_header"], {}, onChange, scope)
          }
        />
      )}
      {separate && source.kind === "query" && (
        <Alert color="red">
          This strategy requires distinct signature and timestamp headers.
          Choose request header as the credential location.
        </Alert>
      )}
      <Select
        label="Key source"
        value={secretMode}
        allowDeselect={false}
        data={[
          { value: "reference", label: "Secret reference (recommended)" },
          { value: "literal", label: "Inline key / environment interpolation" },
        ]}
        onChange={(v) =>
          changeMode(
            value,
            ["secret", "secret_ref"],
            v === "reference"
              ? { secret_ref: { provider: "env", name: "" } }
              : { secret: "" },
            onChange,
            scope,
          )
        }
      />
      <Text size="xs" c="dimmed">
        {keyFormat === "pem"
          ? "Use a PEM public key."
          : keyFormat === "base64_der"
            ? "Use a Base64-encoded PKIX DER P-256 public key."
            : scheme === "basic"
              ? "The shared value must be username:password."
              : "Use the shared secret supplied by the sender."}{" "}
        The builder does not fetch secrets.
      </Text>
      {secretMode === "reference" ? (
        <Stack gap="sm">
          <Select
            label="Secret provider"
            required
            value={String(ref.provider ?? "")}
            data={[
              ...["env", "file", "aws_secrets_manager"],
              ...(ref.provider &&
              !["env", "file", "aws_secrets_manager"].includes(
                String(ref.provider),
              )
                ? [String(ref.provider)]
                : []),
            ]}
            error={
              ref.provider &&
              !["env", "file", "aws_secrets_manager"].includes(
                String(ref.provider),
              )
                ? "Unsupported secret provider."
                : !ref.provider
                  ? "Choose a secret provider."
                  : undefined
            }
            allowDeselect={false}
            onChange={(provider) => {
              const next = { ...ref, provider };
              if (provider !== "aws_secrets_manager")
                delete (next as Record<string, unknown>).selection;
              onChange({ ...value, secret_ref: next });
            }}
          />
          <InactiveSettings
            value={ref}
            fields={ref.provider === "aws_secrets_manager" ? [] : ["selection"]}
            onRemove={() => {
              const next = { ...ref };
              delete next.selection;
              onChange({ ...value, secret_ref: next });
            }}
          />
          <SchemaForm
            schema={{
              type: "object",
              properties: { secret_ref: refSchema as JSONSchema },
              required: ["secret_ref"],
            }}
            value={value}
            onChange={(v) => onChange(v as typeof value)}
            scope={scope}
            fieldHelp={(path) => nodeFieldHelp("auth", path)}
            optionalBlocks={false}
            labels={{ secret_ref: "Secret reference" }}
          />
        </Stack>
      ) : (
        <Fields
          {...props}
          fields={["secret"]}
          required={["secret"]}
          secretFields={keyFormat === "shared_secret" ? ["secret"] : []}
          multiline={keyFormat === "pem" ? ["secret"] : []}
          labels={{ secret: "Verification key" }}
        />
      )}
      {value.secret_ref !== undefined && value.secret !== undefined && (
        <InactiveSettings
          value={value}
          fields={["secret"]}
          onRemove={() => changeMode(value, ["secret"], {}, onChange, scope)}
        />
      )}

      {request && (
        <Fields
          {...props}
          fields={compatible.filter((k) => REQUEST_FIELDS.includes(k))}
          required={[
            ...(separate ? ["timestamp_header"] : []),
            "timestamp_unit",
          ]}
          defaults={{
            timestamp_policy: "symmetric",
            timestamp_tolerance: "5m",
            body_representation: "raw",
            signature_label: "s",
          }}
        />
      )}
      {!jwt && !request && tolerance && (
        <Fields
          {...props}
          fields={["timestamp_tolerance"]}
          defaults={{ timestamp_tolerance: "5m" }}
        />
      )}
      <Fields
        {...props}
        fields={compatible.filter((k) =>
          ["remove_bearer", "remove_prefix"].includes(k),
        )}
      />
    </Stack>
  );
}
