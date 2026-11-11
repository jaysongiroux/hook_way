import { Anchor, Button, Container, Group, Text, Title } from "@mantine/core";
import { Link, useLocation, useNavigate } from "react-router-dom";
import { CtaBand } from "./CtaBand";
import { MarketingShell } from "./MarketingShell";
import { USE_CASES, USE_CASES_FOOTER, USE_CASES_INTRO } from "./useCasesCopy";
import { SCENARIO_DIAGRAMS, SCENARIO_GROUPS } from "./useCaseDiagrams";
import { UseCaseDiagram } from "./UseCaseDiagram";
import { DARK } from "../tokens";
import { docsHref } from "./links";
import "./useCases.css";

const guidePaths: Record<string, string> = {
  "sentry-discord": "docs/guides/sentry-discord.md",
  "mcp-gateway": "docs/guides/openapi-import.md",
  "mcp-events": "docs/guides/mcp-events.md",
  fanout: "docs/guides/webhooks-to-queues.md",
  "dynamic-routing": "docs/guides/webhooks-to-queues.md",
};

export function UseCases() {
  const { hash } = useLocation();
  const navigate = useNavigate();
  const requested = hash.slice(1);
  const selected = USE_CASES.some((item) => item.id === requested)
    ? requested
    : SCENARIO_GROUPS[0].items[0].id;

  return (
    <MarketingShell>
      <Container size="lg" className="hookway-scenarios-container">
        <header className="hookway-scenarios-intro">
          <Group component="nav" aria-label="Breadcrumb" gap="xs" className="hookway-scenarios-breadcrumb">
            <Anchor component={Link} to="/">Home</Anchor>
            <Text aria-hidden="true">/</Text>
            <Text>Use cases</Text>
          </Group>
          <div className="hookway-scenarios-heading">
            <Title order={1}>{USE_CASES_INTRO.title}</Title>
            <div>
              <Text c={DARK.muted} className="hookway-scenarios-lead">{USE_CASES_INTRO.lead}</Text>
              <Group gap="lg" mt="lg">
                <Button component={Link} to={docsHref("docs/get-started.md")} h={44}>Get started</Button>
                <Anchor href={USE_CASES_FOOTER.examplesHref} className="hookway-scenario-library">
                  Browse all examples <span aria-hidden="true">↗</span>
                </Anchor>
              </Group>
            </div>
          </div>
        </header>
        <div className="hookway-scenario-explorer">
          <nav className="hookway-scenario-rail" aria-label="Use case patterns">
            {SCENARIO_GROUPS.map((group) => (
              <div key={group.label} className="hookway-scenario-group">
                <h2 className="hookway-scenario-group-label">{group.label}</h2>
                {group.items.map((item) => (
                  <Link key={item.id} to={`#${item.id}`} preventScrollReset
                    aria-current={selected === item.id ? "true" : undefined}
                    className="hookway-scenario-choice">
                    {item.label}<span aria-hidden="true">→</span>
                  </Link>
                ))}
              </div>
            ))}
          </nav>
          <div className="hookway-scenario-mobile-choice">
            <label htmlFor="use-case-choice">Choose a use case</label>
            <select id="use-case-choice" value={selected}
              onChange={(event) => navigate(`#${event.currentTarget.value}`, { preventScrollReset: true })}>
              {SCENARIO_GROUPS.map((group) => (
                <optgroup key={group.label} label={group.label}>
                  {group.items.map((item) => <option key={item.id} value={item.id}>{item.label}</option>)}
                </optgroup>
              ))}
            </select>
          </div>
          <div className="hookway-scenario-panels">
            {SCENARIO_GROUPS.flatMap((group) => group.items.map((item) => {
              const scenario = USE_CASES.find((value) => value.id === item.id)!;
              const diagram = SCENARIO_DIAGRAMS[item.id];
              return (
                <article key={item.id} id={item.id} className="hookway-scenario-panel"
                  hidden={selected !== item.id} aria-labelledby={`${item.id}-title`}>
                  <div className="hookway-scenario-narrative">
                    <Text className="hookway-scenario-context">{group.label}</Text>
                    <Title order={3} id={`${item.id}-title`}>{scenario.title}</Title>
                    <Text c={DARK.muted} className="hookway-scenario-summary">{scenario.summary}</Text>
                  </div>
                  <figure className="hookway-scenario-figure">
                    <div className="hookway-scenario-figure-heading"><span>Workflow</span><span>Hookway</span></div>
                    <div className="hookway-scenario-canvas"><UseCaseDiagram diagram={diagram} /></div>
                    <figcaption>{diagram.caption}</figcaption>
                  </figure>
                  <div className="hookway-scenario-source">
                    <Anchor href={scenario.exampleHref} className="hookway-scenario-config">
                      <span aria-hidden="true">{"</>"}</span> {scenario.exampleLabel}
                    </Anchor>
                    {guidePaths[item.id] && (
                      <Anchor component={Link} to={docsHref(guidePaths[item.id])}>
                        Follow the complete guide <span aria-hidden="true">→</span>
                      </Anchor>
                    )}
                  </div>
                  <details className="hookway-scenario-detail">
                    <summary>How this pattern works</summary>
                    <Text size="sm">{scenario.detail}</Text>
                  </details>
                </article>
              );
            }))}
          </div>
        </div>
      </Container>
      <CtaBand />
    </MarketingShell>
  );
}
