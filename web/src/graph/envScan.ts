export function scanEnvVars(text: string): string[] {
  const re = /\$\{([A-Za-z_][A-Za-z0-9_]*)\}/g;
  const found = new Set<string>();
  let m: RegExpExecArray | null;
  while ((m = re.exec(text)) !== null) found.add(m[1]);
  return [...found].sort();
}
