import { Anchor, Box, SimpleGrid, Text, Title } from "@mantine/core";
import { BENCHMARK_RESULTS, PRODUCTION_CARDS } from "./productionCopy";
import { Section } from "./Section";
import { DARK } from "../tokens";
import { docsHref } from "./links";

export function Production() {
  return (
    <Section
      title="Control load, monitor delivery, and recover failures"
      lead="Size your deployment with published benchmarks. Then watch request latency, delivery outcomes and capacity pressure with Prometheus and the included Grafana dashboards."
      background={DARK.recessed}
    >
      <Box className="hookway-benchmark">
        <Title order={3} size="h4">
          Measured under burst and sustained traffic
        </Title>
        <Text size="sm" c={DARK.muted} mt={8}>
          October 1, 2026 · local Docker · two replicas, each with 2 vCPU and 4
          GiB · synthetic destination.
        </Text>
        <Box component="dl" className="hookway-benchmark-results">
          {BENCHMARK_RESULTS.map((result) => (
            <Box key={result.label}>
              <Text component="dt" size="sm" fw={600}>
                {result.label}
              </Text>
              <Box component="dd" m={0}>
                <Text className="hookway-benchmark-value">{result.value}</Text>
                <Text size="sm" c={DARK.muted}>
                  {result.detail}
                </Text>
              </Box>
            </Box>
          ))}
        </Box>
        <Text size="sm" c={DARK.muted}>
          Recorded ingress workloads, not a production capacity guarantee.
          Shared protection, replay and MCP require separate sizing.{" "}
          <Anchor href={docsHref("docs/evidence/performance.md")}>
            Results and methodology
          </Anchor>
        </Text>
      </Box>
      <SimpleGrid cols={{ base: 1, sm: 3 }} spacing={32} mt={32}>
        <Box>
          <Title order={3} size="h5">
            Find the bottleneck
          </Title>
          <Text size="sm" c={DARK.muted} mt={8}>
            Track request and node latency, active requests and admission
            rejections to see where traffic slows down.
          </Text>
        </Box>
        <Box>
          <Title order={3} size="h5">
            Watch delivery outcomes
          </Title>
          <Text size="sm" c={DARK.muted} mt={8}>
            Monitor publication errors and retries. Enable replay to investigate
            and recover captured failures.
          </Text>
        </Box>
        <Box>
          <Title order={3} size="h5">
            Understand tool calls
          </Title>
          <Text size="sm" c={DARK.muted} mt={8}>
            Track MCP call outcomes and duration by flow and tool, without
            putting payloads in metrics.
          </Text>
        </Box>
      </SimpleGrid>
      <Box component="details" className="hookway-details" mt={32}>
        <summary>Operational defaults and deployment options</summary>
        <Box component="dl" className="hookway-defaults">
          {PRODUCTION_CARDS.map((item) => (
            <Box key={item.id} className="hookway-capability-row">
              <Box component="dt">
                <Text fw={600} size="sm">
                  {item.title}
                </Text>
                <Text size="sm" c={DARK.muted} mt={4}>
                  {item.value} {item.valueLabel}
                </Text>
              </Box>
              <Text component="dd" m={0} size="sm" c={DARK.muted}>
                {item.line}
              </Text>
            </Box>
          ))}
        </Box>
      </Box>
    </Section>
  );
}
