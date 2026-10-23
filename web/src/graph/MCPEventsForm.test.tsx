import { beforeEach, expect, test } from "vitest";
import { fireEvent, render, screen } from "@testing-library/react";
import { MantineProvider } from "@mantine/core";
import { MCPEventsForm } from "./MCPEventsForm";
import { EventsPanel } from "./EventsPanel";
import { useGraphStore } from "./store";
import { fromYaml } from "./fromYaml";
import { toYaml } from "./toYaml";
import { toConfig } from "./toConfig";
import { literalRoots } from "./literalYaml";
import { fromBundle, toBundle } from "./bundle";

beforeEach(() => useGraphStore.getState().clear());

test("configures durable delivery using secret references and disables it cleanly", () => {
  render(<MantineProvider><EventsPanel opened onClose={() => {}} /></MantineProvider>);
  fireEvent.click(screen.getByLabelText("Enable durable MCP delivery"));
  expect(useGraphStore.getState().configExtra.event_delivery).toMatchObject({ namespace: "agent-events", encryption_key: { provider: "env", name: "EVENT_ENCRYPTION_KEY" } });
  fireEvent.change(screen.getByLabelText("PostgreSQL URL"), { target: { value: "${CUSTOM_EVENT_DATABASE_URL}" } });
  expect(useGraphStore.getState().configExtra.event_delivery).toMatchObject({ postgres_url: "${CUSTOM_EVENT_DATABASE_URL}" });
  fireEvent.click(screen.getByLabelText("Enable durable MCP delivery"));
  expect(useGraphStore.getState().configExtra.event_delivery).toBeUndefined();
});

test("adds an exposed event, retains invalid schema drafts and removes its draft branch", () => {
  useGraphStore.setState({ configExtra: { events: { "issue.opened": { description: "Issue", payload_schema: { type: "object" } } } } });
  let value: Record<string, unknown> = { events: [], auth: { api_keys: { reader: {} } } };
  const change = (next: Record<string, unknown>) => { value = next; view.rerender(renderForm()); };
  const renderForm = () => <MantineProvider><MCPEventsForm value={value} onChange={change} scope="event-test" /></MantineProvider>;
  const view = render(renderForm());
  fireEvent.click(screen.getByRole("button", { name: "Add event subscription" }));
  expect(value.events).toMatchObject([{ event: "issue.opened", input_schema: { type: "object", additionalProperties: false } }]);
  fireEvent.change(screen.getByLabelText("Subscription argument schema"), { target: { value: '{"type":"object","minimum":9007199254740993}' } });
  expect(Object.values(useGraphStore.getState().formDrafts ?? {}).some((d) => d.error.includes("precision"))).toBe(true);
  expect(value.events).toMatchObject([{ input_schema: { type: "object", additionalProperties: false } }]);
  fireEvent.click(screen.getByRole("button", { name: "Remove event subscription 1" }));
  expect(value.events).toEqual([]);
  expect(Object.keys(useGraphStore.getState().formDrafts ?? {}).filter((k) => k.includes("/event/")).length).toBe(0);
});

test("subscription schemas preserve literals and precision in single and split YAML", () => {
  const yaml = `event_delivery: {postgres_url: '\u0024{EVENT_DATABASE_URL}', namespace: demo, encryption_key: {provider: env, name: EVENT_KEY}}
flows:
  agents:
    nodes:
      - id: mcp
        type: mcp_trigger
        events:
          - event: issue.opened
            input_schema:
              type: object
              additionalProperties: false
              properties: {id: {type: string, const: '\u0024{LITERAL}'}, n: {type: integer, minimum: 9007199254740992}}
            filters: {id: id, n: n}
            access: {api_keys: [reader]}
    edges: []
`;
  const doc = fromYaml(yaml);
  expect(toYaml(toConfig(doc).config, literalRoots(doc))).toContain("\\u0024{LITERAL}");
  doc.yamlMode = "split";
  const bundle = toBundle(doc);
  expect(bundle.files[bundle.entry]).toContain("event_delivery:");
  const restored = fromBundle(bundle);
  expect(restored.nodes[0].data.options.events).toEqual(doc.nodes[0].data.options.events);
  expect(restored.configExtra.event_delivery).toEqual(doc.configExtra.event_delivery);
  expect(() => fromYaml(yaml.replace("9007199254740992", "9007199254740993"))).toThrow(/precision/);
});
