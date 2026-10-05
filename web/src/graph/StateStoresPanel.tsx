import { Alert, Modal, Select, Stack, Text } from "@mantine/core";
import { SchemaForm } from "./SchemaForm";
import { isSchemaObject, schemaProperties, type JSONSchema } from "./nodeSchema";
import { useGraphStore } from "./store";
import { record, routingNameError, WorkspaceEntries } from "./WorkspaceEntries";
import catalog from "./workspaceSchema.json";

const backends = ["memory", "redis", "postgres"];
const mapSchema = (catalog as Record<string, JSONSchema>).state_stores;
const storeSchema = isSchemaObject(mapSchema)
  ? (mapSchema.additionalProperties as JSONSchema)
  : false;
const properties = schemaProperties(storeSchema);
const help: Record<string, string> = {
  namespace: "Stable deployment identifier, such as production-us. All instances sharing protection must use the same namespace. Changing it starts fresh protection state.",
  timeout: "Deadline for one protection decision. Omitted uses 250ms; choose 10ms–5s based on your backend and measured latency.",
  startup_timeout: "Time allowed to connect and verify the store before serving. A failure exits Hookway; startup never applies migrations.",
  interval: "How often to sweep expired state. Admission checks expiry independently of this cleanup schedule.",
  batch_size: "Maximum expired entries removed per cleanup batch. Cleanup never evicts live protection.",
  budget: "Maximum time spent in one cleanup pass.",
  url: "Use an environment reference such as ${PROTECTION_DATABASE_URL}. Connections and credentials are resolved by Hookway, not the builder.",
  pool_size: "Maximum pooled connections for this store in each Hookway process.",
  max_conns: "Maximum pooled database connections for this store in each Hookway process.",
  mode: "Single server, Redis Cluster, or Sentinel. Protection operations use the writable primary.",
  addrs: "Redis server or discovery addresses, such as localhost:6379.",
  master_name: "Sentinel master name, required when using Sentinel.",
  db: "Redis database number. Cluster uses database 0.",
  password: "Use ${REDIS_PASSWORD}; never put a production password in a saved builder document.",
  username: "Redis ACL username, preferably an environment reference.",
};

export function StateStoresPanel({ opened, onClose }: { opened: boolean; onClose: () => void }) {
  const value = useGraphStore((s) => s.configExtra.state_stores);
  const setConfigExtra = useGraphStore((s) => s.setConfigExtra);
  return (
    <Modal opened={opened} onClose={onClose} title="Protection stores" size="xl"
      closeButtonProps={{ "aria-label": "Close Protection stores" }}>
      <Stack gap="md">
        <Text size="sm">Define the stores used by rate-limit and duplicate-protection nodes. Memory coordinates one process; Redis and PostgreSQL coordinate instances using the same namespace and policy.</Text>
        <WorkspaceEntries label="protection store" value={value}
          initial={() => ({ namespace: "", memory: {} })} nameError={routingNameError}
          onChange={(state_stores) => setConfigExtra({ state_stores })}>
          {(name, entry, change) => {
            const configured = backends.filter((kind) => entry[kind] !== undefined);
            const kind = configured.length === 1 ? configured[0] : "";
            const visible = ["namespace", "timeout", "startup_timeout", "cleanup", ...(kind ? [kind] : configured)];
            const unknown = Object.keys(entry).filter((key) => !properties[key]);
            return (
              <Stack gap="sm">
                <Select label="Protection backend" value={kind || null} allowDeselect={false}
                  data={[{ value: "memory", label: "Memory · one process" }, { value: "redis", label: "Redis · shared" }, { value: "postgres", label: "PostgreSQL · shared" }]}
                  description="Changing backend removes its connection settings and starts separate protection state. Namespace and timeout settings are retained."
                  onChange={(next) => {
                    if (!next || next === kind) return;
                    change({ ...Object.fromEntries(Object.entries(entry).filter(([key]) => !backends.includes(key))), [next]: {} });
                  }} />
                {!kind && <Alert color="orange">Select exactly one backend. Imported settings are retained until you choose one.</Alert>}
                {kind === "memory" && <Alert color="blue">State resets when this process restarts. Other instances have independent quotas and duplicate windows.</Alert>}
                {kind === "redis" && <Alert color="blue">Use a non-evicting protection deployment. Persistence and failover settings determine how state survives backend failures.</Alert>}
                {kind === "postgres" && <Alert color="blue">Apply migrations with hookway state-store migrate before serving. Expired entries stop affecting admission immediately; background cleanup removes their rows later.</Alert>}
                {unknown.length > 0 && <Alert color="orange">Unrecognized settings are retained: {unknown.join(", ")}. Review the exported YAML.</Alert>}
                <SchemaForm schema={{ type: "object", properties: Object.fromEntries(visible.map((key) => [key, properties[key]])), required: ["namespace", ...(kind ? [kind] : [])] }}
                  value={entry} onChange={(next) => change(record(next))}
                  scope={`state_stores/${encodeURIComponent(name)}`} path={`state_stores.${name}`}
                  order={["namespace", kind, "timeout", "startup_timeout", "cleanup"]}
                  enums={{ mode: ["single", "cluster", "sentinel"] }}
                  labels={{ namespace: "Deployment namespace", timeout: "Operation timeout", startup_timeout: "Startup timeout", cleanup: "Expired-state cleanup", interval: "Cleanup interval", batch_size: "Cleanup batch size", budget: "Cleanup time budget", memory: "Memory", redis: "Redis connection", postgres: "PostgreSQL connection", url: "Connection URL", pool_size: "Connection pool size", max_conns: "Connection pool size", addrs: "Server addresses", master_name: "Sentinel master name", db: "Database number", mode: "Redis topology" }}
                  fieldHelp={(path) => help[path.split(".").at(-1) ?? path]} />
              </Stack>
            );
          }}
        </WorkspaceEntries>
      </Stack>
    </Modal>
  );
}
