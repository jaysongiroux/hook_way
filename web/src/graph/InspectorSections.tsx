import { Accordion, Badge, Group, Text } from "@mantine/core";
import { useState, type ReactNode } from "react";

import { DARK } from "../tokens";

// BORDER is the sidebar's hairline, shared by section frames and route cards.
const BORDER = DARK.border;

export interface SectionSpec {
  /** Stable identity for the open/closed state. */
  value: string;
  label: string;
  /** Short summary shown on the header so a collapsed section still reads. */
  badge?: string | number;
  /** Rendered only while the section is open. */
  render: () => ReactNode;
}

// Sections renders the inspector's collapsible groups. The first section opens
// on selection and the rest carry a summary badge, so a 300px sidebar shows one
// group's fields at a time without hiding what the others hold. Panels mount
// only while open; every field writes straight to the graph store, so there is
// no local form state to lose on collapse.
export function Sections({ sections }: { sections: SectionSpec[] }) {
  const [open, setOpen] = useState<string[]>(sections.length > 0 ? [sections[0].value] : []);
  return (
    <Accordion
      multiple
      value={open}
      onChange={setOpen}
      variant="separated"
      radius="sm"
      chevronPosition="left"
      // No height animation: panels mount their children only while open, and
      // Collapse measures its target height before those children exist, which
      // leaves a freshly opened section clipped to a stale height.
      transitionDuration={0}
      styles={{
        item: { border: `1px solid ${BORDER}` },
        control: { paddingInline: 8 },
        content: { paddingInline: 8, paddingBottom: 8 },
      }}
    >
      {sections.map((s) => (
        <Accordion.Item key={s.value} value={s.value}>
          <Accordion.Control>
            <Group justify="space-between" wrap="nowrap" gap="xs">
              <Text size="sm" fw={500}>
                {s.label}
              </Text>
              {s.badge !== undefined && s.badge !== "" && (
                <Badge size="sm" variant="light" color="gray" tt="none" style={{ flexShrink: 0 }}>
                  {s.badge}
                </Badge>
              )}
            </Group>
          </Accordion.Control>
          <Accordion.Panel>{open.includes(s.value) ? s.render() : null}</Accordion.Panel>
        </Accordion.Item>
      ))}
    </Accordion>
  );
}
