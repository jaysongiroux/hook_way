import { DARK } from "../tokens";

export const PALETTE_ITEM_CLASS = "hookway-palette-item";

export const BUILDER_CSS = `
.${PALETTE_ITEM_CLASS} {
  background: ${DARK.surface};
  border: 1px solid ${DARK.border};
  border-radius: 4px;
  cursor: grab;
}
.${PALETTE_ITEM_CLASS}:hover {
  border-color: ${DARK.accent};
  background-color: ${DARK.recessed};
}
.${PALETTE_ITEM_CLASS}:active {
  cursor: grabbing;
}
`;
