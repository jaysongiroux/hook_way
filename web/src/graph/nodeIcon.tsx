import type { IconType } from "react-icons";
import {
  TbArrowBackUp,
  TbArrowsSplit,
  TbBox,
  TbCode,
  TbDatabaseSearch,
  TbFileText,
  TbFilter,
  TbGauge,
  TbGitBranch,
  TbApi,
  TbHistoryOff,
  TbListCheck,
  TbLockOpen,
  TbPackage,
  TbRepeatOff,
  TbSend,
  TbShieldLock,
  TbTransform,
  TbWebhook,
} from "react-icons/tb";
import { destinationPresentation } from "../marketing/destinationIdentity";
import { DARK } from "../tokens";

/** One shape per catalog type, so a node is recognizable before its label is read. */
const NODE_TYPE_ICONS: Record<string, IconType> = {
  auth: TbShieldLock,
  code: TbCode,
  envelope: TbPackage,
  filter: TbFilter,
  http_request: TbApi,
  http_trigger: TbWebhook,
  if_else: TbGitBranch,
  ip_allowlist: TbListCheck,
  lookup: TbDatabaseSearch,
  loop_guard: TbRepeatOff,
  no_auth: TbLockOpen,
  publish: TbSend,
  rate_limit: TbGauge,
  read_body: TbFileText,
  replay_guard: TbHistoryOff,
  respond: TbArrowBackUp,
  switch: TbArrowsSplit,
  transform: TbTransform,
};

export function nodeTypeIcon(type: string): IconType {
  return NODE_TYPE_ICONS[type] ?? TbBox;
}

/** Publish shows the destination it writes to; every other node shows its type. */
export function nodeGlyph(nodeType: string, destinationType?: string): IconType {
  if (nodeType === "publish" && destinationType) return destinationPresentation(destinationType).icon;
  return nodeTypeIcon(nodeType);
}

export function destinationTypeOf(destinations: Record<string, unknown>, name: string): string {
  const entry = destinations[name];
  if (!entry || typeof entry !== "object" || !("type" in entry)) return "";
  const type = (entry as { type?: unknown }).type;
  return typeof type === "string" ? type : "";
}

/** Smaller than the id + type block so the mark stays secondary to the name. */
export const NODE_ICON_SIZE = 24;

export function NodeGlyph({ nodeType, destinationType }: { nodeType: string; destinationType?: string }) {
  const Icon = nodeGlyph(nodeType, destinationType);
  const key = nodeType === "publish" && destinationType ? destinationType : nodeType;
  return (
    <span data-node-icon={key} style={{ display: "inline-flex", flexShrink: 0, color: DARK.text, lineHeight: 0 }}>
      <Icon aria-hidden="true" focusable="false" size={NODE_ICON_SIZE} />
    </span>
  );
}
