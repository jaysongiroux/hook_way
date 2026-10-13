import { Alert, Modal, Select, Stack, Text } from "@mantine/core";
import { SchemaForm } from "./SchemaForm";
import {
  isSchemaObject,
  schemaProperties,
  type JSONSchema,
} from "./nodeSchema";
import { useGraphStore } from "./store";
import { record, routingNameError, WorkspaceEntries } from "./WorkspaceEntries";
import catalog from "./workspaceSchema.json";

const kinds = ["http", "postgres", "dynamodb"];
const lookupMap = (catalog as Record<string, JSONSchema>).lookups;
const schema = isSchemaObject(lookupMap)
  ? (lookupMap.additionalProperties as JSONSchema)
  : false;
const properties = schemaProperties(schema);
const help: Record<string, string> = {
  timeout:
    "Total lookup timeout, including cache misses. Omitted uses 2s; maximum 30s.",
  ttl: "How long to cache successful results in this process. Omitted uses 5m.",
  negative_ttl:
    "How long to cache a missing result. Omitted or 0 disables this cache.",
  max_entries:
    "Maximum cached keys per provider, per process. Omitted uses 10,000.",
  url: "HTTP URL with exactly one {key} in its path or query, or PostgreSQL connection string. Use ${ENV_NAME} for credentials; never paste secrets here.",
  query:
    "Read-only SQL query with $1 for the lookup key. Return one column that can be read as a string.",
  value_path:
    "Dotted JSON path to a string in the HTTP response, such as data.customer_id.",
  allow_private_targets:
    "Allow trusted internal HTTP endpoints. Off by default; redirects are never followed.",
  secret_headers:
    "Header values resolved from secret references. Use prefix for text such as Bearer followed by a space.",
  headers: "Nonsecret HTTP headers only. Put credentials in Secret headers.",
  name: "Environment variable, file path or AWS Secrets Manager identifier. Enter a reference, never the secret value.",
  table:
    "Existing DynamoDB table to query. AWS credentials come from the server's standard AWS credential chain.",
  endpoint:
    "Optional trusted DynamoDB endpoint override for local tests or a compatible service.",
};

export function LookupsPanel({
  opened,
  onClose,
}: {
  opened: boolean;
  onClose: () => void;
}) {
  const value = useGraphStore((s) => s.configExtra.lookups);
  const setConfigExtra = useGraphStore((s) => s.setConfigExtra);
  return (
    <Modal
      opened={opened}
      onClose={onClose}
      title="Lookup providers"
      size="xl"
      closeButtonProps={{ "aria-label": "Close Lookup providers" }}
    >
      <Stack gap="md">
        <Text size="sm">
          Use a request value to fetch a string from an API or database. Nodes
          reference these providers by name. Connections and secrets are
          resolved by the server; the builder does not contact these services.
        </Text>
        <WorkspaceEntries
          label="lookup provider"
          value={value}
          initial={() => ({ http: { url: "", value_path: "" } })}
          nameError={routingNameError}
          onChange={(lookups) => setConfigExtra({ lookups })}
        >
          {(name, entry, change) => {
            const selected = kinds.filter((kind) => entry[kind] !== undefined);
            const kind = selected.length === 1 ? selected[0] : "";
            const visible = ["timeout", "cache", ...(kind ? [kind] : selected)];
            const unknown = Object.keys(entry).filter(
              (key) => !properties[key],
            );
            return (
              <Stack gap="sm">
                <Select
                  label="Lookup backend"
                  value={kind || null}
                  allowDeselect={false}
                  data={[
                    { value: "http", label: "HTTP API" },
                    { value: "postgres", label: "PostgreSQL" },
                    { value: "dynamodb", label: "DynamoDB" },
                  ]}
                  description="Changing backend removes its connection settings. Shared cache and timeout settings are retained."
                  onChange={(next) => {
                    if (!next || next === kind) return;
                    change({
                      ...Object.fromEntries(
                        Object.entries(entry).filter(
                          ([key]) => !kinds.includes(key),
                        ),
                      ),
                      [next]: {},
                    });
                  }}
                />
                {!kind && (
                  <Alert color="orange">
                    Select exactly one backend. Imported settings are retained
                    until you choose one.
                  </Alert>
                )}
                {unknown.length > 0 && (
                  <Alert color="orange">
                    Unrecognized settings are retained: {unknown.join(", ")}.
                    Review the exported YAML.
                  </Alert>
                )}
                <SchemaForm
                  schema={{
                    type: "object",
                    properties: Object.fromEntries(
                      visible.map((key) => [key, properties[key]]),
                    ),
                    required: kind ? [kind] : [],
                  }}
                  value={entry}
                  onChange={(next) => change(record(next))}
                  scope={`lookups/${encodeURIComponent(name)}`}
                  path={`lookups.${name}`}
                  order={[kind, "timeout", "cache"]}
                  defaults={{
                    timeout: "2s",
                    "cache.ttl": "5m",
                    "cache.max_entries": 10000,
                  }}
                  enums={{
                    method: ["GET", "POST"],
                    provider: ["env", "file", "aws_secrets_manager"],
                  }}
                  labels={{
                    timeout: "Lookup timeout",
                    cache: "Result cache",
                    ttl: "Result lifetime",
                    negative_ttl: "Missing-result lifetime",
                    http: "HTTP API",
                    postgres: "PostgreSQL",
                    dynamodb: "DynamoDB",
                    method: "HTTP method",
                    query: "SQL query",
                    allow_private_targets: "Allow private targets",
                    table: "Table name",
                    index: "Index name",
                    key_attribute: "Key attribute",
                    value_attribute: "Result attribute",
                    page_limit: "Maximum query pages",
                    url: kind === "postgres" ? "Database URL" : "Request URL",
                    value_path: "Result JSON path",
                    secret_headers: "Secret headers",
                    headers: "Nonsecret headers",
                    max_entries: "Maximum cached keys",
                  }}
                  fieldHelp={(path) =>
                    path.endsWith(".url")
                      ? kind === "postgres"
                        ? "PostgreSQL connection string. Use ${ENV_NAME} for the DSN or credentials; never paste secrets here."
                        : "HTTP URL with exactly one {key} in its path or query. Put credentials in Secret headers."
                      : (help[path] ?? help[path.split(".").at(-1) ?? path])
                  }
                />
              </Stack>
            );
          }}
        </WorkspaceEntries>
      </Stack>
    </Modal>
  );
}
