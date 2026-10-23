import { expect, test } from "vitest";
import { render, screen } from "@testing-library/react";
import { MantineProvider } from "@mantine/core";
import { MemoryRouter } from "react-router-dom";
import { theme } from "../theme";
import { Marketing } from "./Marketing";
import { DOC_LINKS, GITHUB_REPO, docsHref } from "./links";
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
  expect(heading).toHaveTextContent(
    /route webhooks\. serve HTTP routes\. expose MCP tools/i,
  );
  const main = screen.getByRole("main");
  expect(main).toHaveTextContent(
    /handle synchronous HTTP requests as a reverse proxy or native flow/i,
  );
  expect(main).toHaveTextContent(/test deployed MCP tools from the builder/i);
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

test("features include MCP alongside native delivery and verification", () => {
  wrap();
  const section = screen
    .getByRole("heading", {
      level: 2,
      name: "Webhooks, HTTP handlers and MCP tools in the same builder",
    })
    .closest("section");
  expect(section).not.toBeNull();
  const text = section!.textContent ?? "";
  expect(text).toMatch(/invalid credentials and unknown schemes are rejected/i);
  const marks = [
    "Send events to the systems you run",
    "Expose APIs as MCP tools",
    "Verify who sent each request",
    "Handle events and file uploads",
    "Handle synchronous HTTP requests",
    "Decide which events go where",
    "Prepare data for the next service",
    "Route to the right environment",
    "Process batches with limits",
  ];
  for (const mark of marks) expect(text).toContain(mark);
  expect(text).toMatch(/HTTP Basic/);
  expect(text).toMatch(/exact tokens/);
  expect(text).toMatch(/named connections/i);
  expect(text).toMatch(/DynamoDB/);
  expect(text).toMatch(/required JSON fields, value types and formats/);
  expect(text).toMatch(/ordered XML/);
  expect(text).toMatch(/batch size, concurrency/);
  expect(text).toMatch(/same connection/);
  expect(text).not.toMatch(/HMAC, static header, or JWT/);
});

test("MCP connects API import, event-triggered lookup and delivery operations", () => {
  wrap();
  expect(
    screen.getByRole("link", { name: "authenticated MCP endpoints" }),
  ).toHaveAttribute("href", "#mcp-gateway");
  const section = screen
    .getByRole("heading", {
      name: "Give agents tools and events",
    })
    .closest("section")!;
  expect(section.parentElement).toHaveAttribute("id", "mcp-gateway");
  expect(section).toHaveTextContent(/Upload an OpenAPI spec/);
  expect(section).toHaveTextContent("issue.opened");
  expect(section).toHaveTextContent("get_issue");
  expect(section).toHaveTextContent(/clients that support MCP Events/);
  expect(section).toHaveTextContent(/Callback acceptance confirms receipt/);
  expect(section).toHaveTextContent(/API keys or an external OAuth provider/);
  expect(section).toHaveTextContent(/keep upstream credentials separate/i);
  expect(
    screen.getByRole("link", { name: "Import an API as MCP tools" }),
  ).toHaveAttribute("href", docsHref("docs/guides/openapi-import.md"));
  expect(
    screen.getByRole("link", { name: "Try the issue event demo" }),
  ).toHaveAttribute("href", docsHref("examples/mcp-events/demo/README.md"));
  expect(
    screen.getByRole("link", { name: "Explore event operations" }),
  ).toHaveAttribute("href", docsHref("docs/operations/mcp-events-console.md"));
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
  expect(section).toHaveTextContent(/5,000 requests\/s/i);
  expect(section).toHaveTextContent(/three 20-second bursts/i);
  expect(section).toHaveTextContent(
    /two replicas, each with 2 vCPU and 4 GiB/i,
  );
  expect(section).toHaveTextContent(/900,000 requests/);
  expect(section).toHaveTextContent(/25–50 ms/);
  expect(section).toHaveTextContent(/Histogram upper bounds/);
  expect(section).toHaveTextContent(/not a production capacity guarantee/);
  expect(section).toHaveTextContent(/synthetic destination/i);
  expect(
    screen.getByRole("link", { name: "Results and methodology" }),
  ).toHaveAttribute("href", docsHref("docs/evidence/performance.md"));
});

test("a real config sample is shown rather than described", () => {
  wrap();
  // The sample must look like the YAML a user would actually write.
  const sample = screen.getByLabelText("Example Hookway configuration");
  expect(sample.textContent).toContain("flows:");
  expect(sample.textContent).toContain("hmac_sha256");
  expect(sample.textContent).toContain("destinations:");
  const flow = screen.getByRole("figure", { name: /The github YAML flow/ });
  expect(
    Array.from(flow.querySelectorAll("li > code"), (node) => node.textContent),
  ).toEqual(["in", "rl", "body", "auth", "env", "out"]);
  expect(flow).toHaveTextContent("https://example.com/events");
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
  expect(screen.getByRole("main")).toHaveTextContent(/No sign-in required/);
  expect(
    screen.getByRole("link", { name: "Deploy the replay console" }),
  ).toHaveAttribute("href", docsHref("docs/operations/replay-console.md"));
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

test("documentation links open the published pages in the same tab", () => {
  wrap();
  for (const link of DOC_LINKS) {
    const el = screen.getByRole("link", { name: new RegExp(link.title, "i") });
    expect(el).toHaveAttribute("href", link.href);
    expect(el).not.toHaveAttribute("target");
    expect(el).toHaveAttribute("href", expect.stringMatching(/^\/docs\//));
  }
});

test("the page does not restate the documentation it links to", () => {
  wrap();
  // Keep the overview a signpost rather than a documentation rewrite.
  // Mantine's responsive components inject styles inside main, so exclude CSS
  // from the copy budget without changing the rendered page.
  const content = screen.getByRole("main").cloneNode(true) as HTMLElement;
  content.querySelectorAll("style").forEach((style) => style.remove());
  const copy = content.textContent!;
  expect(copy.length).toBeLessThan(13500);
});

test("replay section distinguishes spot checks from recovery and offers file downloads", () => {
  wrap();
  expect(screen.getByText(/For spot checks, put Sample/)).toHaveTextContent(
    /Inspections are view only/,
  );
  expect(screen.getByText(/For spot checks, put Sample/)).toHaveTextContent(
    /download captured files/,
  );
  expect(
    screen.getByRole("link", { name: "See the sampled inspection example" }),
  ).toHaveAttribute("href", docsHref("examples/inspect.yaml"));
});
