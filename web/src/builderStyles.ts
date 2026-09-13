import { DARK } from "./tokens";

export const BUILDER_ROOT_CLASS = "hookway-builder";
export const BUILDER_TOOLBAR_CLASS = "hookway-builder-toolbar";
export const BUILDER_PALETTE_ITEM_CLASS = "hookway-palette-item";
export const BUILDER_FLOATING_CLASS = "hookway-floating-surface";
export const BUILDER_FLOATING_HEADER_CLASS = "hookway-floating-header";
export const BUILDER_OVERLAY_CLASS = "hookway-floating-overlay";

// The editor is intentionally denser and more industrial than the marketing
// page. These rules finish Mantine and React Flow's dark-mode treatment while
// keeping every surface on the shared Hookway navy palette.
export const BUILDER_CSS = `
.${BUILDER_ROOT_CLASS} {
  --hw-panel: #18253b;
  --hw-panel-raised: #1e2e49;
  --hw-input: #121d30;
  --hw-input-hover: #172640;
  --hw-canvas: #0b111b;
  --hw-border: #304563;
  --hw-border-strong: #466184;
  --hw-text: #f4f7fc;
  --hw-muted: #93a6c5;
  --hw-accent: ${DARK.accent};
  background: var(--hw-canvas);
  color: var(--hw-text);
}

.${BUILDER_ROOT_CLASS} .mantine-AppShell-header {
  background: rgba(18, 29, 48, 0.96);
  border-color: var(--hw-border);
  box-shadow: 0 8px 24px rgba(3, 8, 16, 0.24);
  backdrop-filter: blur(14px);
}

.${BUILDER_ROOT_CLASS} .mantine-AppShell-navbar,
.${BUILDER_ROOT_CLASS} .mantine-AppShell-aside {
  background: var(--hw-panel);
  border-color: var(--hw-border);
}

.${BUILDER_ROOT_CLASS} .mantine-AppShell-main {
  background: var(--hw-canvas);
}

.${BUILDER_TOOLBAR_CLASS} {
  padding: 3px;
  background: #0f1929;
  border: 1px solid var(--hw-border);
  border-radius: 9px;
}

.${BUILDER_TOOLBAR_CLASS} .mantine-Button-root {
  color: #c9d5e8;
  border-radius: 6px;
  transition: color 120ms ease, background-color 120ms ease;
}

.${BUILDER_TOOLBAR_CLASS} .mantine-Button-root:hover {
  color: var(--hw-text);
  background: var(--hw-panel-raised);
}

.${BUILDER_TOOLBAR_CLASS} .hookway-clear-action {
  color: #ff9aa6;
}

.${BUILDER_PALETTE_ITEM_CLASS} {
  position: relative;
  overflow: hidden;
  background: #142035;
  border-color: var(--hw-border);
  box-shadow: 0 6px 18px rgba(4, 10, 20, 0.12);
  transition: transform 140ms ease, border-color 140ms ease, background-color 140ms ease, box-shadow 140ms ease;
}

.${BUILDER_PALETTE_ITEM_CLASS}::before {
  content: "";
  position: absolute;
  inset: 0 auto 0 0;
  width: 3px;
  background: var(--node-accent);
}

.${BUILDER_PALETTE_ITEM_CLASS}:hover {
  transform: translateY(-1px);
  background: var(--hw-panel-raised);
  border-color: var(--node-accent);
  box-shadow: 0 10px 24px rgba(4, 10, 20, 0.24);
}

.${BUILDER_PALETTE_ITEM_CLASS}:active {
  transform: translateY(0);
  cursor: grabbing;
}

:where(.${BUILDER_ROOT_CLASS}, .${BUILDER_FLOATING_CLASS}) .mantine-Input-input {
  color: var(--hw-text, #f4f7fc);
  background: var(--hw-input, #121d30);
  border-color: var(--hw-border, #304563);
  box-shadow: inset 0 1px 0 rgba(255, 255, 255, 0.025);
  transition: border-color 120ms ease, background-color 120ms ease, box-shadow 120ms ease;
}

:where(.${BUILDER_ROOT_CLASS}, .${BUILDER_FLOATING_CLASS}) .mantine-Input-input:hover {
  background: var(--hw-input-hover, #172640);
  border-color: var(--hw-border-strong, #466184);
}

:where(.${BUILDER_ROOT_CLASS}, .${BUILDER_FLOATING_CLASS}) .mantine-Input-input:focus,
:where(.${BUILDER_ROOT_CLASS}, .${BUILDER_FLOATING_CLASS}) .mantine-Input-input:focus-within {
  background: var(--hw-input, #121d30);
  border-color: ${DARK.accent};
  box-shadow: 0 0 0 2px rgba(54, 108, 237, 0.2);
}

:where(.${BUILDER_ROOT_CLASS}, .${BUILDER_FLOATING_CLASS}) .mantine-Input-description {
  color: #8296b7;
}

.${BUILDER_ROOT_CLASS} .mantine-Accordion-item {
  background: #142035;
  border-color: var(--hw-border) !important;
  overflow: hidden;
}

.${BUILDER_ROOT_CLASS} .mantine-Accordion-control {
  color: #dce5f3;
  transition: color 120ms ease, background-color 120ms ease;
}

.${BUILDER_ROOT_CLASS} .mantine-Accordion-control:hover {
  color: var(--hw-text);
  background: var(--hw-panel-raised);
}

.${BUILDER_ROOT_CLASS} .mantine-Accordion-panel {
  border-top: 1px solid rgba(48, 69, 99, 0.7);
}

.${BUILDER_ROOT_CLASS} .mantine-Badge-root {
  color: #c3d0e3;
  background: #263751;
  border: 1px solid #344a6a;
}

.${BUILDER_ROOT_CLASS} .mantine-SegmentedControl-root {
  padding: 3px;
  background: #0f1929;
  border: 1px solid var(--hw-border);
}

.${BUILDER_ROOT_CLASS} .mantine-SegmentedControl-indicator {
  background: #27416f;
  border: 1px solid #436bc0;
  box-shadow: 0 2px 8px rgba(3, 8, 16, 0.3);
}

.${BUILDER_ROOT_CLASS} .mantine-SegmentedControl-label {
  color: #aab9cf;
}

.${BUILDER_ROOT_CLASS} .mantine-SegmentedControl-label[data-active] {
  color: #ffffff;
}

.${BUILDER_ROOT_CLASS} .mantine-Fieldset-root {
  background: #101a2b;
  border-color: var(--hw-border);
}

.${BUILDER_ROOT_CLASS} .react-flow__controls {
  overflow: hidden;
  border: 1px solid var(--hw-border);
  border-radius: 9px;
  box-shadow: 0 10px 24px rgba(0, 0, 0, 0.3);
}

.${BUILDER_ROOT_CLASS} .react-flow__controls-button {
  color: #dce5f3;
  fill: currentColor;
  background: #17243a;
  border-color: var(--hw-border);
}

.${BUILDER_ROOT_CLASS} .react-flow__controls-button:hover {
  color: #ffffff;
  background: #243754;
}

.${BUILDER_ROOT_CLASS} .react-flow__controls-button:disabled {
  color: #637693;
  background: #111c2d;
}

.${BUILDER_ROOT_CLASS} .react-flow__attribution {
  color: #6f829f;
  background: rgba(11, 17, 27, 0.88);
}

.${BUILDER_ROOT_CLASS} .react-flow__attribution a {
  color: inherit;
}

.${BUILDER_ROOT_CLASS} ::-webkit-scrollbar,
.${BUILDER_FLOATING_CLASS} ::-webkit-scrollbar {
  width: 10px;
  height: 10px;
}

.${BUILDER_ROOT_CLASS} ::-webkit-scrollbar-track,
.${BUILDER_FLOATING_CLASS} ::-webkit-scrollbar-track {
  background: transparent;
}

.${BUILDER_ROOT_CLASS} ::-webkit-scrollbar-thumb,
.${BUILDER_FLOATING_CLASS} ::-webkit-scrollbar-thumb {
  background: #3a4e6d;
  border: 3px solid transparent;
  border-radius: 999px;
  background-clip: padding-box;
}

.${BUILDER_OVERLAY_CLASS} {
  background: rgba(3, 7, 13, 0.74) !important;
  backdrop-filter: blur(2px);
}

.${BUILDER_FLOATING_CLASS} {
  --hw-input: #101a2b;
  --hw-input-hover: #15243a;
  --hw-border: #304563;
  --hw-border-strong: #466184;
  --hw-text: #f4f7fc;
  color: var(--hw-text);
  background: #18253b;
  border: 1px solid var(--hw-border);
  box-shadow: 0 24px 80px rgba(0, 0, 0, 0.48);
}

.${BUILDER_FLOATING_HEADER_CLASS} {
  background: #18253b;
  border-bottom: 1px solid #304563;
}

.mantine-Popover-dropdown {
  color: #f4f7fc;
  background: #18253b;
  border-color: #3a506f;
  box-shadow: 0 16px 40px rgba(0, 0, 0, 0.42);
}

.mantine-Combobox-option:hover,
.mantine-Combobox-option[data-combobox-selected] {
  background: #263b5d;
}

@media (prefers-reduced-motion: reduce) {
  .${BUILDER_PALETTE_ITEM_CLASS},
  .${BUILDER_TOOLBAR_CLASS} .mantine-Button-root,
  .${BUILDER_ROOT_CLASS} .mantine-Accordion-control,
  :where(.${BUILDER_ROOT_CLASS}, .${BUILDER_FLOATING_CLASS}) .mantine-Input-input {
    transition: none;
  }
}
`;
