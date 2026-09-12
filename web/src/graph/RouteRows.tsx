import { Group, Stack, Text, UnstyledButton } from "@mantine/core";
import { useGraphStore } from "./store";
import { BORDER } from "./InspectorSections";

export interface RouteRowSpec {
  edgeId: string;
  /** Dimmed caption above the target, e.g. "→ orders" or "from digifi". */
  caption: string;
  target: string;
  /** Accessible name for the row. */
  aria: string;
}

// RouteRowList renders the clickable route cards shared by an endpoint's
// outgoing routes and a destination's incoming ones; selecting a row opens
// that route in the inspector.
export function RouteRowList({ rows, emptyText }: { rows: RouteRowSpec[]; emptyText: string }) {
  const setSelectedEdge = useGraphStore((s) => s.setSelectedEdge);
  if (rows.length === 0) {
    return (
      <Text size="xs" c="dimmed">
        {emptyText}
      </Text>
    );
  }
  return (
    <Stack gap={6}>
      {rows.map((r) => (
        <UnstyledButton key={r.edgeId} aria-label={r.aria} onClick={() => setSelectedEdge(r.edgeId)}>
          <Group
            justify="space-between"
            wrap="nowrap"
            style={{ border: `1px solid ${BORDER}`, borderRadius: 6, padding: "6px 8px" }}
          >
            <div style={{ minWidth: 0 }}>
              <Text size="xs" c="dimmed">
                {r.caption}
              </Text>
              <Text size="sm" truncate>
                {r.target || "(no target)"}
              </Text>
            </div>
            <Text c="dimmed">›</Text>
          </Group>
        </UnstyledButton>
      ))}
    </Stack>
  );
}
