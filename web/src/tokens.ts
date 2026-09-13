// Shared dark surface tokens. The marketing page applies these explicitly;
// the builder layers its denser editor surfaces on top in builderStyles.ts.
//
// Every pair below was checked with contrastRatio (see tokens.test.ts) rather
// than by eye. Measured against the canvas: white 16.3:1, muted 6.4:1,
// accent 3.5:1 (large text and UI only), accentText 6.9:1.
export const DARK = {
  /** Page canvas. Hometap's text navy, used here as the base. */
  bg: "#152033",
  /** Lifted surface for cards and tiles. */
  surface: "#1B2840",
  /** Recessed band, for code blocks and the closing call to action. */
  recessed: "#0F1826",
  /** Hairline border, visible without reading as a hard rule. */
  border: "#2A3A57",
  text: "#FFFFFF",
  muted: "#8FA3C7",
  /** Hometap Accent 1. Fills and large text only; 3.5:1 on the canvas. */
  accent: "#366CED",
  /** A lighter step of the accent, for small text that must be coloured. */
  accentText: "#7FA9F5",
  /** Hometap Accent 4, used sparingly for the event pulse. */
  highlight: "#70DAFF",
} as const;

// NODE is the shared identity palette for graph nodes, used by the builder
// canvas and by the marketing page's builder frame so the two match.
//
// Measured on DARK.surface: endpoint border 3.2:1, destination 3.8:1, filter
// 6.2:1, labels above 6:1, badge text 7.7:1 on its tint. The previous
// destination border (#434C5E) measured 1.7:1 and was effectively invisible.
export const NODE = {
  border: {
    endpoint: "#366CED",
    destination: "#6D82AB",
    filter: "#25BE8A",
  },
  label: {
    endpoint: "#7FA9F5",
    destination: "#FFFFFF",
    filter: "#FFFFFF",
  },
  /** Subtle tint behind the destination type badge. */
  badgeBg: "#24365C",
  badgeText: "#C6D0E3",
} as const;

// EDGE colours for the builder canvas. The defaults React Flow ships are
// tuned for a light background: a thin #b1b1b7 line reads as warm grey against
// navy, and the unselected stroke was left to React Flow entirely.
export const EDGE = {
  /** Resting stroke and arrowhead; ~4:1 on the canvas. */
  line: "#6D82AB",
  selected: "#366CED",
  /** Border on the target-name pill that sits on the canvas. */
  labelBorder: "#4A5C80",
} as const;

const linearize = (channel: number): number => {
  const c = channel / 255;
  return c <= 0.04045 ? c / 12.92 : Math.pow((c + 0.055) / 1.055, 2.4);
};

/** relativeLuminance implements the WCAG 2.1 definition. */
export function relativeLuminance(hex: string): number {
  const h = hex.replace("#", "");
  const [r, g, b] = [0, 2, 4].map((i) => parseInt(h.slice(i, i + 2), 16));
  return 0.2126 * linearize(r) + 0.7152 * linearize(g) + 0.0722 * linearize(b);
}

/** contrastRatio returns the WCAG contrast ratio between two hex colours. */
export function contrastRatio(a: string, b: string): number {
  const [lighter, darker] = [relativeLuminance(a), relativeLuminance(b)].sort((x, y) => y - x);
  return (lighter + 0.05) / (darker + 0.05);
}
