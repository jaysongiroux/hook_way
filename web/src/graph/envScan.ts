export function scanEnvVars(text: string): string[] {
  const found = new Set<string>();
  const envRef = /\$\{([A-Za-z_][A-Za-z0-9_]*)\}/g;
  let m: RegExpExecArray | null;
  while ((m = envRef.exec(text)) !== null) found.add(m[1]);
  collectSecretRefEnvNames(text, found);
  return [...found].sort();
}

// auth.secret_ref with provider env names an environment variable. Those
// names never appear as ${VAR}, so the environment panel would miss them
// unless we scan the mapping as well as interpolations.
function collectSecretRefEnvNames(text: string, found: Set<string>) {
  const re = /secret_ref:\s*(\{[^}]*\}|(?:\n[ \t]+[^\n]+)+)/g;
  let m: RegExpExecArray | null;
  while ((m = re.exec(text)) !== null) {
    const body = m[1];
    if (!/\bprovider:\s*env\b/.test(body)) continue;
    const name = /\bname:\s*["']?([A-Za-z_][A-Za-z0-9_]*)/.exec(body);
    if (name) found.add(name[1]);
  }
}
