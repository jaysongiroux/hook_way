// Every outbound link on the marketing site resolves from here, so the repo
// path is stated once. The site deliberately links to the GitHub copies of the
// docs instead of restating them.
export const GITHUB_REPO = "https://github.com/jaysongiroux/hook_way";

/**
 * The owner/repo slug, derived so that nowhere else in the site has to spell
 * the owner a second time. Before this existed the footer and the comparison
 * page each hardcoded their own copy, which is exactly how a rename rots half
 * the links and leaves the other half working.
 */
export const GITHUB_REPO_SLUG = GITHUB_REPO.replace(/^https?:\/\/github\.com\//, "");

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
  doc("README.md", "Overview and quick start", "What Hookway is and how to run it locally."),
  doc("docs/configuration.md", "Configuration reference", "Every block, field, and default, with worked samples."),
  doc("docs/cli.md", "CLI reference", "Every command and flag of hookway and hookway-replay."),
  doc("docs/security.md", "Security model", "Signature verification, auth, and the threat model."),
  doc("docs/replay-service.md", "Replay operations", "Capture, inspect, and retry failed deliveries with the optional replay service."),
  doc("docs/local-development.md", "Local development", "Run the stack, stop a destination, and replay what it missed."),
];

export const EXAMPLES = {
  path: "examples",
  title: "Example configs",
  description: "Ready-to-run YAML for XML, multipart, conditional routing, and every destination.",
  href: repoTree("examples"),
};
