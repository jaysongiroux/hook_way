import { Modal, Code, Group, Button, Alert, ScrollArea, Stack, Text, List } from "@mantine/core";
import { useEffect, useMemo, useState } from "react";
import { useGraphStore } from "./store";
import { toConfig, ExportError } from "./toConfig";
import { toYaml } from "./toYaml";
import { validateYaml, type ValidationIssue } from "./validate";
import type { GraphDocument } from "./model";

export function ExportModal({ opened, onClose }: { opened: boolean; onClose: () => void }) {
  const nodes = useGraphStore((s) => s.nodes);
  const edges = useGraphStore((s) => s.edges);
  const serverConfig = useGraphStore((s) => s.serverConfig);
  const configExtra = useGraphStore((s) => s.configExtra);
  const connections = useGraphStore((s) => s.connections);
  const destinations = useGraphStore((s) => s.destinations);
  const activeFlow = useGraphStore((s) => s.activeFlow);
  const otherFlows = useGraphStore((s) => s.otherFlows);
  const deliveryFailurePolicy = useGraphStore((s) => s.deliveryFailurePolicy);

  const exported = useMemo(() => {
    const doc: GraphDocument = {
      nodes,
      edges,
      serverConfig,
      configExtra,
      connections,
      destinations,
      activeFlow,
      otherFlows,
      deliveryFailurePolicy,
    };
    try {
      const { config } = toConfig(doc);
      return { yaml: toYaml(config), error: "" };
    } catch (e) {
      return { yaml: "", error: e instanceof ExportError ? e.message : String(e) };
    }
  }, [nodes, edges, serverConfig, configExtra, connections, destinations, activeFlow, otherFlows, deliveryFailurePolicy]);

  const [issues, setIssues] = useState<ValidationIssue[] | null>(null);
  useEffect(() => {
    if (!opened || exported.error || !exported.yaml) {
      setIssues(null);
      return;
    }
    let cancelled = false;
    void validateYaml(exported.yaml).then((got) => {
      if (!cancelled) setIssues(got);
    });
    return () => {
      cancelled = true;
    };
  }, [opened, exported.yaml, exported.error]);

  const blocking = (issues ?? []).filter((i) => i.severity !== "warning");
  const download = () => {
    const blob = new Blob([exported.yaml], { type: "text/yaml" });
    const url = URL.createObjectURL(blob);
    const a = document.createElement("a");
    a.href = url;
    a.download = "hookway.yaml";
    document.body.appendChild(a);
    a.click();
    a.remove();
    setTimeout(() => URL.revokeObjectURL(url), 0);
  };

  const copy = () => {
    navigator.clipboard?.writeText(exported.yaml).catch(() => {
      /* clipboard unavailable */
    });
  };

  return (
    <Modal opened={opened} onClose={onClose} title="Export config" size="lg">
      {exported.error ? (
        <Alert color="red" title="Cannot export">
          {exported.error}
        </Alert>
      ) : (
        <Stack gap="sm">
          <Text size="sm" c="dimmed">
            This builder generates YAML for you to deploy. It does not connect to a running Hookway instance.
          </Text>
          {issues && blocking.length > 0 && (
            <Alert color="red" title="Validation errors" data-testid="validation-errors">
              <List size="sm">
                {blocking.map((iss, i) => (
                  <List.Item key={i}>
                    <Text span ff="monospace">
                      {iss.field}
                    </Text>
                    : {iss.message}
                  </List.Item>
                ))}
              </List>
            </Alert>
          )}
          {issues && blocking.length === 0 && (
            <Alert color="green" title="Valid" data-testid="validation-ok">
              This config passes <Text span ff="monospace">hookway validate</Text>.
            </Alert>
          )}
          <ScrollArea h={400}>
            <Code block>{exported.yaml}</Code>
          </ScrollArea>
          <Group mt="md" justify="flex-end">
            <Button variant="default" onClick={copy}>
              Copy
            </Button>
            <Button onClick={download}>Download hookway.yaml</Button>
          </Group>
        </Stack>
      )}
    </Modal>
  );
}
