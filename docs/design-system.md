# Design System

The visual system for the `web/` bundle — the marketing overview and the config
builder, which share one palette and type scale. This is a UI style guide, not
system architecture; for the typed-port node graph see the README's
[Architecture](../README.md#architecture-the-typed-port-node-graph) section and
[Request-pipeline invariants](invariants.md).

Light institutional operations console. Marketing and the config builder share one system.

## Surface

Cool gray canvas `#F4F5F7`, white panels, hairline `#C5CBD4`. Steel `#0C447C` is the only accent — fills, links, selected graph chrome. No second highlight, no navy field, no cyan.

## Type

Public Sans for UI. Source Code Pro for YAML, node types, and destination names. Headings weight 650. No training-data fonts.

## Rules

No glow orbs, gradient text, glass, decorative card bars, travelling-edge pulses, section kickers, candy window dots, or neon-on-navy. Cards are 4px, not pills. Numbers on the site are counts a reader can verify in the repo.

## Chrome

The Hookway mark sits in both navbars and returns to `/`. Builder header: flow switcher, Nodes / Inspector, Config (YAML and workspace), Clear. Graph handles keep muted port colours; node labels stay ink on white.

## Contrast

WCAG AA pairs are asserted in `web/src/tokens.test.ts`.
