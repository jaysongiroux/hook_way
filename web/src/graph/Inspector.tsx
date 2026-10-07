import { useEffect, useState } from "react";
import {
  Alert,
  Button,
  Group,
  Select,
  Stack,
  Text,
  Textarea,
  TextInput,
} from "@mantine/core";
import { useGraphStore } from "./store";
import { nodeMeta, nodeLabel } from "./nodeSchema";
import { nodeHelp, portLabel } from "./help";
import { FlowResponseEditor } from "./FlowResponseEditor";
import { NodeReadiness } from "./NodeReadiness";
import { NodeOptionsForm } from "./NodeOptionsForm";
import { nodeScope } from "./formDrafts";
import { Sections, type SectionSpec } from "./InspectorSections";
import { yamlIdOf } from "./model";
import { DARK } from "../tokens";

export function Inspector() {
  const selectedId = useGraphStore((s) => s.selectedId);
  const node = useGraphStore((s) => s.nodes.find((n) => n.id === s.selectedId));
  const updateNodeData = useGraphStore((s) => s.updateNodeData);
  const selectedEdgeId = useGraphStore((s) => s.selectedEdgeId);
  const edge = useGraphStore((s) =>
    s.edges.find((e) => e.id === s.selectedEdgeId),
  );
  const nodes = useGraphStore((s) => s.nodes);
  const activeFlow = useGraphStore((s) => s.activeFlow);
  const deliveryFailurePolicy = useGraphStore((s) => s.deliveryFailurePolicy);
  const captureFailures = useGraphStore((s) => s.flowExtra?.capture_failures);
  const setDeliveryFailurePolicy = useGraphStore(
    (s) => s.setDeliveryFailurePolicy,
  );
  const setSelected = useGraphStore((s) => s.setSelected);

  if (selectedEdgeId && edge) {
    const from = nodes.find((n) => n.id === edge.source);
    const to = nodes.find((n) => n.id === edge.target);
    const port =
      edge.sourceHandle && edge.sourceHandle !== "out"
        ? `:${edge.sourceHandle}`
        : "";
    return (
      <Stack gap="sm" data-testid="inspector">
        <Text
          fw={600}
          size="xs"
          tt="uppercase"
          c="dimmed"
          style={{ letterSpacing: "0.08em" }}
        >
          Edge
        </Text>
        <Text fw={600} size="sm" ff="monospace">
          {from ? yamlIdOf(from) : "?"}
          {port}&gt;{to ? yamlIdOf(to) : "?"}
        </Text>
        <Text size="xs" c="dimmed">
          Nodes connect only when their input and output types match. Drag a new
          connection to change this path.
        </Text>
      </Stack>
    );
  }

  if (!selectedId || !node) {
    return (
      <Stack gap="sm" data-testid="inspector">
        <Text
          fw={600}
          size="xs"
          tt="uppercase"
          c="dimmed"
          style={{ letterSpacing: "0.08em" }}
        >
          Flow
        </Text>
        <Text size="sm" c="dimmed">
          Double-click a node to configure it. These settings apply to the whole
          flow.
        </Text>
        <FlowNameField />
        <Select
          label="Delivery failure policy"
          description="Controls publish failures. Continue after failures tries every publish. The default response is 202 when at least one succeeds; HTTP response policies can override it."
          data={[
            { value: "", label: "Use default (stop on first failure)" },
            { value: "stop_on_failure", label: "Stop on first failure" },
            { value: "skip_failed", label: "Continue after failures" },
          ]}
          value={deliveryFailurePolicy ?? ""}
          allowDeselect={false}
          comboboxProps={{ keepMounted: false }}
          onChange={(v) => setDeliveryFailurePolicy(v ?? "")}
        />
        <FlowResponseEditor />
        {deliveryFailurePolicy === "skip_failed" && (
          <Alert
            color="orange"
            title="Partial delivery can return success"
            styles={{ title: { color: DARK.text } }}
          >
            With the default response policy, one successful publication is
            enough for HTTP 202. Failed destinations may miss the event. Replay
            capture is best effort; sender retries can repeat successful
            deliveries.
          </Alert>
        )}
        {captureFailures === false && (
          <Text size="sm" c="dimmed">
            Failure capture is disabled for this flow. Inspect nodes still collect observations when Replay is configured. Plan recovery separately;
            batch flows do not support replay capture.
          </Text>
        )}
        {nodes.length > 0 && (
          <Stack gap={6}>
            <Text
              fw={600}
              size="xs"
              tt="uppercase"
              c="dimmed"
              style={{ letterSpacing: "0.08em" }}
            >
              Nodes
            </Text>
            <Stack gap={0} className="hookway-inspector-node-list">
              {nodes.map((n) => (
                <Button
                  className="hookway-inspector-node"
                  key={n.id}
                  size="xs"
                  variant="subtle"
                  color="dark"
                  radius={0}
                  fullWidth
                  justify="space-between"
                  aria-label={`Select ${yamlIdOf(n)}`}
                  onClick={() => setSelected(n.id)}
                >
                  <Group
                    justify="space-between"
                    wrap="nowrap"
                    gap="xs"
                    w="100%"
                  >
                    <Text ff="monospace" size="xs" c="dark">
                      {yamlIdOf(n)}
                    </Text>
                    <Text size="xs" c="dimmed">
                      {nodeLabel(n.data.nodeType)}
                    </Text>
                  </Group>
                </Button>
              ))}
            </Stack>
          </Stack>
        )}
      </Stack>
    );
  }

  const meta = nodeMeta(node.data.nodeType, node.data.options);
  const description = node.data.description ?? "";
  const setOptions = (options: Record<string, unknown>) =>
    updateNodeData(node.id, { options });
  const sections: SectionSpec[] = [
    {
      value: "metadata",
      label: "Identity and description",
      render: () => (
        <Stack gap="sm">
          <TextInput
            label="Node ID"
            description="Unique name used in YAML connections, for example in>out."
            value={yamlIdOf(node)}
            onChange={(e) =>
              updateNodeData(node.id, { yamlId: e.currentTarget.value })
            }
          />
          <Textarea
            label="Description"
            description="Optional note exported with this node."
            value={description}
            autosize
            minRows={2}
            onChange={(e) =>
              updateNodeData(node.id, { description: e.currentTarget.value })
            }
          />
        </Stack>
      ),
    },
  ];

  return (
    <Stack gap="sm" data-testid="inspector">
      <Text
        fw={600}
        size="xs"
        tt="uppercase"
        c="dimmed"
        style={{ letterSpacing: "0.08em" }}
      >
        {nodeLabel(node.data.nodeType)}
      </Text>
      {nodeHelp(node.data.nodeType) && (
        <Text size="sm" c="dimmed" lh={1.4}>
          {nodeHelp(node.data.nodeType)}
        </Text>
      )}
      <Text size="xs" c="dimmed">
        YAML type:{" "}
        <Text component="code" inherit>
          {node.data.nodeType}
        </Text>
        {meta ? ` · ${portLabel(meta.input)} → ${portLabel(meta.output)}` : ""}
        {meta?.detached ? " · delivery step" : ""}
      </Text>
      <NodeReadiness nodeId={node.id} />
      <NodeOptionsForm
        key={`options/${node.id}`}
        nodeType={node.data.nodeType}
        value={node.data.options}
        onChange={setOptions}
        scope={nodeScope(activeFlow, node.id)}
        nodeId={yamlIdOf(node)}
      />
      <Sections
        key={`metadata/${node.id}`}
        sections={sections}
        defaultOpen={[]}
      />
    </Stack>
  );
}

function FlowNameField() {
  const activeFlow = useGraphStore((s) => s.activeFlow);
  const otherFlows = useGraphStore((s) => s.otherFlows);
  const renameFlow = useGraphStore((s) => s.renameFlow);
  const [draft, setDraft] = useState(activeFlow);
  const [error, setError] = useState("");

  useEffect(() => {
    setDraft(activeFlow);
    setError("");
  }, [activeFlow]);

  return (
    <TextInput
      label="Flow name"
      description="Name of this flow in YAML. Set its public URL in the HTTP endpoint node."
      value={draft}
      error={error || undefined}
      onChange={(e) => {
        const next = e.currentTarget.value;
        setDraft(next);
        const trimmed = next.trim();
        if (!trimmed) {
          setError("");
          return;
        }
        if (trimmed !== activeFlow && otherFlows[trimmed]) {
          setError("That flow already exists.");
          return;
        }
        setError("");
        renameFlow(trimmed);
      }}
      onBlur={() => {
        setDraft(activeFlow);
        setError("");
      }}
    />
  );
}
