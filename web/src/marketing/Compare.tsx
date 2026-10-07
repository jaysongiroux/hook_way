import { Anchor, Box, Button, Container, Group, Stack, Table, Text, Title } from "@mantine/core";
import { useDocumentTitle } from "@mantine/hooks";
import { Link } from "react-router-dom";
import {
  COMPARE_DIFFERS,
  COMPARE_CHECKED_ON,
  COMPARE_INTRO,
  COMPARE_OUTPOST_NOTE,
  COMPARE_ROWS,
  COMPARE_SOURCES,
  COMPETITORS,
  type SupportLevel,
} from "./compareCopy";
import { CtaBand } from "./CtaBand";
import { MarketingShell } from "./MarketingShell";
import { Section } from "./Section";
import {
  COMPARE_LEVEL_CLASS,
  COMPARE_TABLE_CLASS,
  PAGE_HERO_CLASS,
} from "./marketingStyles";
import { DARK } from "../tokens";

function levelLabel(level: SupportLevel): string {
  switch (level) {
    case "yes":
      return "Yes";
    case "partial":
      return "Partial";
    case "no":
      return "No";
    case "n/a":
      return "n/a";
  }
}

export function Compare() {
  useDocumentTitle("Compare — Hookway");
  return (
    <MarketingShell>
      <Box component="section" className={PAGE_HERO_CLASS} pt={56} pb={64} style={{ background: DARK.bg }}>
        <Container size="lg">
          <Stack gap="md" maw={720}>
            <Title
              order={1}
              c={DARK.text}
              style={{ fontSize: "clamp(32px, 5vw, 44px)", lineHeight: 1.15, letterSpacing: -0.4, fontWeight: 650 }}
            >
              {COMPARE_INTRO.title}
            </Title>
            <Text size="lg" c={DARK.muted} maw={640}>
              {COMPARE_INTRO.lead}
            </Text>
            <Group gap="sm" mt="xs">
              <Button component={Link} to="/use-cases" size="md" color="brand">
                See use cases
              </Button>
              <Button
                component={Link}
                to="/builder"
                size="md"
                variant="default"
                styles={{ root: { background: DARK.surface, borderColor: DARK.border, color: DARK.text } }}
              >
                Open the builder
              </Button>
            </Group>
          </Stack>
        </Container>
      </Box>

      <Section
        title="Products in this comparison"
        lead="Three inbound-oriented offerings. This page covers features and integrations only."
      >
        <Box className="hookway-compare-products">
          {COMPETITORS.map((c) => (
            <Box
              key={c.id}
              className="hookway-compare-product"
              data-product={c.id}
              style={{
                background: DARK.surface,
                border: `1px solid ${DARK.border}`,
                borderRadius: 4,
                padding: 20,
              }}
            >
              <Stack gap={8}>
                <Group gap="sm">
                  <Text fw={650} c={DARK.text}>{c.name}</Text>
                  {c.mark && <Text size="xs" c={DARK.muted}>{c.mark}</Text>}
                </Group>
                <Text size="sm" c={DARK.muted} style={{ lineHeight: 1.55 }}>
                  {c.role}
                </Text>
                <Anchor href={c.sourceHref} target="_blank" rel="noopener noreferrer" size="sm" c={DARK.accent}>
                  {c.sourceLabel}
                </Anchor>
              </Stack>
            </Box>
          ))}
        </Box>
      </Section>

      <Section
        title="Feature and integration matrix"
        lead={`Checked ${COMPARE_CHECKED_ON}. Each cell links to its supporting documentation. Different delivery and storage boundaries are explained where they matter.`}
        background={DARK.recessed}
      >
        <Box component="aside" className="hookway-compare-differs" aria-labelledby="where-hookway-differs">
          <Text id="where-hookway-differs" component="h3" fw={650} c={DARK.text} size="md" style={{ margin: 0 }}>
            {COMPARE_DIFFERS.title}
          </Text>
          <Box component="ul" className="hookway-compare-differs-list">
            {COMPARE_DIFFERS.points.map((point) => (
              <Box component="li" key={point.title}>
                <Text fw={650} c={DARK.text} size="sm">
                  {point.title}
                </Text>
                <Text size="sm" c={DARK.muted} mt={4} style={{ lineHeight: 1.5 }}>
                  {point.detail}
                </Text>
              </Box>
            ))}
          </Box>
        </Box>
        <Text size="xs" c={DARK.muted} mb={8}>
          Scroll sideways to read each product's column.
        </Text>
        <Box
          className={COMPARE_TABLE_CLASS}
          role="region"
          aria-label="Scrollable comparison table"
          tabIndex={0}
          style={{ overflowX: "auto" }}
        >
          <Table
            aria-label="Feature and integration comparison"
            withTableBorder
            withColumnBorders
            horizontalSpacing="md"
            verticalSpacing="md"
            styles={{
              table: {
                background: DARK.surface,
                borderColor: DARK.border,
                borderCollapse: "separate",
                borderSpacing: 0,
              },
              th: {
                color: DARK.text,
                fontWeight: 650,
                fontSize: 13,
                borderColor: DARK.border,
              },
              td: { color: DARK.muted, fontSize: 13, verticalAlign: "top", borderColor: DARK.border },
            }}
          >
            <Table.Thead>
              <Table.Tr>
                <Table.Th scope="col">Capability</Table.Th>
                {COMPETITORS.map((c) => (
                  <Table.Th key={c.id} scope="col" data-product={c.id}>
                    {c.name}
                  </Table.Th>
                ))}
              </Table.Tr>
            </Table.Thead>
            <Table.Tbody>
              {COMPARE_ROWS.map((row) => (
                <Table.Tr key={row.id}>
                  <Table.Th scope="row">
                    <Text fw={600} c={DARK.text} size="sm">
                      {row.capability}
                    </Text>
                    {row.note && (
                      <Text size="xs" c={DARK.muted} mt={4} fw={450}>
                        {row.note}
                      </Text>
                    )}
                  </Table.Th>
                  {COMPETITORS.map((c) => {
                    const cell = row.values[c.id];
                    return (
                      <Table.Td key={c.id} data-product={c.id}>
                        <Text
                          component="span"
                          className={COMPARE_LEVEL_CLASS}
                          data-level={cell.level}
                          fw={650}
                          size="xs"
                        >
                          {cell.count !== undefined ? `${cell.count} ${cell.count === 1 ? "type" : "types"}` : levelLabel(cell.level)}
                        </Text>
                        <Text size="sm" mt={6} style={{ lineHeight: 1.45 }}>
                          {cell.detail}
                        </Text>
                        <Anchor
                          href={row.sources[c.id]}
                          target="_blank"
                          rel="noopener noreferrer"
                          size="xs"
                          aria-label={`${c.name}: ${row.capability} documentation`}
                        >
                          Documentation
                        </Anchor>
                      </Table.Td>
                    );
                  })}
                </Table.Tr>
              ))}
            </Table.Tbody>
          </Table>
        </Box>
      </Section>

      <Section title={COMPARE_OUTPOST_NOTE.title}>
        <Text size="md" c={DARK.muted} maw={720} style={{ lineHeight: 1.55 }}>
          {COMPARE_OUTPOST_NOTE.body}{" "}
          <Anchor href={COMPARE_OUTPOST_NOTE.href} target="_blank" rel="noopener noreferrer" c={DARK.accent}>
            {COMPARE_OUTPOST_NOTE.label}
          </Anchor>
          .
        </Text>
        <Box mt={28}>
          <Text size="sm" fw={650} c={DARK.text} mb={8}>
            Sources checked for this page
          </Text>
          <Stack gap={4}>
            {COMPARE_SOURCES.map((s) => (
              <Anchor key={s.href} href={s.href} target="_blank" rel="noopener noreferrer" size="sm" c={DARK.accent}>
                {s.label}
              </Anchor>
            ))}
          </Stack>
        </Box>
      </Section>

      <CtaBand />
    </MarketingShell>
  );
}
