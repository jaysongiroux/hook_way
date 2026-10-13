import { Modal, Stack, Text, TextInput, NumberInput } from "@mantine/core";
import { useGraphStore } from "./store";
import { SERVER_FIELD_HELP } from "./help";

// The managed keys of the top-level `server:` block. Unknown server keys the UI
// doesn't list here still ride along untouched (setServerConfig merges patches).
interface ServerField {
  key: string;
  label: string;
  kind: "text" | "number";
  placeholder?: string;
  min?: number;
}

const SERVER_FIELDS: ServerField[] = [
  { key: "addr", label: "Address", kind: "text", placeholder: ":8080" },
  { key: "max_body_bytes", label: "Max body bytes", kind: "number", min: 0 },
  {
    key: "read_timeout",
    label: "Read timeout",
    kind: "text",
    placeholder: "5s",
  },
  {
    key: "write_timeout",
    label: "Write timeout",
    kind: "text",
    placeholder: "10s",
  },
  {
    key: "idle_timeout",
    label: "Idle timeout",
    kind: "text",
    placeholder: "60s",
  },
  {
    key: "shutdown_delay",
    label: "Traffic withdrawal delay",
    kind: "text",
    placeholder: "0s",
  },
  {
    key: "shutdown_timeout",
    label: "Shutdown timeout",
    kind: "text",
    placeholder: "10s",
  },
  {
    key: "shutdown_total_timeout",
    label: "Total shutdown limit",
    kind: "text",
    placeholder: "60s",
  },
  {
    key: "publish_timeout",
    label: "Publish timeout",
    kind: "text",
    placeholder: "10s",
  },
  // -1 disables the limit, so no min clamp here.
  {
    key: "max_inflight_requests",
    label: "Max inflight requests",
    kind: "number",
  },
  {
    key: "fanout_concurrency",
    label: "Fan-out concurrency",
    kind: "number",
    min: 0,
  },
  {
    key: "readiness_interval",
    label: "Readiness interval",
    kind: "text",
    placeholder: "10s",
  },
  {
    key: "readiness_timeout",
    label: "Readiness timeout",
    kind: "text",
    placeholder: "5s",
  },
];

export function ServerPanel({
  opened,
  onClose,
}: {
  opened: boolean;
  onClose: () => void;
}) {
  const serverConfig = useGraphStore((s) => s.serverConfig);
  const setServerConfig = useGraphStore((s) => s.setServerConfig);
  return (
    <Modal
      opened={opened}
      onClose={onClose}
      title="Core server settings"
      size="xl"
      closeButtonProps={{ "aria-label": "Close Core server settings" }}
    >
      <Stack gap="sm">
        <Text size="sm" c="dimmed">
          Process-wide{" "}
          <Text span ff="monospace">
            server:
          </Text>{" "}
          settings written into the exported YAML.{" "}
          <Text span ff="monospace">
            addr
          </Text>{" "}
          and{" "}
          <Text span ff="monospace">
            max_body_bytes
          </Text>{" "}
          are required. Timeouts are Go durations such as{" "}
          <Text span ff="monospace">
            5s
          </Text>{" "}
          or{" "}
          <Text span ff="monospace">
            1m
          </Text>
          .
        </Text>
        {SERVER_FIELDS.map((f) => {
          const val = serverConfig[f.key];
          if (f.kind === "number") {
            return (
              <NumberInput
                key={f.key}
                label={f.label}
                description={SERVER_FIELD_HELP[f.key]}
                value={val === "" || val == null ? "" : Number(val)}
                onChange={(v) => setServerConfig({ [f.key]: v })}
                min={f.min}
              />
            );
          }
          return (
            <TextInput
              key={f.key}
              label={f.label}
              description={SERVER_FIELD_HELP[f.key]}
              placeholder={f.placeholder}
              value={String(val ?? "")}
              onChange={(e) =>
                setServerConfig({ [f.key]: e.currentTarget.value })
              }
            />
          );
        })}
      </Stack>
    </Modal>
  );
}
