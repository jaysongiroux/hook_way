import { unified } from "unified";
import remarkRehype from "remark-rehype";
import rehypeRaw from "rehype-raw";
import rehypeSanitize, { defaultSchema } from "rehype-sanitize";
import rehypeStringify from "rehype-stringify";
import { visit } from "unist-util-visit";
import { toString } from "mdast-util-to-string";
import GithubSlugger from "github-slugger";
import { createHighlighter } from "shiki";
import { fromHtml } from "hast-util-from-html";
import { parser } from "./catalog.mjs";

// Markdown HTML is sanitized before our trusted highlighting/diagram transforms.
const schema = {
  ...defaultSchema,
  clobberPrefix: "",
  attributes: {
    ...defaultSchema.attributes,
    "*": [...defaultSchema.attributes["*"], "id"],
    span: [...(defaultSchema.attributes.span || []), "dataSourceTitle"],
    code: [
      ...(defaultSchema.attributes.code || []),
      ["className", /^language-/],
    ],
  },
};
let highlighter;
export const escapeHtml = (value) =>
  String(value).replace(
    /[&<>"']/g,
    (c) =>
      ({ "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;", "'": "&#39;" })[
        c
      ],
  );
export function siteOrigin(
  value = process.env.SITE_URL || "https://gethookway.com",
) {
  const url = new URL(value);
  if (
    !["http:", "https:"].includes(url.protocol) ||
    url.username ||
    url.password ||
    url.pathname !== "/" ||
    url.search ||
    url.hash
  ) {
    throw new Error(
      "SITE_URL must be an HTTP(S) origin, for example https://gethookway.com",
    );
  }
  return url.origin;
}
export async function compilePage(entry, body, config) {
  const tree = parser.parse(body);
  const headings = [];
  const slugger = new GithubSlugger();
  visit(tree, (node) => {
    if (node.type === "html") {
      const original = node.value.match(/data-source-title="([^"]*)"/);
      if (original) slugger.slug(decodeURIComponent(original[1]));
    }
    if (node.type === "heading") {
      const title = toString(node);
      const id = slugger.slug(title);
      node.data = { ...node.data, hProperties: { id } };
      headings.push({ id, title, depth: node.depth });
    }
  });
  const yaml = entry.kind === "yaml";
  // The YAML actions are rendered as React components, not source HTML controls.
  if (yaml)
    tree.children = [
      { type: "code", lang: "yaml", value: entry.text.replace(/\n$/, "") },
    ];
  const processor = unified()
    .use(remarkRehype, { allowDangerousHtml: true })
    .use(rehypeRaw)
    .use(rehypeSanitize, schema);
  const hast = await processor.run(tree);
  const textParts = [];
  visit(hast, "text", (node) => textParts.push(node.value));
  const plainText = textParts.join(" ").replace(/\s+/g, " ").trim();
  highlighter ??= createHighlighter({
    themes: ["github-light"],
    langs: [
      "yaml",
      "json",
      "bash",
      "go",
      "javascript",
      "typescript",
      "sql",
      "dockerfile",
      "text",
    ],
  });
  const highlight = await highlighter;
  visit(hast, "element", (node, index, parent) => {
    if (node.tagName !== "pre" || !parent) return;
    const code = node.children[0];
    if (code?.tagName !== "code") return;
    const text = code.children.map((child) => child.value || "").join("");
    const language =
      code.properties.className
        ?.find((name) => name.startsWith("language-"))
        ?.slice(9) || "text";
    if (language === "mermaid") {
      node.properties = {
        className: ["mermaid"],
        ariaLabel: "Hookway flow diagram",
      };
      node.children = [{ type: "text", value: text }];
    } else {
      const lang = highlight.getLoadedLanguages().includes(language)
        ? language
        : "text";
      parent.children[index] = fromHtml(
        highlight.codeToHtml(text, { lang, theme: "github-light" }),
        { fragment: true },
      ).children[0];
    }
  });
  const html = unified().use(rehypeStringify).stringify(hast);
  const slug = entry.route.slice("/docs/".length).replace(/\/$/, "") || "index";
  return {
    source: entry.source,
    route: entry.route,
    title: entry.title,
    kind: entry.kind,
    description:
      entry.description ||
      (yaml ? `YAML example: ${entry.source}` : plainText.slice(0, 170)),
    html,
    headings,
    dataUrl: `/docs/data/${slug}.json`,
    rawUrl: yaml ? `/docs/raw/${entry.source}` : null,
    editUrl: entry.source
      ? `${config.repository}/edit/${config.ref}/${entry.source}`
      : null,
    searchText:
      `${entry.source || ""} ${yaml ? entry.text : plainText}`.replace(
        /\s+/g,
        " ",
      ),
  };
}
export function pageHtml(template, page, origin) {
  const metadata = `<title>${escapeHtml(page.title)} | Hookway</title><meta name="description" content="${escapeHtml(page.description)}"><link rel="canonical" href="${escapeHtml(origin + page.route)}">`;
  return template
    .replace(/<title>[\s\S]*?<\/title>/, "")
    .replace(/<meta\s+name="description"[^>]*>/, "")
    .replace("</head>", `${metadata}</head>`)
    .replace(
      '<div id="root"></div>',
      `<div id="root"><article><h1>${escapeHtml(page.title)}</h1>${page.html}</article></div><script id="hookway-doc-page" type="application/json">${JSON.stringify(page).replace(/</g, "\\u003c")}</script>`,
    );
}
