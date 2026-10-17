import {
  Anchor,
  Box,
  Button,
  Container,
  Group,
  Tabs,
  Text,
  Title,
} from "@mantine/core";
import { useDocumentTitle } from "@mantine/hooks";
import { useState } from "react";
import { Link } from "react-router-dom";
import { CtaBand } from "./CtaBand";
import { MarketingShell } from "./MarketingShell";
import { USE_CASES, USE_CASES_FOOTER, USE_CASES_INTRO } from "./useCasesCopy";
import { SCENARIO_DIAGRAMS, SCENARIO_GROUPS } from "./useCaseDiagrams";
import { UseCaseDiagram } from "./UseCaseDiagram";
import { DARK } from "../tokens";
import "./useCases.css";

export function UseCases() {
  useDocumentTitle("Use cases — Hookway");
  const [active, setActive] = useState("mcp-gateway");
  const scenario = USE_CASES.find((item) => item.id === active)!;
  const diagram = SCENARIO_DIAGRAMS[active];
  return (
    <MarketingShell animated>
      <Box component="section" className="hookway-scenarios-intro">
        <Container size="lg">
          <div
            className="hookway-scenarios-heading"
            data-marketing-motion="intro"
          >
            <Title order={1} c={DARK.text}>
              {USE_CASES_INTRO.title}
            </Title>
            <div>
              <Text size="lg" c={DARK.muted}>
                {USE_CASES_INTRO.lead}
              </Text>
              <Group gap="sm" mt={24}>
                <Button component={Link} to="/builder" size="md" color="brand">
                  Open the builder
                </Button>
                <Anchor
                  href={USE_CASES_FOOTER.examplesHref}
                  className="hookway-scenario-library"
                >
                  Browse all examples
                </Anchor>
              </Group>
            </div>
          </div>
        </Container>
      </Box>

      <Container size="lg" className="hookway-scenarios-container">
        <Tabs
          value={active}
          onChange={(value) => value && setActive(value)}
          orientation="vertical"
          keepMounted={false}
          className="hookway-scenario-explorer"
        >
          <Tabs.List
            aria-label="Choose a use case"
            className="hookway-scenario-rail"
          >
            {SCENARIO_GROUPS.map((group) => (
              <div key={group.label} className="hookway-scenario-group">
                <Text component="p" className="hookway-scenario-group-label">
                  {group.label}
                </Text>
                {group.items.map((item) => (
                  <Tabs.Tab value={item.id} key={item.id}>
                    {item.label}
                  </Tabs.Tab>
                ))}
              </div>
            ))}
          </Tabs.List>
          <div className="hookway-scenario-mobile-choice">
            <label htmlFor="hookway-scenario-choice">Choose a use case</label>
            <select
              id="hookway-scenario-choice"
              value={active}
              onChange={(event) => setActive(event.currentTarget.value)}
            >
              {SCENARIO_GROUPS.map((group) => (
                <optgroup label={group.label} key={group.label}>
                  {group.items.map((item) => (
                    <option value={item.id} key={item.id}>
                      {item.label}
                    </option>
                  ))}
                </optgroup>
              ))}
            </select>
          </div>
          <Tabs.Panel
            value={active}
            className="hookway-scenario-panel"
            aria-labelledby="hookway-scenario-title"
          >
            <div className="hookway-scenario-narrative">
              <Title order={2} id="hookway-scenario-title">
                {scenario.title}
              </Title>
              <Text c={DARK.muted} className="hookway-scenario-summary">
                {scenario.summary}
              </Text>
            </div>
            <figure className="hookway-scenario-figure">
              <div className="hookway-scenario-canvas">
                <UseCaseDiagram diagram={diagram} />
              </div>
              <figcaption>{diagram.caption}</figcaption>
            </figure>
            <div className="hookway-scenario-source">
              <Text size="sm" c={DARK.muted}>
                Start with the runnable configuration
              </Text>
              <Anchor href={scenario.exampleHref}>
                {scenario.exampleLabel}
              </Anchor>
            </div>
            <details key={active} className="hookway-scenario-detail">
              <summary>How this pattern works</summary>
              <Text size="sm" c={DARK.muted}>
                {scenario.detail}
              </Text>
            </details>
            <Text
              size="xs"
              c={DARK.muted}
              className="hookway-scenario-footnote"
            >
              Simplified flow. The linked YAML includes the complete nodes,
              authentication and configuration.
            </Text>
          </Tabs.Panel>
        </Tabs>
      </Container>
      <CtaBand />
    </MarketingShell>
  );
}
