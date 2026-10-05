import type { IconType } from "react-icons";
import { TbBox } from "react-icons/tb";
import { nodeDescriptor } from "./nodeDescriptors";
import { destinationPresentation } from "../shared/ui/destinationIdentity";
import { DARK } from "../tokens";

export function nodeTypeIcon(type: string): IconType {
  return nodeDescriptor(type)?.icon ?? TbBox;
}

/** Publish shows the destination it writes to; every other node shows its type. */
export function nodeGlyph(
  nodeType: string,
  destinationType?: string,
): IconType {
  if (nodeType === "publish" && destinationType)
    return destinationPresentation(destinationType).icon;
  return nodeTypeIcon(nodeType);
}

export function destinationTypeOf(
  destinations: Record<string, unknown>,
  name: string,
): string {
  const entry = destinations[name];
  if (!entry || typeof entry !== "object" || !("type" in entry)) return "";
  const type = (entry as { type?: unknown }).type;
  return typeof type === "string" ? type : "";
}

/** Smaller than the id + type block so the mark stays secondary to the name. */
export const NODE_ICON_SIZE = 24;

export function NodeGlyph({
  nodeType,
  destinationType,
}: {
  nodeType: string;
  destinationType?: string;
}) {
  const Icon = nodeGlyph(nodeType, destinationType);
  const key =
    nodeType === "publish" && destinationType ? destinationType : nodeType;
  return (
    <span
      data-node-icon={key}
      style={{
        display: "inline-flex",
        flexShrink: 0,
        color: DARK.text,
        lineHeight: 0,
      }}
    >
      <Icon aria-hidden="true" focusable="false" size={NODE_ICON_SIZE} />
    </span>
  );
}
