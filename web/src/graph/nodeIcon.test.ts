import { expect, test } from "vitest";
import { destinationPresentation } from "../shared/ui/destinationIdentity";
import { NODE_TYPES } from "./nodeSchema";
import { destinationTypeOf, nodeGlyph, nodeTypeIcon } from "./nodeIcon";

test("every catalog node type has its own icon", () => {
  const seen = new Set<unknown>();
  for (const type of NODE_TYPES) {
    const icon = nodeTypeIcon(type);
    expect(icon, type).toBeTypeOf("function");
    expect(seen.has(icon), `${type} shares an icon with another type`).toBe(false);
    seen.add(icon);
  }
});

test("a publish node takes its icon from the destination it writes to", () => {
  expect(nodeGlyph("publish", "sqs")).toBe(destinationPresentation("sqs").icon);
  expect(nodeGlyph("publish", "kafka")).toBe(destinationPresentation("kafka").icon);
  expect(nodeGlyph("publish", "sqs")).not.toBe(nodeGlyph("publish", "kafka"));
  expect(nodeGlyph("publish")).toBe(nodeTypeIcon("publish"));
  expect(nodeGlyph("filter", "sqs")).toBe(nodeTypeIcon("filter"));
});

test("destinationTypeOf reads the type of a named destination", () => {
  const destinations = { orders: { type: "sqs" }, bare: { target: "x" } };
  expect(destinationTypeOf(destinations, "orders")).toBe("sqs");
  expect(destinationTypeOf(destinations, "bare")).toBe("");
  expect(destinationTypeOf(destinations, "missing")).toBe("");
});
