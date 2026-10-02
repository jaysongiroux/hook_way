/** Parse the authoring syntax accepted by Go time.ParseDuration, in nanoseconds.
 * Go remains authoritative for node-specific limits and cross-field semantics.
 */
export function parseGoDuration(value: string): bigint | null {
  if (!value) return null;
  let remaining = value;
  let negative = false;
  if (remaining[0] === "+" || remaining[0] === "-") {
    negative = remaining[0] === "-";
    remaining = remaining.slice(1);
  }
  if (remaining === "0") return 0n;
  const limit = negative ? 1n << 63n : (1n << 63n) - 1n;
  const units: Record<string, bigint> = {
    ns: 1n,
    us: 1000n,
    µs: 1000n,
    μs: 1000n,
    ms: 1000000n,
    s: 1000000000n,
    m: 60000000000n,
    h: 3600000000000n,
  };
  let total = 0n;
  let count = 0;
  while (remaining) {
    const match = /^(\d+(?:\.\d*)?|\.\d+)(ns|us|µs|μs|ms|s|m|h)/.exec(
      remaining,
    );
    if (!match) return null;
    const [wholeRaw, fraction = ""] = match[1].split(".");
    const whole = wholeRaw.replace(/^0+/, "") || "0";
    if (whole.length > 19) return null;
    const unit = units[match[2]];
    let piece = BigInt(whole) * unit;
    // Match Go's bounded fractional accumulation and float64 conversion,
    // including nanosecond rounding for fractional hours.
    let fractionValue = 0n;
    let scale = 1;
    let overflow = false;
    for (const digit of fraction) {
      if (overflow) continue;
      if (fractionValue > ((1n << 63n) - 1n) / 10n) {
        overflow = true;
        continue;
      }
      const next = fractionValue * 10n + BigInt(digit);
      if (next > 1n << 63n) {
        overflow = true;
        continue;
      }
      fractionValue = next;
      scale *= 10;
    }
    if (fractionValue > 0n)
      piece += BigInt(
        Math.trunc(Number(fractionValue) * (Number(unit) / scale)),
      );
    total += piece;
    if (total > limit) return null;
    remaining = remaining.slice(match[0].length);
    count += 1;
  }
  return count ? (negative ? -total : total) : null;
}

export function goDurationIssue(
  value: unknown,
  {
    allowZero = false,
    required = false,
  }: { allowZero?: boolean; required?: boolean } = {},
): string | undefined {
  if (value === undefined || value === null || value === "")
    return required
      ? "Enter a Go duration, such as 500ms, 2s or 5m."
      : undefined;
  const duration = typeof value === "string" ? parseGoDuration(value) : null;
  if (duration === null)
    return "Use a Go duration, such as 500ms, 2s or 5m (units: ns, us, ms, s, m, h).";
  if (duration < 0n || (!allowZero && duration === 0n))
    return allowZero
      ? "Duration must be zero or positive."
      : "Duration must be positive.";
  return undefined;
}
