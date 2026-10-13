import { parse } from "yaml";

export function scanEnvVars(text: string): string[] {
  const found = new Set<string>();
  const envRef = /\$\{([A-Za-z_][A-Za-z0-9_]*)\}/g;
  let m: RegExpExecArray | null;
  while ((m = envRef.exec(text)) !== null) found.add(m[1]);
  // References also occur in auth.secret_ref, managed headers, OAuth clients
  // and exchange parameters. Parse YAML/JSON so nesting cannot hide a required
  // environment variable from the authoring panel.
  try {
    const seen = new WeakSet<object>();
    const visit = (value: unknown, depth = 0) => {
      if (!value || typeof value !== "object" || depth > 128 || seen.has(value))
        return;
      seen.add(value);
      const ref = value as Record<string, unknown>;
      if (
        ref.provider === "env" &&
        typeof ref.name === "string" &&
        /^[A-Za-z_][A-Za-z0-9_]*$/.test(ref.name)
      ) {
        found.add(ref.name);
      }
      for (const child of Object.values(value)) visit(child, depth + 1);
    };
    visit(parse(text));
  } catch {
    // An incomplete document still exposes its explicit ${VAR} references.
  }
  return [...found].sort();
}
