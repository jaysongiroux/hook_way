import { DARK } from "../tokens";

export const PALETTE_ITEM_CLASS = "hookway-palette-item";

export const BUILDER_CSS = `
.hookway-builder-root {
  color-scheme: light;
}
.hookway-builder-root ::selection {
  background: ${DARK.accent};
  color: ${DARK.surface};
}
.hookway-builder-root * {
  scrollbar-color: ${DARK.border} transparent;
  scrollbar-width: thin;
}
.hookway-builder-root *::-webkit-scrollbar {
  width: 10px;
  height: 10px;
}
.hookway-builder-root *::-webkit-scrollbar-thumb {
  background: ${DARK.border};
  background-clip: padding-box;
  border: 2px solid transparent;
  border-radius: 999px;
}
.hookway-builder-root :focus-visible {
  outline: 2px solid ${DARK.accent};
  outline-offset: 2px;
}
.${PALETTE_ITEM_CLASS} {
  background: ${DARK.surface};
  border: 1px solid ${DARK.border};
  border-radius: 4px;
  cursor: grab;
  transition: background-color 120ms ease-out, border-color 120ms ease-out;
}
.${PALETTE_ITEM_CLASS}:hover {
  border-color: ${DARK.accent};
  background-color: ${DARK.recessed};
}
.${PALETTE_ITEM_CLASS}:active {
  cursor: grabbing;
}
.hookway-inspector-node-list {
  overflow: hidden;
  border: 1px solid ${DARK.border};
  border-radius: 4px;
  background: ${DARK.surface};
}
.hookway-inspector-node + .hookway-inspector-node {
  border-top: 1px solid ${DARK.border};
}
.hookway-inspector-node:hover {
  background: ${DARK.recessed};
}
.hookway-builder-root .react-flow__controls-button {
  color: ${DARK.text};
  background: ${DARK.surface};
  border-bottom-color: ${DARK.border};
}
.hookway-builder-root .react-flow__controls-button:hover:not(:disabled) {
  background: ${DARK.recessed};
}
.hookway-builder-root .react-flow__controls-button svg {
  fill: currentColor;
}
.hookway-builder-root .react-flow__attribution {
  color: ${DARK.muted};
  background: transparent;
}
`;
