// Every outbound link on the marketing site resolves from here, so the repo
// path is stated once. The site deliberately links to the GitHub copies of the
// docs instead of restating them.
export const GITHUB_REPO = "https://github.com/jaysongiroux/hook_way";
// The current feature set and documentation live on v2. Update when it merges.
export const GITHUB_REF = "v2";

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

export interface DocLink {
  /** Repo-relative path, checked by the tests so a rename cannot rot the link. */
  path: string;
  title: string;
  description: string;
  href: string;
}

function doc(path: string, title: string, description: string): DocLink {
  return { path, title, description, href: repoBlob(path) };
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
    "Ready-to-run YAML for XML, multipart, conditional routing, and every destination.",
  href: repoTree("examples"),
};
