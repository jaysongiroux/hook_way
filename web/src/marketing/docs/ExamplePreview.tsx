import { useEffect, useMemo, useState } from "react";
import { Code, Group, Loader, Modal, NativeSelect, Stack, Text } from "@mantine/core";
import { stringify } from "yaml";
import { GraphView } from "../../graph/GraphView";
import { yamlIdOf, type FlowSnapshot, type GraphDocument, type HookwayNode } from "../../graph/model";
import { nodeLabel, nodeMeta } from "../../graph/nodeSchema";
import { nodeHelp } from "../../graph/help";
import { loadExamplePreview, type PublishedYAML } from "./loadExamplePreview";
import "./examplePreview.css";

function NodeDetails({ node, destinations }: { node: HookwayNode; destinations: Record<string, unknown> }) {
  const meta = nodeMeta(node.data.nodeType, node.data.options);
  const destination = node.data.nodeType === "publish" ? String(node.data.options.destination ?? "") : "";
  return (
    <Stack gap="md">
      <Text size="sm" c="dimmed">Read-only example</Text>
      <div>
        <Text fw={600}>{nodeLabel(node.data.nodeType)}</Text>
        <Text size="sm">Node ID: <code>{yamlIdOf(node)}</code></Text>
        <Text size="sm">YAML type: <code>{node.data.nodeType}</code></Text>
        {meta && <Text size="sm">{meta.input} → {meta.output}{meta.detached ? " · delivery step" : ""}</Text>}
      </div>
      {nodeHelp(node.data.nodeType) && <Text size="sm">{nodeHelp(node.data.nodeType)}</Text>}
      {node.data.description && <div>
        <Text fw={600} size="sm">Description</Text>
        <Text size="sm" style={{ whiteSpace: "pre-wrap" }}>{node.data.description}</Text>
      </div>}
      <div>
        <Text fw={600} size="sm" mb="xs">Configuration</Text>
        <Code block>{stringify(node.data.options)}</Code>
      </div>
      {destination && Object.prototype.hasOwnProperty.call(destinations, destination) && <div>
        <Text fw={600} size="sm" mb="xs">Destination: {destination}</Text>
        <Code block>{stringify(destinations[destination])}</Code>
      </div>}
    </Stack>
  );
}

function FlowPreview({ graph, destinations }: { graph: FlowSnapshot; destinations: Record<string, unknown> }) {
  const [selectedId, setSelectedId] = useState<string | null>(null);
  const [selectedEdgeId, setSelectedEdgeId] = useState<string | null>(null);
  const [opened, setOpened] = useState(false);
  const node = graph.nodes.find((item) => item.id === selectedId);
  return (
    <>
      <div className="docs-example-canvas">
        {graph.nodes.length ? <GraphView
          mode="readOnly"
          graph={graph}
          destinations={destinations}
          selectedNodeId={selectedId}
          selectedEdgeId={selectedEdgeId}
          onNodeSelect={(id) => { setSelectedId(id); setSelectedEdgeId(null); }}
          onNodeActivate={() => setOpened(true)}
          onEdgeSelect={(id) => { setSelectedEdgeId(id); setSelectedId(null); }}
        /> : <Text p="md" size="sm">This flow has no nodes.</Text>}
      </div>
      <Modal
        opened={opened && !!node}
        onClose={() => setOpened(false)}
        title={"Node details: " + (node ? yamlIdOf(node) : "")}
        closeButtonProps={{ "aria-label": "Close node details" }}
        size="lg"
      >
        {node && <NodeDetails node={node} destinations={destinations} />}
      </Modal>
    </>
  );
}

function DocumentPreview({ document }: { document: GraphDocument }) {
  const [flow, setFlow] = useState(document.activeFlow);
  const names = [document.activeFlow, ...Object.keys(document.otherFlows)];
  const graph = useMemo(() => flow === document.activeFlow
    ? { nodes: document.nodes, edges: document.edges }
    : document.otherFlows[flow], [document, flow]);
  return (
    <section className="docs-example-preview" aria-label="Example graph preview">
      <Group justify="space-between" gap="sm" mb="xs">
        <Text component="h2" size="md" fw={600} m={0}>Graph preview</Text>
        {names.length > 1 && <NativeSelect label="Flow" data={names} value={flow} onChange={(event) => setFlow(event.currentTarget.value)} />}
      </Group>
      <Text size="xs" c="dimmed" mb="sm">Read-only · Drag or scroll to pan. Use the controls to zoom or fit. Select a node for details.</Text>
      <FlowPreview key={flow} graph={graph} destinations={document.destinations} />
    </section>
  );
}

export default function ExamplePreview({ page, publishedFiles }: { page: PublishedYAML; publishedFiles: readonly PublishedYAML[] }) {
  const [result, setResult] = useState<{ document: GraphDocument | null; failed: boolean }>();
  useEffect(() => {
    const controller = new AbortController();
    void loadExamplePreview(page, publishedFiles, controller.signal)
      .then((document) => { if (!controller.signal.aborted) setResult({ document, failed: false }); })
      .catch(() => { if (!controller.signal.aborted) setResult({ document: null, failed: true }); });
    return () => controller.abort();
  }, [page, publishedFiles]);
  if (!result) return <Loader size="sm" my="md" aria-label="Loading graph preview" />;
  if (result.failed) return <Text role="status" size="sm" my="md">Graph preview unavailable. The YAML source is available below.</Text>;
  return result.document ? <DocumentPreview document={result.document} /> : null;
}
