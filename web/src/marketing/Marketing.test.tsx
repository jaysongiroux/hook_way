import { expect, test } from "vitest";
import { render, screen } from "@testing-library/react";
import { MantineProvider } from "@mantine/core";
import { MemoryRouter } from "react-router-dom";
import { theme } from "../theme";
import { Marketing } from "./Marketing";
import { DOC_LINKS, GITHUB_REPO } from "./links";
import { destinationPresentation } from "./destinationIdentity";

function wrap() {
  return render(
    <MantineProvider theme={theme}>
      <MemoryRouter>
        <Marketing />
      </MemoryRouter>
    </MantineProvider>,
  );
}

test("the hero states what Hookway does and offers both calls to action", () => {
  wrap();
  const heading = screen.getByRole("heading", { level: 1 });
  expect(heading).toHaveTextContent(/receive, verify, and route webhooks/i);
  const main = screen.getByRole("main");
  expect(main).toHaveTextContent(/verify the request by wire format, then publish/i);
  expect(main).toHaveTextContent(/the builder exports that YAML; it does not talk to a running process/i);
  expect(screen.getByRole("link", { name: "Hookway home" })).toHaveAttribute("href", "/");
  expect(screen.getByRole("navigation", { name: "Primary" })).toBeInTheDocument();

  const builderCta = screen.getByRole("link", { name: /open the builder/i });
  expect(builderCta).toHaveAttribute("href", "/builder");

  const repoCta = screen.getByRole("link", { name: /github/i });
  expect(repoCta).toHaveAttribute("href", expect.stringContaining(GITHUB_REPO));
});

test("features lead with publish and verify, then bodies, branch, and transform", () => {
  wrap();
  const section = screen.getByRole("heading", { level: 2, name: "Configure the full request path" }).closest("section");
  expect(section).not.toBeNull();
  const text = section!.textContent ?? "";
  expect(text).toMatch(/verification fails closed on an unknown scheme/i);
  const marks = [
    "Publish to 14 destination types",
    "Verify 11 wire-format schemes",
    "JSON, XML, multipart, and raw bodies",
    "Branch on request data",
    "field_map",
  ];
  let at = -1;
  for (const mark of marks) {
    const next = text.indexOf(mark);
    expect(next, mark).toBeGreaterThan(at);
    at = next;
  }
  expect(text).toMatch(/HTTP Basic/);
  expect(text).toMatch(/exact header/);
  expect(text).toMatch(/publish\.payload: object/);
  expect(text).toMatch(/named connections/i);
  expect(text).toMatch(/if\/else for two outcomes/i);
  expect(text).toMatch(/switch for the first matching labelled route/i);
  expect(text).toMatch(/changed_fields/);
  expect(text).toMatch(/xml_prune/);
  expect(text).toMatch(/http_request/);
  expect(text).not.toMatch(/HMAC, static header, or JWT/);
});

test("the destination marquee states the Temporal YAML limitation", () => {
  wrap();
  expect(screen.getByRole("main")).toHaveTextContent(
    /Temporal workflow settings are not yet exposed in standalone YAML/,
  );
});

test("the overview points to dynamic-routing use cases and the sourced comparison", () => {
  wrap();
  const main = screen.getByRole("main");
  expect(main).toHaveTextContent(/variables can come from a path, JSON body, or filtered header/i);
  expect(main).toHaveTextContent(/\{envName\}-events/);
  expect(screen.getByRole("link", { name: "Explore use cases" })).toHaveAttribute("href", "/use-cases");
  expect(screen.getByRole("link", { name: "View the comparison" })).toHaveAttribute("href", "/compare");
});

test("the overview uses concrete product language instead of generic hype", () => {
  wrap();
  const copy = screen.getByRole("main").textContent!.toLowerCase();
  for (const phrase of [
    "seamless",
    "effortless",
    "game-changing",
    "cutting-edge",
    "next-level",
    "supercharge",
    "unlock the power",
    "future-proof",
    "all-in-one",
    "transform your",
  ]) {
    expect(copy, `marketing copy must not use generic hype ("${phrase}")`).not.toContain(phrase);
  }
});

test("a real config sample is shown rather than described", () => {
  wrap();
  // The sample must look like the YAML a user would actually write.
  const sample = screen.getByLabelText("Example Hookway configuration");
  expect(sample.textContent).toContain("flows:");
  expect(sample.textContent).toContain("hmac_sha256");
  expect(sample.textContent).toContain("destinations:");
});

test("every destination type the builder supports is listed", () => {
  wrap();
  for (const t of ["rabbitmq", "sqs", "kafka", "temporal", "redis_stream", "webhook"]) {
    expect(screen.getAllByText(destinationPresentation(t).label).length).toBeGreaterThanOrEqual(2);
  }
});

test("documentation links open the GitHub copies in a new tab safely", () => {
  wrap();
  for (const link of DOC_LINKS) {
    const el = screen.getByRole("link", { name: new RegExp(link.title, "i") });
    expect(el).toHaveAttribute("href", link.href);
    expect(el).toHaveAttribute("target", "_blank");
    expect(el).toHaveAttribute("rel", expect.stringContaining("noreferrer"));
  }
});

test("the page does not restate the documentation it links to", () => {
  wrap();
  // Guard against the site drifting into a docs rewrite: keep it a signpost.
  // Measured on the content region, since Mantine injects a stylesheet into
  // the render container.
  const copy = screen.getByRole("main").textContent!;
  // It exists to keep the page a signpost: docs/configuration.md alone is
  // ~95KB, and the site must not duplicate it.
  //
  // The "~4,300 after the bento rewrite" note that used to sit here went stale:
  // the marquee, the Explore band and the compare links landed afterwards and
  // took the page to ~5,450, i.e. 50 characters off the bound. Correcting the
  // retry/metrics/enablement claims made the honest copy longer still, so
  // redundant prose was trimmed to pay for it rather than raising the bound.
  // Leading with publish and verify, and stating the Temporal YAML limit on
  // the marquee, was paid for the same way (shorter bodies, code-band, and
  // production leads). It now sits at 5,459. If you need more room, cut something.
  expect(copy.length).toBeLessThan(5500);
});
