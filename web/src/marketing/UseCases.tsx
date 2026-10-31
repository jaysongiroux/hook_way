import {
  Anchor,
  Box,
  Button,
  Container,
  Group,
  Text,
  Title,
} from "@mantine/core";
import { Link } from "react-router-dom";
import { CtaBand } from "./CtaBand";
import { MarketingShell } from "./MarketingShell";
import { USE_CASES, USE_CASES_FOOTER, USE_CASES_INTRO } from "./useCasesCopy";
import { SCENARIO_DIAGRAMS, SCENARIO_GROUPS } from "./useCaseDiagrams";
import { UseCaseDiagram } from "./UseCaseDiagram";
import { DARK } from "../tokens";
import { docsHref } from "./links";
import "./useCases.css";
const guidePaths: Record<string, string> = {
  "mcp-gateway": "docs/guides/openapi-import.md",
  "mcp-events": "docs/guides/mcp-events.md",
  fanout: "docs/guides/webhooks-to-queues.md",
  "dynamic-routing": "docs/guides/webhooks-to-queues.md",
};
export function UseCases() {
  return (
    <MarketingShell>
      <Container size="lg" py={48}>
        <Group component="nav" aria-label="Breadcrumb" gap="xs" mb="xl">
          <Anchor component={Link} to="/">
            Home
          </Anchor>
          <Text aria-hidden="true">/</Text>
          <Text>Use cases</Text>
        </Group>
        <Title order={1}>{USE_CASES_INTRO.title}</Title>
        <Text size="xl" c={DARK.muted} mt="lg" maw={900}>
          {USE_CASES_INTRO.lead}
        </Text>
        <Group my="xl">
          <Button component={Link} to={docsHref("docs/get-started.md")}>
            Get started
          </Button>
          <Anchor href={USE_CASES_FOOTER.examplesHref}>
            Browse all examples
          </Anchor>
        </Group>
        {SCENARIO_GROUPS.map((group) => (
          <section key={group.label}>
            <Title order={2} mt={48}>
              {group.label}
            </Title>
            {group.items.map((item) => {
              const scenario = USE_CASES.find((value) => value.id === item.id)!;
              const diagram = SCENARIO_DIAGRAMS[item.id];
              return (
                <Box
                  component="article"
                  key={item.id}
                  id={item.id}
                  className="hookway-use-case"
                >
                  <Title order={3}>{scenario.title}</Title>
                  <Text mt="sm" c={DARK.muted}>
                    {scenario.summary}
                  </Text>
                  <Group mt="md">
                    {guidePaths[item.id] && (
                      <Anchor
                        component={Link}
                        to={docsHref(guidePaths[item.id])}
                      >
                        Follow the complete guide
                      </Anchor>
                    )}
                    <Anchor href={scenario.exampleHref}>
                      {scenario.exampleLabel}
                    </Anchor>
                  </Group>
                  <details className="hookway-scenario-detail">
                    <summary>How this pattern works</summary>
                    <Text size="sm">{scenario.detail}</Text>
                    <figure>
                      <UseCaseDiagram diagram={diagram} />
                      <figcaption>{diagram.caption}</figcaption>
                    </figure>
                  </details>
                </Box>
              );
            })}
          </section>
        ))}
      </Container>
      <CtaBand />
    </MarketingShell>
  );
}
