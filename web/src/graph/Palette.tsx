import { Box, Stack, Text } from "@mantine/core";
import { NODE_SCHEMAS, NODE_TYPES, nodeLabel } from "./nodeSchema";
import { nodeHelp, PALETTE_GROUP_HELP } from "./help";
import { PALETTE_ITEM_CLASS } from "./builderStyles";
import { DARK } from "../tokens";
import { TEMPLATES } from "./templates";

export const DND_MIME = "application/hookway-node";
export const DND_TEMPLATE_MIME = "application/hookway-template";

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
    <Stack gap="lg">
      <Stack gap={8}>
        <div>
          <Text fw={600} size="xs" tt="uppercase" c={DARK.muted} style={{ letterSpacing: "0.08em" }}>
            Templates
          </Text>
          <Text size="xs" c={DARK.muted} lh={1.4} mt={4}>
            {PALETTE_GROUP_HELP.Templates}
          </Text>
        </div>
        {TEMPLATES.map((def) => (
          <Box
            key={def.id}
            className={PALETTE_ITEM_CLASS}
            draggable
            onDragStart={(e) => {
              e.dataTransfer.setData(DND_TEMPLATE_MIME, def.id);
              e.dataTransfer.effectAllowed = "move";
            }}
          >
            <Box px={10} py={8}>
              <Text size="sm" fw={600} c={DARK.text}>
                {def.label}
              </Text>
              <Text size="xs" c={DARK.muted} lh={1.4}>
                {def.help}
              </Text>
            </Box>
          </Box>
        ))}
      </Stack>
      {GROUPS.map((group) =>
        group.types.length === 0 ? null : (
          <Stack key={group.title} gap={8}>
            <div>
              <Text fw={600} size="xs" tt="uppercase" c={DARK.muted} style={{ letterSpacing: "0.08em" }}>
                {group.title}
              </Text>
              {PALETTE_GROUP_HELP[group.title] && (
                <Text size="xs" c={DARK.muted} lh={1.4} mt={4}>
                  {PALETTE_GROUP_HELP[group.title]}
                </Text>
              )}
            </div>
            {group.types.map((type) => (
                <Box
                  key={type}
                  className={PALETTE_ITEM_CLASS}
                  draggable
                  onDragStart={(e) => {
                    e.dataTransfer.setData(DND_MIME, type);
                    e.dataTransfer.effectAllowed = "move";
                  }}
                >
                  <Box px={10} py={8}>
                    <Text size="sm" fw={600} c={DARK.text}>
                      {nodeLabel(type)}
                    </Text>
                    <Text size="xs" c={DARK.muted} lh={1.4}>
                      {nodeHelp(type)}
                    </Text>
                  </Box>
                </Box>
              ))}
          </Stack>
        ),
      )}
    </Stack>
  );
}
