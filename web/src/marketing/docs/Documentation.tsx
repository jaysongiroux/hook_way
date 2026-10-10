import {
  Anchor,
  Button,
  Container,
  Group,
  Loader,
  Modal,
  TextInput,
  Text,
} from "@mantine/core";
import { useEffect, useRef, useState, type MouseEvent } from "react";
import { useDocumentTitle } from "@mantine/hooks";
import { Link, useLocation, useNavigate } from "react-router-dom";
import publication from "virtual:hookway-docs-data";
import { MarketingShell } from "../MarketingShell";
import { DARK } from "../../tokens";
import "./documentation.css";

interface Heading {
  id: string;
  title: string;
  depth: number;
}
interface DocPage {
  route: string;
  title: string;
  description: string;
  source: string | null;
  kind: string;
  html: string;
  headings: Heading[];
  rawUrl: string | null;
  editUrl: string | null;
}
interface NavItem {
  label: string;
  link?: string;
  items?: NavItem[];
}
interface SearchEntry {
  route: string;
  title: string;
  text: string;
}
const initialData = document.getElementById("hookway-doc-page")?.textContent;
const initialPage: DocPage | undefined = initialData
  ? JSON.parse(initialData)
  : undefined;

function Navigation({
  items,
  route,
  onSelect,
}: {
  items: NavItem[];
  route: string;
  onSelect: () => void;
}) {
  return (
    <>
      {items.map((item) =>
        item.items ? (
          <details
            key={item.label}
            open={
              item.items.some((child) =>
                route.startsWith(child.link || "/unpublished/"),
              ) || undefined
            }
          >
            <summary>{item.label}</summary>
            <Navigation items={item.items} route={route} onSelect={onSelect} />
          </details>
        ) : (
          <Link
            key={item.link}
            to={item.link!}
            aria-current={route === item.link ? "page" : undefined}
            onClick={onSelect}
          >
            {item.label}
          </Link>
        ),
      )}
    </>
  );
}
function Search({ opened, close }: { opened: boolean; close: () => void }) {
  const [query, setQuery] = useState("");
  const [index, setIndex] = useState<SearchEntry[]>([]);
  const [error, setError] = useState(false);
  useEffect(() => {
    if (!opened || index.length) return;
    setError(false);
    const controller = new AbortController();
    fetch("/docs/search.json", { signal: controller.signal })
      .then((response) => {
        if (!response.ok) throw new Error("Search unavailable");
        return response.json();
      })
      .then(setIndex)
      .catch((error) => {
        if (error.name !== "AbortError") setError(true);
      });
    return () => controller.abort();
  }, [opened, index.length]);
  const terms = query.toLowerCase().trim().split(/\s+/).filter(Boolean);
  const matches = terms.length
    ? index
      .filter((page) =>
        terms.every((term) =>
          `${page.title} ${page.text}`.toLowerCase().includes(term),
        ),
      )
      .sort(
        (a, b) =>
          Number(b.title.toLowerCase().includes(query.toLowerCase())) -
          Number(a.title.toLowerCase().includes(query.toLowerCase())),
      )
      .slice(0, 15)
    : [];
  return (
    <Modal
      opened={opened}
      onClose={close}
      title="Search documentation"
      size="lg"
    >
      <TextInput
        aria-label="Search documentation"
        placeholder="Search guides, configuration, and YAML examples"
        value={query}
        onChange={(event) => setQuery(event.currentTarget.value)}
        data-autofocus
      />
      <div className="docs-search-results" aria-live="polite">
        {error ? (
          <Text c="red">Search could not load. Close and reopen to retry.</Text>
        ) : query.trim() && !index.length ? (
          <Loader size="sm" />
        ) : terms.length && !matches.length ? (
          <Text c="dimmed">No matching pages. Try a different term.</Text>
        ) : !terms.length ? (
          <Text c="dimmed">Search the documentation and examples.</Text>
        ) : (
          matches.map((page) => (
            <Link key={page.route} to={page.route} onClick={close}>
              <strong>{page.title}</strong>
              <small>
                {page.route.replace("/docs/", "") || "Documentation"}
              </small>
            </Link>
          ))
        )}
      </div>
    </Modal>
  );
}
export default function Documentation() {
  const location = useLocation();
  const navigate = useNavigate();
  const route = `${location.pathname.replace(/\/$/, "")}/`;
  const metadata = publication.pages.find((page) => page.route === route);
  const [loaded, setLoaded] = useState<DocPage | undefined>(initialPage);
  const [failed, setFailed] = useState(false);
  const [menu, setMenu] = useState(false);
  const [search, setSearch] = useState(false);
  const [copyStatus, setCopyStatus] = useState("Copy YAML");
  const article = useRef<HTMLDivElement>(null);
  const page = loaded?.route === route ? loaded : undefined;
  useDocumentTitle(
    `${page?.title || metadata?.title || "404 — Page not found"} | Hookway`,
  );
  useEffect(() => {
    setFailed(false);
    setCopyStatus("Copy YAML");
    if (!metadata || page) return;
    const controller = new AbortController();
    fetch(metadata.dataUrl, { signal: controller.signal })
      .then((response) => {
        if (!response.ok) throw new Error("Page unavailable");
        return response.json();
      })
      .then(setLoaded)
      .catch((error) => {
        if (error.name !== "AbortError") setFailed(true);
      });
    return () => controller.abort();
  }, [route, metadata, page]);
  useEffect(() => {
    const description = document.querySelector('meta[name="description"]');
    description?.setAttribute(
      "content",
      page?.description ||
      (metadata
        ? "Documentation for Hookway."
        : "Documentation page not found."),
    );
    let canonical = document.querySelector<HTMLLinkElement>(
      'link[rel="canonical"]',
    );
    if (!canonical) {
      canonical = document.createElement("link");
      canonical.rel = "canonical";
      document.head.append(canonical);
    }
    canonical.href = publication.origin + route;
  }, [page, metadata, route]);
  useEffect(() => {
    if (!page) return;
    window.scrollTo(0, 0);
    const diagrams = Array.from(
      article.current?.querySelectorAll<HTMLElement>(".mermaid") || [],
    );
    let cancelled = false;
    if (diagrams.length)
      void import("mermaid")
        .then(async ({ default: mermaid }) => {
          if (cancelled) return;
          mermaid.initialize({
            startOnLoad: false,
            securityLevel: "strict",
            theme: "neutral",
            fontFamily: "Public Sans",
          });
          // Detached nodes avoid stale rendering when navigating away mid-import.
          for (const [index, node] of diagrams.entries()) {
            const { svg } = await mermaid.render(
              `hookway-diagram-${Date.now()}-${index}`,
              node.textContent || "",
            );
            if (!cancelled) node.innerHTML = svg;
          }
        })
        .catch(() => {
          /* Keep the readable diagram source if rendering fails. */
        });
    return () => {
      cancelled = true;
    };
  }, [page, route]);
  useEffect(() => {
    if (!page || !location.hash) return;
    let id = location.hash.slice(1);
    try {
      id = decodeURIComponent(id);
    } catch {
      /* Malformed URL fragments have no matching heading. */
    }
    document.getElementById(id)?.scrollIntoView();
  }, [page, location.hash]);
  useEffect(() => {
    const shortcut = (event: KeyboardEvent) => {
      if ((event.metaKey || event.ctrlKey) && event.key.toLowerCase() === "k") {
        event.preventDefault();
        setSearch(true);
      }
    };
    window.addEventListener("keydown", shortcut);
    return () => window.removeEventListener("keydown", shortcut);
  }, []);
  function followLink(event: MouseEvent<HTMLDivElement>) {
    const anchor = (event.target as Element).closest<HTMLAnchorElement>(
      "a[href]",
    );
    if (
      !anchor ||
      event.button !== 0 ||
      event.metaKey ||
      event.ctrlKey ||
      event.shiftKey ||
      event.altKey ||
      anchor.target ||
      anchor.hasAttribute("download")
    )
      return;
    const url = new URL(anchor.href);
    if (
      url.origin !== window.location.origin ||
      !publication.pages.some((page) => page.route === url.pathname)
    )
      return;
    event.preventDefault();
    navigate(url.pathname + url.search + url.hash);
  }
  async function copyYaml() {
    if (!page?.rawUrl) return;
    try {
      const response = await fetch(page.rawUrl);
      if (!response.ok) throw new Error("Download failed");
      await navigator.clipboard.writeText(await response.text());
      setCopyStatus("Copied");
    } catch {
      setCopyStatus("Copy failed — try again");
    }
  }
  return (
    <MarketingShell>
      <Container
        size="lg"
        className="docs-container"
        style={
          {
            "--docs-accent": DARK.accent,
            "--docs-muted": DARK.muted,
            "--docs-border": DARK.border,
            "--docs-recessed": DARK.recessed,
          } as React.CSSProperties
        }
      >
        <Button
          className="docs-menu-toggle"
          size="xs"
          aria-label="Documentation menu"
          variant="default"
          onClick={() => setMenu(!menu)}
          aria-expanded={menu}
          aria-controls="docs-navigation"
        >
          Browse
        </Button>
        <div className="docs-layout">
          <nav
            id="docs-navigation"
            aria-label="Documentation"
            className={`docs-navigation ${menu ? "is-open" : ""}`}
          >
            <Button
              className="docs-sidebar-search"
              size="xs"
              variant="default"
              fullWidth
              onClick={() => setSearch(true)}
            >
              Search
            </Button>
            <Navigation
              items={publication.sidebar}
              route={route}
              onSelect={() => setMenu(false)}
            />
          </nav>
          <article
            className="docs-article"
            aria-busy={!!metadata && !page && !failed}
          >
            {!metadata ? (
              <>
                <h1>404 — Page not found</h1>
                <p>This documentation page is unavailable.</p>
                <Link to="/docs/">Browse documentation</Link>
              </>
            ) : failed ? (
              <>
                <h1>{metadata.title}</h1>
                <p role="alert">
                  This page could not load. Refresh to try again.
                </p>
              </>
            ) : !page ? (
              <Loader aria-label="Loading documentation" />
            ) : (
              <>
                <div className="docs-breadcrumb">
                  <Link to="/docs/">Docs</Link>
                  {route !== "/docs/" && <> / {page.title}</>}
                </div>
                <h1>{page.title}</h1>
                {page.source && (
                  <div className="docs-source">
                    <code>{page.source}</code>
                    {page.editUrl && (
                      <Anchor
                        href={page.editUrl}
                        target="_blank"
                        rel="noopener noreferrer"
                        size="xs"
                      >
                        Edit on GitHub ↗
                      </Anchor>
                    )}
                  </div>
                )}
                {page.rawUrl && (
                  <Group className="docs-yaml-actions" gap="sm">
                    <Button
                      component="a"
                      href={page.rawUrl}
                      download
                      variant="default"
                    >
                      Download YAML
                    </Button>
                    <Button
                      variant="filled"
                      color={DARK.accent}
                      onClick={copyYaml}
                      aria-live="polite"
                    >
                      {copyStatus}
                    </Button>
                  </Group>
                )}
                {page.headings.length > 3 && (
                  <details className="docs-outline">
                    <summary>On this page</summary>
                    <ul>
                      {page.headings
                        .filter((heading) => heading.depth <= 3)
                        .map((heading) => (
                          <li key={heading.id}>
                            <a href={`#${heading.id}`}>{heading.title}</a>
                          </li>
                        ))}
                    </ul>
                  </details>
                )}
                <div
                  className="docs-content"
                  ref={article}
                  onClick={followLink}
                  dangerouslySetInnerHTML={{ __html: page.html }}
                />
              </>
            )}
          </article>
        </div>
      </Container>
      <Search opened={search} close={() => setSearch(false)} />
    </MarketingShell>
  );
}
