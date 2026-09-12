// Every outbound link on the marketing site resolves from here, so the repo
// path is stated once. The site deliberately links to the GitHub copies of the
// docs instead of restating them.
export const GITHUB_REPO = "https://github.com/StarwingAI/hook_way";

/** repoBlob links a file at the repo's default branch. */
export function repoBlob(path: string): string {
  return `${GITHUB_REPO}/blob/main/${path}`;
}

/** repoTree links a directory at the repo's default branch. */
export function repoTree(path: string): string {
  return `${GITHUB_REPO}/tree/main/${path}`;
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
  doc("README.md", "Overview and quick start", "What Hookway is and how to get it running locally."),
  doc("docs/configuration.md", "Configuration reference", "Every block, field, and default, with worked samples."),
  doc("docs/cli.md", "CLI reference", "Serve, validate, and edit configs from the command line."),
  doc("docs/security.md", "Security model", "Signature verification, auth, and the threat model."),
  doc("docs/production-readiness.md", "Production readiness", "Operating it for real: limits, metrics, and failure modes."),
  doc("docs/local-development.md", "Local development", "Run the stack and its dependencies on your machine."),
];

export const EXAMPLES = {
  path: "examples",
  title: "Example configs",
  description: "Ready-to-run YAML for each destination, filters, replay, and variables.",
  href: repoTree("examples"),
};
