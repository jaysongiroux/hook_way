import publishedLinks from "virtual:hookway-docs";

// Source links stay on GitHub; published docs use the build's filtered inventory.
export const GITHUB_REPO = "https://github.com/jaysongiroux/hook_way";
// Public source links follow the repository's published branch.
export const GITHUB_REF = "main";

/** Repository slug for visible attribution. */
export const GITHUB_REPO_SLUG = GITHUB_REPO.replace(
  /^https?:\/\/github\.com\//,
  "",
);

/** repoBlob links a file for the documented product version. */
export function repoBlob(path: string): string {
  return `${GITHUB_REPO}/blob/${GITHUB_REF}/${path}`;
}

/** repoTree links a directory for the documented product version. */
export function repoTree(path: string): string {
  return `${GITHUB_REPO}/tree/${GITHUB_REF}/${path}`;
}

/** Use the discovered public page, retaining GitHub for excluded source files. */
export function docsHref(path: string): string {
  const split = path.search(/[?#]/);
  const source = split < 0 ? path : path.slice(0, split);
  const suffix = split < 0 ? "" : path.slice(split);
  return (publishedLinks[source] ?? repoBlob(source)) + suffix;
}

export interface DocLink {
  /** Repo-relative path, checked by the tests so a rename cannot rot the link. */
  path: string;
  title: string;
  description: string;
  href: string;
}

function doc(path: string, title: string, description: string): DocLink {
  return { path, title, description, href: docsHref(path) };
}

export const DOC_LINKS: DocLink[] = [
  doc(
    "README.md",
    "Overview and quick start",
    "What Hookway is and how to run it locally.",
  ),
  doc(
    "docs/README.md",
    "Documentation index",
    "Find references, feature guides, operations and developer guidance.",
  ),
  doc(
    "docs/reference/configuration.md",
    "Configuration reference",
    "YAML fields, defaults, and examples.",
  ),
  doc(
    "docs/guides/ingress-adapters.md",
    "Ingress adapters",
    "SQS/SNS, routing, credential profiles, batches and document relays.",
  ),
  doc(
    "docs/reference/cli.md",
    "CLI reference",
    "Core and replay commands, configuration checks and operations.",
  ),
  doc(
    "docs/evidence/performance.md",
    "Performance measurements",
    "Burst and sustained results, hardware, methodology and limits.",
  ),
  doc(
    "docs/reference/security.md",
    "Security model",
    "Signature verification, auth, and the threat model.",
  ),
  doc(
    "docs/operations/replay-service.md",
    "Replay operations",
    "Recover failed deliveries and view captured webhooks with the optional replay service.",
  ),
  doc(
    "docs/guides/protection-stores.md",
    "Rate limits and duplicate protection",
    "Use memory, Redis, or PostgreSQL to control requests and duplicates.",
  ),
  doc(
    "docs/guides/json-validation.md",
    "Validate JSON",
    "Check schemas and samples before sending data downstream.",
  ),
  doc(
    "docs/development/local-development.md",
    "Local development",
    "Run the stack, stop a destination, and replay what it missed.",
  ),
];

export const EXAMPLES = {
  path: "examples",
  title: "Example configs",
  description:
    "YAML templates for authenticated MCP tools, XML, multipart, routing, and every destination.",
  href: docsHref("examples"),
};
