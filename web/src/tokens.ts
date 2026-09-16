// Shared surface tokens for the marketing overview and the builder.
// Institutional light: office daylight, paper and steel, no neon-on-navy.
//
// Every pair below was checked with contrastRatio (see tokens.test.ts) rather
// than by eye.
export const DARK = {
  /** Page canvas. Cool institutional gray, not cream and not navy. */
  bg: "#F4F5F7",
  /** Lifted surface for cards, panels, and node bodies. */
  surface: "#FFFFFF",
  /** Recessed band, for code blocks and alternating sections. */
  recessed: "#EBEEF2",
  /** Hairline border. */
  border: "#C5CBD4",
  text: "#14181F",
  muted: "#3E4A59",
  /** Steel blue. Fills, links, and large text. */
  accent: "#0C447C",
  /** Same steel, dark enough for small text on the canvas. */
  accentText: "#0C447C",
  /** No second accent. Highlight is the same steel, not cyan. */
  highlight: "#0C447C",
} as const;

// NODE is the shared identity palette for graph nodes, used by the builder
// canvas and by the marketing page's builder frame so the two match.
export const NODE = {
  border: {
    endpoint: "#0C447C",
    destination: "#4A5568",
    filter: "#1B5E3B",
  },
  label: {
    endpoint: "#0C447C",
    destination: "#14181F",
    filter: "#14181F",
  },
  /** Subtle tint behind the destination type badge. */
  badgeBg: "#D8DDE4",
  badgeText: "#14181F",
} as const;

// PORT colours handles by payload type so illegal wiring is visible, not just
// refused. Muted enough to sit on white cards without reading as decoration.
export const PORT: Record<string, string> = {
  None: "#5C6778",
  RequestHead: "#0C447C",
  RawRequest: "#7A4E0E",
  Verified: "#1B5E3B",
  Envelope: "#0C447C",
  Terminal: "#4A5568",
  on_error: "#8F2D2D",
};

export const EDGE = {
  /** Resting stroke and arrowhead. */
  line: "#5C6778",
  selected: "#0C447C",
  /** Border on the target-name pill that sits on the canvas. */
  labelBorder: "#C5CBD4",
} as const;

const linearize = (channel: number): number => {
  const c = channel / 255;
  return c <= 0.04045 ? c / 12.92 : Math.pow((c + 0.055) / 1.055, 2.4);
};

/** relativeLuminance implements the WCAG 2.1 definition. */
function relativeLuminance(hex: string): number {
  const h = hex.replace("#", "");
  const [r, g, b] = [0, 2, 4].map((i) => parseInt(h.slice(i, i + 2), 16));
  return 0.2126 * linearize(r) + 0.7152 * linearize(g) + 0.0722 * linearize(b);
}

/** contrastRatio returns the WCAG contrast ratio between two hex colours. */
export function contrastRatio(a: string, b: string): number {
  const [lighter, darker] = [relativeLuminance(a), relativeLuminance(b)].sort((x, y) => y - x);
  return (lighter + 0.05) / (darker + 0.05);
}
