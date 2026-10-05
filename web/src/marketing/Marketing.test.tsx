import { expect, test } from "vitest";
import { render, screen } from "@testing-library/react";
import { MantineProvider } from "@mantine/core";
import { MemoryRouter } from "react-router-dom";
import { theme } from "../theme";
import { Marketing } from "./Marketing";
import { DOC_LINKS, GITHUB_REPO, repoBlob } from "./links";
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
  expect(main).toHaveTextContent(
    /check who sent each request, prepare its data/i,
  );
  expect(main).toHaveTextContent(
    /the builder does not connect to or manage your running server/i,
  );
  expect(screen.getByRole("link", { name: "Hookway home" })).toHaveAttribute(
    "href",
    "/",
  );
  expect(
    screen.getByRole("navigation", { name: "Primary" }),
  ).toBeInTheDocument();

  const builderCta = screen.getByRole("link", { name: /open the builder/i });
  expect(builderCta).toHaveAttribute("href", "/builder");

  const repoCta = screen.getByRole("link", { name: /github/i });
  expect(repoCta).toHaveAttribute("href", expect.stringContaining(GITHUB_REPO));
});

test("features lead with publish and verify, then bodies, branch, and transform", () => {
  wrap();
  const section = screen
    .getByRole("heading", {
      level: 2,
      name: "From incoming webhook to delivered event",
    })
    .closest("section");
  expect(section).not.toBeNull();
  const text = section!.textContent ?? "";
  expect(text).toMatch(/invalid credentials and unknown schemes are rejected/i);
  const marks = [
    "Send events to the systems you run",
    "Verify who sent each request",
    "Handle events and file uploads",
    "Decide which events go where",
    "Prepare data for the next service",
    "Route to the right environment",
    "Call APIs and return their responses",
    "Process batches with limits",
  ];
  let at = -1;
  for (const mark of marks) {
    const next = text.indexOf(mark);
    expect(next, mark).toBeGreaterThan(at);
    at = next;
  }
  expect(text).toMatch(/HTTP Basic/);
  expect(text).toMatch(/exact tokens/);
  expect(text).toMatch(/named connections/i);
  expect(text).toMatch(/DynamoDB/);
  expect(text).toMatch(/required JSON fields, value types and formats/);
  expect(text).toMatch(/ordered XML/);
  expect(text).toMatch(/batch size, concurrency/);
  expect(text).toMatch(/managed credential profiles/);
  expect(text).not.toMatch(/HMAC, static header, or JWT/);
});

test("the destination marquee explains Temporal workflow delivery", () => {
  wrap();
  expect(screen.getByRole("main")).toHaveTextContent(
    /Start Temporal workflows; your workers process the events/,
  );
});

test("the overview points to dynamic-routing use cases and the sourced comparison", () => {
  wrap();
  const main = screen.getByRole("main");
  expect(main).toHaveTextContent(
    /variables can come from a path, JSON body, or filtered header/i,
  );
  expect(main).toHaveTextContent(/\{envName\}-events/);
  expect(
    screen.getByRole("link", { name: "Explore use cases" }),
  ).toHaveAttribute("href", "/use-cases");
  expect(
    screen.getByRole("link", { name: "View the comparison" }),
  ).toHaveAttribute("href", "/compare");
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
    expect(
      copy,
      `marketing copy must not use generic hype ("${phrase}")`,
    ).not.toContain(phrase);
  }
});

test("performance measurements include hardware and synthetic workload limits", () => {
  wrap();
  const section = screen
    .getByRole("heading", {
      level: 2,
      name: "Control load, monitor delivery, and recover failures",
    })
    .closest("section");
  expect(section).toHaveTextContent(/measured: 5,000 requests\/s/i);
  expect(section).toHaveTextContent(/three 20-second bursts on two replicas/i);
  expect(section).toHaveTextContent(/2 vCPU and 4 GiB/i);
  expect(section).toHaveTextContent(/synthetic destination/i);
  expect(
    screen.getByRole("link", { name: "Results and methodology" }),
  ).toHaveAttribute("href", repoBlob("docs/evidence/performance.md"));
});

test("a real config sample is shown rather than described", () => {
  wrap();
  // The sample must look like the YAML a user would actually write.
  const sample = screen.getByLabelText("Example Hookway configuration");
  expect(sample.textContent).toContain("flows:");
  expect(sample.textContent).toContain("hmac_sha256");
  expect(sample.textContent).toContain("destinations:");
});

test("replay operations explain recovery, scoped access and the public builder", () => {
  wrap();
  const section = screen
    .getByRole("heading", {
      level: 2,
      name: "Recover missed deliveries with the replay console",
    })
    .closest("section");
  expect(section).toHaveTextContent(/sender got a success response/i);
  expect(section).toHaveTextContent(/without rerunning the whole flow/i);
  expect(section).toHaveTextContent(/scoped API key/i);
  expect(section).toHaveTextContent(/without signing in/i);
  expect(
    screen.getByRole("link", { name: "Deploy the replay console" }),
  ).toHaveAttribute("href", repoBlob("docs/operations/replay-console.md"));
  expect(
    screen.getByRole("img", { name: /replay console showing/i }),
  ).toHaveAttribute("loading", "lazy");
});

test("every destination type the builder supports is listed", () => {
  wrap();
  for (const t of [
    "rabbitmq",
    "sqs",
    "kafka",
    "temporal",
    "redis_stream",
    "webhook",
  ]) {
    expect(
      screen.getAllByText(destinationPresentation(t).label).length,
    ).toBeGreaterThanOrEqual(2);
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
  // It exists to keep the page a signpost: docs/reference/configuration.md alone is
  // ~95KB, and the site must not duplicate it.
  //
  // Include the ingress-adapter guide and its native capabilities, while
  // retaining a compact overview, including the replay investigation walkthrough,
  // rather than duplicating the reference.
  expect(copy.length).toBeLessThan(10000);
});
