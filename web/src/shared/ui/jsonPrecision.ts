/** JSON Schema is stored as a JavaScript object in the graph. Check every
 * numeric token before JSON.parse, which can silently round decimal text. */
export function precisionRisk(raw: string): string | null {
  let quoted = false;
  let escaped = false;
  for (let i = 0; i < raw.length; i++) {
    const ch = raw[i];
    if (quoted) {
      if (escaped) escaped = false;
      else if (ch === "\\") escaped = true;
      else if (ch === '"') quoted = false;
      continue;
    }
    if (ch === '"') { quoted = true; continue; }
    if (ch !== "-" && (ch < "0" || ch > "9")) continue;
    const match = /^-?(?:0|[1-9][0-9]*)(?:\.[0-9]+)?(?:[eE][+-]?[0-9]+)?/.exec(raw.slice(i));
    if (!match) continue;
    const literal = match[0];
    const value = Number(literal);
    if (!Number.isFinite(value) || Object.is(value, -0) ||
      decimalIdentity(literal) !== decimalIdentity(String(value))) {
      return `Numeric literal ${literal} would lose precision in the builder. Edit the source YAML manually for this constraint.`;
    }
    i += literal.length - 1;
  }
  return null;
}

/** Normalize decimal notation without converting through a JS number. */
function decimalIdentity(literal: string): string {
  const [mantissa, exponentText] = literal.toLowerCase().split("e");
  const negative = mantissa.startsWith("-");
  const unsigned = negative ? mantissa.slice(1) : mantissa;
  const [whole, fraction = ""] = unsigned.split(".");
  let digits = (whole + fraction).replace(/^0+/, "");
  if (!digits) return negative ? "-0" : "0";
  let exponent = Number(exponentText ?? 0) - fraction.length;
  while (digits.endsWith("0")) { digits = digits.slice(0, -1); exponent++; }
  return `${negative ? "-" : ""}${digits}e${exponent}`;
}

export function parseSchemaObject(raw: string): Record<string, unknown> {
  const risk = precisionRisk(raw);
  if (risk) throw new Error(risk);
  const parsed: unknown = JSON.parse(raw);
  if (!parsed || typeof parsed !== "object" || Array.isArray(parsed)) {
    throw new Error("Enter a JSON Schema object.");
  }
  if (Object.keys(parsed).length === 0) {
    throw new Error("Enter a nonempty JSON Schema object.");
  }
  return parsed as Record<string, unknown>;
}
