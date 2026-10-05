# Design system

Marketing, the config builder, and the replay console share the theme and
components in `web/src/shared/ui`. This page covers their appearance and
interaction rules. The [README diagram](../../README.md#how-it-fits-together)
shows the services; [pipeline invariants](invariants.md) explain the request graph.

## Color and layout

Use the shared tokens: cool gray `#F4F5F7` for the canvas, white `#FFFFFF` for
panels, `#C5CBD4` for borders, and steel blue `#0C447C` for links, primary actions,
and selection. Keep panels simple, with 4px corners and restrained borders.
Error and success states use semantic colors from the theme; graph ports use
their existing phase colors.

Avoid decorative gradients, glow, glass effects, and unnecessary animation.
Make the important action and current state easy to find.

## Typography and copy

Use Public Sans for interface text and Source Code Pro for code. Shared headings
use weight 650. In the standalone console, system fallbacks apply when those
fonts are unavailable; it does not load external font services.

Use readable labels and explain consequences before recovery actions. Marketing
claims must match implemented behavior. Feature counts come from catalogs;
performance claims include their measured workload and date.

## Navigation and accessibility

The Hookway mark links to `/`. The builder keeps flow selection, Nodes,
Inspector, and configuration actions together. Feature-specific layouts stay
in their own modules; reuse shared controls rather than restyling them locally.

Support keyboard navigation, visible focus, labeled fields, and readable errors.
Keep entered values after validation fails. Check narrow layouts as well as
wide ones. Contrast pairs are tested in `web/src/tokens.test.ts`; new colors
need the same checks.
