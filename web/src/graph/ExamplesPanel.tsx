import { useEffect, useMemo, useState } from "react";
import { Alert, Box, Button, Group, Modal, NativeSelect, Radio, Stack, Text, TextInput } from "@mantine/core";
import type { RepositoryExample } from "./exampleCatalog";
import { fromBundle } from "./bundle";
import { useGraphStore } from "./store";
import { DARK } from "../tokens";
import { PALETTE_ITEM_CLASS } from "./builderStyles";

async function loadExamples() {
  return (await import("./repositoryExamples")).repositoryExamples;
}

export function ExamplesPanel({ onImported }: { onImported?: () => void }) {
  const [examples, setExamples] = useState<RepositoryExample[]>();
  const [error, setError] = useState("");
  const [attempt, setAttempt] = useState(0);
  const [search, setSearch] = useState("");
  const [selected, setSelected] = useState<RepositoryExample>();
  useEffect(() => {
    let cancelled = false;
    setError("");
    void loadExamples().then(
      (loaded) => { if (!cancelled) setExamples(loaded); },
      (error: unknown) => { if (!cancelled) setError(error instanceof Error ? error.message : String(error)); },
    );
    return () => { cancelled = true; };
  }, [attempt]);
  const query = search.trim().toLowerCase();
  const matches = examples?.filter((example) =>
    [example.label, example.id, ...example.flows.map((flow) => flow.name)]
      .some((value) => value.toLowerCase().includes(query)),
  );
  return (
    <Stack gap="sm">
      <Text size="xs" c={DARK.muted}>Start from a repository example. Import a flow’s nodes or load its complete core configuration.</Text>
      <TextInput size="xs" aria-label="Search examples" placeholder="Search examples..." value={search}
        onChange={(event) => setSearch(event.currentTarget.value)} />
      {error ? (
        <Alert color="red" title="Examples unavailable" role="alert">
          <Text size="xs">{error}</Text>
          <Button size="compact-xs" mt="sm" onClick={() => setAttempt((value) => value + 1)}>Retry</Button>
        </Alert>
      ) : !matches ? <Text size="sm" role="status">Loading examples…</Text> : matches.length === 0 ? (
        <Text size="sm" c={DARK.muted} role="status">{examples?.length ? `No examples match “${search.trim()}”.` : "No examples are available."}</Text>
      ) : matches.map((example) => (
        <Box key={example.id} className={PALETTE_ITEM_CLASS} p={10} style={{ cursor: "default" }}>
          <Group justify="space-between" align="flex-start" wrap="nowrap" gap="sm">
            <Text size="sm" fw={600} style={{ minWidth: 0, flex: 1 }}>{example.label}</Text>
            <Button size="compact-xs" variant="subtle" aria-label={`Import example ${example.id}`}
              onClick={() => setSelected(example)}>Import</Button>
          </Group>
          <Text size="xs" c={DARK.muted} mt={4} style={{ overflowWrap: "anywhere" }}>{example.id}</Text>
          <Text size="xs" c={DARK.muted} mt={4} style={{ overflowWrap: "anywhere" }}>
            Flows: {example.flows.map((flow) => flow.name).join(", ")}
          </Text>
        </Box>
      ))}
      {selected && <ExampleImportDialog key={selected.id} example={selected}
        onClose={() => setSelected(undefined)} onImported={onImported} />}
    </Stack>
  );
}

export function ExampleImportDialog({ example, onClose, onImported }: {
  example: RepositoryExample;
  onClose: () => void;
  onImported?: () => void;
}) {
  const activeFlow = useGraphStore((state) => state.activeFlow);
  const [mode, setMode] = useState("nodes");
  const [flow, setFlow] = useState(example.flows.find((item) => item.nodeCount > 0)?.name ?? "");
  const [error, setError] = useState("");
  const prepared = useMemo(() => {
    try { return { doc: fromBundle(example, null), error: "" }; }
    catch (error) { return { doc: undefined, error: error instanceof Error ? error.message : String(error) }; }
  }, [example]);
  const source = example.flows.find((item) => item.name === flow);
  const counts = mode === "nodes" ? source : {
    nodeCount: example.flows.reduce((sum, item) => sum + item.nodeCount, 0),
    edgeCount: example.flows.reduce((sum, item) => sum + item.edgeCount, 0),
  };
  const doImport = () => {
    try {
      if (!prepared.doc) throw new Error(prepared.error);
      const state = useGraphStore.getState();
      if (mode === "nodes") {
        const snapshot = flow === prepared.doc.activeFlow ? prepared.doc : prepared.doc.otherFlows[flow];
        if (!snapshot?.nodes.length) throw new Error("Select a flow with nodes to import.");
        state.appendExampleGraph({ nodes: snapshot.nodes, edges: snapshot.edges });
      } else {
        state.setGraph({ ...prepared.doc,
          replayServiceConfig: state.replayServiceConfig, replayFilename: state.replayFilename });
      }
      onClose();
      onImported?.();
    } catch (error) { setError(error instanceof Error ? error.message : String(error)); }
  };
  return (
    <Modal opened onClose={onClose} title={`Import ${example.label}`} size="md" closeButtonProps={{ "aria-label": "Close example import" }}>
      <Stack gap="md">
        <Text size="xs" c={DARK.muted} style={{ overflowWrap: "anywhere" }}>{example.id}</Text>
        <Radio.Group label="Import mode" value={mode} onChange={setMode}>
          <Stack gap="sm" mt="xs">
            <Radio value="nodes" label="Nodes only" />
            <Radio value="core" label="Nodes + core config" />
          </Stack>
        </Radio.Group>
        {mode === "nodes" && (example.flows.length > 1 ? (
          <NativeSelect label="Source flow" value={flow} onChange={(event) => setFlow(event.currentTarget.value)}
            data={example.flows.map((item) => ({ value: item.name, label: item.name, disabled: item.nodeCount === 0 }))} />
        ) : <Text size="sm">Flow: {flow}</Text>)}
        <Text size="sm">{counts?.nodeCount ?? 0} {counts?.nodeCount === 1 ? "node" : "nodes"} · {counts?.edgeCount ?? 0} {counts?.edgeCount === 1 ? "edge" : "edges"}</Text>
        {mode === "nodes" ? (
          <Alert role="note" title={`Append to ${activeFlow}`}>
            Nodes and edges are added to the active canvas. Core resources and flow settings are omitted.
            References are preserved and may bind to existing resources with matching names.
            Review destinations, credentials, stores, and other dependencies, then validate and rewire as needed.
          </Alert>
        ) : (
          <Alert role="note" color="yellow" title="Replace core configuration">
            All current core flows and settings will be replaced with this example’s configuration.
            The separate replay-service document is kept. Review environment variables before deployment.
          </Alert>
        )}
        {(prepared.error || error) && <Alert color="red" role="alert" title="Import failed">{prepared.error || error}</Alert>}
        <Group justify="flex-end">
          <Button variant="default" onClick={onClose}>Cancel</Button>
          <Button onClick={doImport} disabled={!prepared.doc || (mode === "nodes" && !source?.nodeCount)}>
            {mode === "nodes" ? "Import nodes" : "Replace core configuration"}
          </Button>
        </Group>
      </Stack>
    </Modal>
  );
}
