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
