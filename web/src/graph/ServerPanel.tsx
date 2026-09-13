import { Drawer, Stack, Text, TextInput, NumberInput } from "@mantine/core";
import { useGraphStore } from "./store";
import { BUILDER_FLOATING_CLASS, BUILDER_FLOATING_HEADER_CLASS, BUILDER_OVERLAY_CLASS } from "../builderStyles";

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
  { key: "read_timeout", label: "Read timeout", kind: "text", placeholder: "5s" },
  { key: "write_timeout", label: "Write timeout", kind: "text", placeholder: "10s" },
  { key: "idle_timeout", label: "Idle timeout", kind: "text", placeholder: "60s" },
  { key: "shutdown_timeout", label: "Shutdown timeout", kind: "text", placeholder: "10s" },
  { key: "publish_timeout", label: "Publish timeout", kind: "text", placeholder: "10s" },
  // -1 disables the limit, so no min clamp here.
  { key: "max_inflight_requests", label: "Max inflight requests", kind: "number" },
  { key: "fanout_concurrency", label: "Fan-out concurrency", kind: "number", min: 0 },
  { key: "readiness_interval", label: "Readiness interval", kind: "text", placeholder: "10s" },
  { key: "readiness_timeout", label: "Readiness timeout", kind: "text", placeholder: "5s" },
];

export function ServerPanel({ opened, onClose }: { opened: boolean; onClose: () => void }) {
  const serverConfig = useGraphStore((s) => s.serverConfig);
  const setServerConfig = useGraphStore((s) => s.setServerConfig);
  return (
    <Drawer
      opened={opened}
      onClose={onClose}
      position="right"
      title="Server settings"
      classNames={{ content: BUILDER_FLOATING_CLASS, header: BUILDER_FLOATING_HEADER_CLASS }}
      overlayProps={{ className: BUILDER_OVERLAY_CLASS }}
    >
      <Stack gap="sm">
        <Text size="sm" c="dimmed">
          The top-level <Text span ff="monospace">server:</Text> block. <Text span ff="monospace">addr</Text> and{" "}
          <Text span ff="monospace">max_body_bytes</Text> are required; timeouts accept durations like{" "}
          <Text span ff="monospace">5s</Text>.
        </Text>
        {SERVER_FIELDS.map((f) => {
          const val = serverConfig[f.key];
          if (f.kind === "number") {
            return (
              <NumberInput
                key={f.key}
                label={f.label}
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
              placeholder={f.placeholder}
              value={String(val ?? "")}
              onChange={(e) => setServerConfig({ [f.key]: e.currentTarget.value })}
            />
          );
        })}
      </Stack>
    </Drawer>
  );
}
