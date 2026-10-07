import { expect, test } from "vitest";
import { humanLabel } from "../shared/ui/text";
import {
  labelledPorts,
  NODE_DESCRIPTORS,
  nodeDescriptor,
  nodeLabel,
  nodeSummary,
} from "./nodeDescriptors";
import { defaultNodeOptions, NODE_TYPES } from "./nodeSchema";

test("the generated catalog and authoring descriptors cover the same nodes", () => {
  expect(Object.keys(NODE_DESCRIPTORS).sort()).toEqual(NODE_TYPES);
  for (const type of NODE_TYPES) {
    const descriptor = nodeDescriptor(type)!;
    expect(descriptor.editor, type).toBeTruthy();
    if (descriptor.editor === "simple") {
      expect(descriptor.simple?.primaryFields.length, type).toBeGreaterThan(0);
    }
  }
});

test("new nodes receive independent nested starting values", () => {
  const firstAuth = defaultNodeOptions("auth", "first");
  const firstFilter = defaultNodeOptions("filter", "first");
  const firstSwitch = defaultNodeOptions("switch", "first");
  (firstAuth.source as Record<string, unknown>).key = "X-Changed";
  (firstFilter.conditions as Record<string, unknown>[])[0].path = "changed";
  (firstSwitch.cases as Record<string, unknown>[])[0].port = "changed";
  expect(defaultNodeOptions("auth", "second").source).toEqual({ kind: "header", key: "X-Signature" });
  expect(defaultNodeOptions("filter", "second").conditions).toEqual([{ source: "body", path: "", operator: "eq" }]);
  expect(labelledPorts("switch", defaultNodeOptions("switch", "second"))).toEqual(["matched", "default"]);
  expect(defaultNodeOptions("http_trigger", "second").path).toBe("/webhooks/second");
});

test("canvas summaries and labelled outputs retain their configured meaning", () => {
  expect(defaultNodeOptions("sample", "first")).toEqual({ rate: 1 });
  expect(nodeSummary("sample", { rate: 0.2 })).toBe("20% kept");
  expect(nodeSummary("sample", { rate: 0.001 })).toBe("0.1% kept");
  expect(nodeSummary("sample", { rate: 0.00001 })).toBe("0.001% kept");
  expect(nodeSummary("sample", { rate: 0 })).toBe("0% kept");
  expect(nodeSummary("sample", { rate: 1 })).toBe("100% kept");
  expect(nodeSummary("sample", {})).toBe("");
  expect(nodeSummary("publish", { destination: "events", target: "orders" })).toBe("events → orders");
  expect(nodeSummary("filter", { conditions: [{ path: "event.type", operator: "eq" }] })).toBe("event.type eq");
  expect(nodeSummary("respond", { passthrough: true, status: 200 })).toBe("passthrough");
  expect(labelledPorts("if_else", {})).toEqual(["true", "false"]);
  expect(labelledPorts("switch", { cases: [{ port: "first" }, { port: "second" }], default: "second" })).toEqual(["first", "second"]);
});

test.each(["future_node", "toString", "constructor", "__proto__"])("unknown node %s uses generic presentation", (type) => {
  expect(nodeDescriptor(type)).toBeUndefined();
  expect(nodeLabel(type)).toBe(humanLabel(type));
  expect(nodeSummary(type, {})).toBe("");
  expect(labelledPorts(type, {})).toEqual([]);
});
