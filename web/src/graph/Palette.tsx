import { Paper, Stack, Text } from "@mantine/core";
import { NODE_SCHEMAS, NODE_TYPES, nodeLabel } from "./nodeSchema";
import { nodeHelp, PALETTE_GROUP_HELP } from "./help";

export const DND_MIME = "application/hookway-node";

const GROUPS: { title: string; types: string[] }[] = [
  { title: "Trigger", types: NODE_TYPES.filter((t) => NODE_SCHEMAS[t].input === "None") },
  { title: "Request head", types: NODE_TYPES.filter((t) => NODE_SCHEMAS[t].input === "RequestHead") },
  { title: "Raw body", types: NODE_TYPES.filter((t) => NODE_SCHEMAS[t].input === "RawRequest") },
  { title: "Verified", types: NODE_TYPES.filter((t) => NODE_SCHEMAS[t].input === "Verified") },
  { title: "Envelope", types: NODE_TYPES.filter((t) => NODE_SCHEMAS[t].input === "Envelope" && !NODE_SCHEMAS[t].detached && NODE_SCHEMAS[t].output !== "Terminal") },
  { title: "Terminal", types: NODE_TYPES.filter((t) => NODE_SCHEMAS[t].output === "Terminal") },
];

export function Palette() {
  return (
    <Stack gap="md">
      {GROUPS.map((group) =>
        group.types.length === 0 ? null : (
          <Stack key={group.title} gap="xs">
            <div>
              <Text fw={600} size="xs" tt="uppercase" c="dimmed">
                {group.title}
              </Text>
              {PALETTE_GROUP_HELP[group.title] && (
                <Text size="xs" c="dimmed" lh={1.35}>
                  {PALETTE_GROUP_HELP[group.title]}
                </Text>
              )}
            </div>
            {group.types.map((type) => (
              <Paper
                key={type}
                withBorder
                p="xs"
                radius="sm"
                draggable
                onDragStart={(e) => {
                  e.dataTransfer.setData(DND_MIME, type);
                  e.dataTransfer.effectAllowed = "move";
                }}
                style={{ cursor: "grab" }}
              >
                <Text size="sm" fw={500}>
                  {nodeLabel(type)}
                </Text>
                <Text size="xs" c="dimmed" lh={1.35}>
                  {nodeHelp(type)}
                </Text>
              </Paper>
            ))}
          </Stack>
        ),
      )}
    </Stack>
  );
}
