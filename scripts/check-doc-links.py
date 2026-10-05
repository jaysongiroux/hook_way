#!/usr/bin/env python3
"""Check repository file and heading links in published docs and marketing copy."""
from pathlib import Path
import re
from urllib.parse import unquote, urlsplit


ROOT = Path(__file__).resolve().parents[1]
REPO_URL = "https://github.com/jaysongiroux/hook_way/"


def without_code(text):
    return re.sub(r"^```[^\n]*\n.*?^```\s*$", "", text, flags=re.M | re.S)


def headings(path):
    anchors = set()
    counts = {}
    for title in re.findall(r"^#{1,6}\s+(.+?)\s*#*$", without_code(path.read_text()), re.M):
        title = re.sub(r"\[([^]]+)\]\([^)]*\)", r"\1", title).lower()
        slug = "".join(c for c in title if c.isalnum() or c in "-_ ").replace(" ", "-")
        count = counts.get(slug, 0)
        counts[slug] = count + 1
        anchors.add(slug + (f"-{count}" if count else ""))
    return anchors


def problem(source, link, repo_relative=False):
    if link.startswith(REPO_URL):
        # Distribution docs use absolute GitHub links; check their local targets.
        match = re.match(r"(?:blob|tree)/[^/]+/(.*)", link[len(REPO_URL):])
        if not match:
            return None
        link, repo_relative = match[1], True
    parsed = urlsplit(link)
    if parsed.scheme or parsed.netloc:
        return None
    target = (((ROOT if repo_relative else source.parent) / unquote(parsed.path)) if parsed.path else source).resolve()
    if not target.is_relative_to(ROOT):
        return "target is outside the repository"
    if not target.exists():
        return "file or directory is missing"
    if parsed.fragment and target.suffix == ".md" and unquote(parsed.fragment) not in headings(target):
        return "heading is missing"
    return None


def main():
    errors = []
    checked = 0
    documents = sorted(ROOT.glob("*.md")) + sorted((ROOT / "docs").rglob("*.md"))
    documents.append(ROOT / "examples/README.md")
    for source in documents:
        if "local-plans" in source.parts:
            continue
        text = without_code(source.read_text())
        for link in re.findall(r"\]\(([^\s)]+)(?:\s+\"[^\"]*\")?\)", text):
            checked += 1
            error = problem(source, link.strip("<>"))
            if error:
                errors.append(f"{source.relative_to(ROOT)}: {link}: {error}")
    for source in sorted((ROOT / "web/src/marketing").glob("*.*")):
        if source.suffix not in (".ts", ".tsx") or ".test." in source.name:
            continue
        for link in re.findall(r"repo(?:Blob|Tree)\(\s*[\"']([^\"']+)[\"']\s*\)", source.read_text()):
            checked += 1
            error = problem(source, link, repo_relative=True)
            if error:
                errors.append(f"{source.relative_to(ROOT)}: {link}: {error}")
    if errors:
        print("\n".join(errors))
        return 1
    print(f"Checked {checked} documentation and marketing links.")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
