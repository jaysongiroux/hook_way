import { Anchor, Box, Stack, Text, Title } from "@mantine/core";
import { DOC_LINKS, EXAMPLES, docsHref } from "./links";
import { Section } from "./Section";
import { DARK } from "../tokens";

const GROUPS = [
  {
    title: "Build a flow",
    paths: [
      "docs/reference/configuration.md",
      "docs/guides/ingress-adapters.md",
      "docs/reference/cli.md",
      "docs/guides/json-validation.md",
    ],
  },
  {
    title: "Deploy and operate",
    paths: [
      "docs/evidence/performance.md",
      "docs/operations/replay-service.md",
      "docs/guides/protection-stores.md",
      "docs/reference/security.md",
    ],
  },
];

export function DocsLinks() {
  const quickstart = DOC_LINKS[0];
  const index = DOC_LINKS[1];
  const development = DOC_LINKS.find(
    (link) => link.path === "docs/development/local-development.md",
  )!;
  return (
    <Section
      title="From your first flow to production"
      lead="Start with a working configuration. Find the details as you need them."
      background={DARK.recessed}
    >
      <Box className="hookway-docs-layout">
        <Stack gap={16} className="hookway-docs-start">
          <Title order={3} size="h4">
            Run your first flow
          </Title>
          <Text c={DARK.muted} size="sm">
            Run Hookway locally, change an example, and follow a request through
            your infrastructure.
          </Text>
          <Anchor href={quickstart.href} fw={600}>
            {quickstart.title}
          </Anchor>
          <Anchor href={EXAMPLES.href}>{EXAMPLES.title}</Anchor>
          <Anchor href={docsHref("docs/guides/mcp-gateway.md")}>
            MCP gateway guide
          </Anchor>
          <Anchor href={development.href}>{development.title}</Anchor>
        </Stack>
        <Box className="hookway-docs-references">
          {GROUPS.map((group) => (
            <Box key={group.title} className="hookway-docs-group">
              <Title order={3} size="h5">
                {group.title}
              </Title>
              <Box component="ul" className="hookway-docs-list">
                {group.paths.map((path) => {
                  const link = DOC_LINKS.find((item) => item.path === path)!;
                  return (
                    <li key={path}>
                      <Anchor href={link.href}>{link.title}</Anchor>
                      <Text size="sm" c={DARK.muted}>
                        {link.description}
                      </Text>
                    </li>
                  );
                })}
              </Box>
            </Box>
          ))}
        </Box>
      </Box>
      <Box className="hookway-docs-footer">
        <Anchor href={index.href}>{index.title}</Anchor>
        <Text size="sm" c={DARK.muted}>
          Documentation is versioned with the code and built from repository
          sources.
        </Text>
      </Box>
    </Section>
  );
}
