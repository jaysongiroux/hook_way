import registry from "../../site.json";
import { site } from "virtual:hookway-docs";

export interface PageMetadata {
  path: string;
  title: string;
  description: string;
  indexable?: boolean;
}
export const canonicalPath = (path: string) =>
  path === "/builder" || path === "/builder/"
    ? "/builder"
    : path === "/"
      ? "/"
      : path.replace(/\/+$/, "") + "/";
export const pageMetadata = (pathname: string): PageMetadata => {
  const path = canonicalPath(pathname);
  return (
    registry.pages.find((page) => page.path === path) || {
      path,
      title:
        path === "/builder" ? "Hookway Config" : "Page not found | Hookway",
      description:
        path === "/builder"
          ? "Build, validate and export a Hookway configuration in your browser."
          : "This Hookway page could not be found. Browse the documentation or return to the homepage.",
      indexable: false,
    }
  );
};
export function applyMetadata(page: PageMetadata) {
  document.title = page.title;
  const meta = (attribute: "name" | "property", key: string, value: string) => {
    let tag = document.head.querySelector<HTMLMetaElement>(
      "meta[" + attribute + '="' + key + '"]',
    );
    if (!tag) {
      tag = document.createElement("meta");
      tag.setAttribute(attribute, key);
      document.head.append(tag);
    }
    tag.content = value;
  };
  meta("name", "description", page.description);
  meta(
    "name",
    "robots",
    page.indexable === false || !site.indexable
      ? "noindex, follow"
      : "index, follow",
  );
  meta("property", "og:title", page.title);
  meta("property", "og:description", page.description);
  meta("property", "og:url", site.origin + page.path);
  meta("property", "og:type", "website");
  meta("property", "og:image", site.origin + registry.image);
  meta("name", "twitter:card", "summary_large_image");
  meta("name", "twitter:title", page.title);
  meta("name", "twitter:description", page.description);
  meta("name", "twitter:image", site.origin + registry.image);
  let canonical = document.head.querySelector<HTMLLinkElement>(
    'link[rel="canonical"]',
  );
  if (!canonical) {
    canonical = document.createElement("link");
    canonical.rel = "canonical";
    document.head.append(canonical);
  }
  canonical.href = site.origin + page.path;
  // Static structured data belongs to the directly loaded page, not a later route.
  document.getElementById("hookway-schema")?.remove();
  const items: Record<string, unknown>[] =
    page.path === "/"
      ? [{ "@type": "WebSite", name: registry.name, url: site.origin + "/" }]
      : [];
  if (page.path !== "/" && page.indexable !== false)
    items.push({
      "@type": "BreadcrumbList",
      itemListElement: [
        {
          "@type": "ListItem",
          position: 1,
          name: "Home",
          item: site.origin + "/",
        },
        {
          "@type": "ListItem",
          position: 2,
          name: page.title.split(" | ")[0],
          item: site.origin + page.path,
        },
      ],
    });
  if (items.length) {
    const script = document.createElement("script");
    script.id = "hookway-schema";
    script.type = "application/ld+json";
    script.textContent = JSON.stringify({
      "@context": "https://schema.org",
      "@graph": items,
    });
    document.head.append(script);
  }
}
