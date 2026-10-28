import { expect, test } from "vitest";
import { existsSync } from "node:fs";
import { resolve } from "node:path";
import { fireEvent, render, screen, within } from "@testing-library/react";
import { MantineProvider } from "@mantine/core";
import { MemoryRouter } from "react-router-dom";
import type { ReactElement } from "react";
import { theme } from "../theme";
import { Compare } from "./Compare";
import { UseCases } from "./UseCases";
import { USE_CASES } from "./useCasesCopy";
import { COMPARE_ROWS, COMPETITORS } from "./compareCopy";
import { docsHref } from "./links";

const repoRoot = resolve(__dirname, "../../..");

function wrap(ui: ReactElement) {
  return render(
    <MantineProvider theme={theme}>
      <MemoryRouter>{ui}</MemoryRouter>
    </MantineProvider>,
  );
}

const HYPE = [
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
];

test("use-cases page states the S3 attachment pattern and links examples that exist", () => {
  wrap(<UseCases />);
  expect(screen.getByRole("heading", { level: 1 })).toHaveTextContent(
    /patterns that stay in your infrastructure/i,
  );
  const main = screen.getByRole("main");
  for (const uc of USE_CASES) {
    fireEvent.change(
      screen.getByLabelText("Choose a use case", { selector: "select" }),
      {
        target: { value: uc.id },
      },
    );
    expect(
      screen.getByRole("heading", { level: 2, name: uc.title }),
    ).toBeInTheDocument();
    expect(main).toHaveTextContent(uc.summary);
    expect(main).toHaveTextContent(uc.detail);
    expect(within(main).getByRole("img")).toBeInTheDocument();
    expect(
      existsSync(resolve(repoRoot, uc.examplePath)),
      `${uc.examplePath} missing`,
    ).toBe(true);
    expect(screen.getByRole("link", { name: uc.exampleLabel })).toHaveAttribute(
      "href",
      uc.exampleHref,
    );
  }
});

test("use-cases page avoids generic hype", () => {
  wrap(<UseCases />);
  const copy = screen.getByRole("main").textContent!.toLowerCase();
  for (const phrase of HYPE) {
    expect(copy, `must not use "${phrase}"`).not.toContain(phrase);
  }
});

test("compare page names Hookdeck and Convoy without pricing language", () => {
  wrap(<Compare />);
  expect(screen.getByRole("heading", { level: 1 })).toHaveTextContent(
    /how hookway compares/i,
  );
  const main = screen.getByRole("main");
  expect(main).toHaveTextContent(/Hookdeck Event Gateway/);
  expect(main).toHaveTextContent(/Convoy/);
  expect(main).toHaveTextContent(/Outpost/);
  const lower = main.textContent!.toLowerCase();
  for (const banned of [
    "$/mo",
    "free tier",
    "pricing page",
    "per month",
    "subscribe now",
  ]) {
    expect(
      lower,
      `must not discuss commercial packaging ("${banned}")`,
    ).not.toContain(banned);
  }
  for (const phrase of HYPE) {
    expect(lower, `must not use "${phrase}"`).not.toContain(phrase);
  }
});

test("compare matrix covers every competitor column and cites sources", () => {
  wrap(<Compare />);
  expect(
    screen.getByRole("region", { name: /scrollable comparison table/i }),
  ).toHaveAttribute("tabindex", "0");
  const table = screen.getByRole("table", {
    name: /feature and integration comparison/i,
  });
  for (const c of COMPETITORS) {
    expect(
      within(table).getByRole("columnheader", { name: c.name }),
    ).toBeInTheDocument();
  }
  expect(COMPARE_ROWS.length).toBeGreaterThanOrEqual(8);
  expect(
    screen.getAllByRole("link", { name: /Hookdeck Destinations/i }).length,
  ).toBeGreaterThan(0);
  expect(
    screen.getByRole("link", { name: /Hookdeck Transformations/i }),
  ).toBeInTheDocument();
  expect(
    screen.getAllByRole("link", { name: /Hookdeck Outpost/i }).length,
  ).toBeGreaterThan(0);
  expect(
    screen.getByRole("link", { name: /Convoy Sources/i }),
  ).toBeInTheDocument();
  expect(
    screen.getByRole("link", { name: /Convoy Functions/i }),
  ).toBeInTheDocument();
  expect(
    screen.getByRole("link", { name: /Convoy Metrics/i }),
  ).toBeInTheDocument();
  expect(screen.getByRole("link", { name: "Hookdeck Limits" })).toHaveAttribute(
    "href",
    "https://hookdeck.com/docs/limits",
  );
  expect(screen.getByRole("link", { name: "Hookdeck Events" })).toHaveAttribute(
    "href",
    "https://hookdeck.com/docs/events",
  );
  expect(
    screen.getByRole("link", { name: "Hookdeck Retries" }),
  ).toHaveAttribute("href", "https://hookdeck.com/docs/retries");
  expect(
    screen.getByRole("link", { name: "Hookdeck Requests" }),
  ).toHaveAttribute("href", "https://hookdeck.com/docs/requests");
  expect(
    screen.getByRole("link", { name: "Convoy events and event deliveries" }),
  ).toHaveAttribute(
    "href",
    "https://www.getconvoy.io/docs/product-manual/events-and-event-deliveries",
  );
  expect(
    screen.getByRole("link", { name: "Convoy retry event delivery" }),
  ).toHaveAttribute(
    "href",
    "https://www.getconvoy.io/docs/api-reference/event-deliveries/retry-event-delivery",
  );
  expect(
    screen.getByRole("link", { name: "Hookway replay service" }),
  ).toHaveAttribute("href", docsHref("docs/operations/replay-service.md"));
  expect(
    existsSync(resolve(repoRoot, "docs/operations/replay-service.md")),
  ).toBe(true);
});

test("compare page states Hookdeck Event Gateway destinations are HTTP-oriented", () => {
  wrap(<Compare />);
  const main = screen.getByRole("main");
  expect(main).toHaveTextContent(/HTTP, CLI for local testing, and Mock API/);
  expect(main).toHaveTextContent(/Preserves arbitrary request bodies/);
});

test("the destinations row describes configurable Temporal workflows", () => {
  const row = COMPARE_ROWS.find((r) => r.id === "destinations");
  expect(row?.note).toMatch(
    /Configure Temporal workflow type, input, IDs and timeouts in YAML or the builder/,
  );
  wrap(<Compare />);
  expect(screen.getByRole("main")).toHaveTextContent(
    /Configure Temporal workflow type, input, IDs and timeouts in YAML or the builder/,
  );
});

test("Hookway replay is the full optional service, not a partial", () => {
  const row = COMPARE_ROWS.find((r) => r.id === "replay")!;
  expect(row.values.hookway.level).toBe("yes");
  expect(row.values.hookway.level).not.toBe("partial");
  expect(row.capability).toBe("Inspect and retry failed deliveries");
  expect(row.values.hookway.detail).toMatch(/optional hookway-replay/i);
  expect(
    COMPARE_ROWS.find((r) => r.id === "payload-retention")!.values.hookway
      .detail,
  ).toMatch(/16 MiB/);
  expect(row.values.hookway.detail).toMatch(/resumes at the failed node/i);
  expect(row.values.hookway.detail).toMatch(/scoped operations console/i);
  expect(row.values.hookway.detail).toMatch(/no automatic re-drive/i);
  expect(row.values.hookdeck.level).toBe("yes");
  expect(
    COMPARE_ROWS.find((r) => r.id === "payload-retention")!.values.hookdeck
      .detail,
  ).toMatch(/413/);
  expect(row.values.hookdeck.detail).toMatch(/bulk replay of requests/);
  expect(row.values.convoy.level).toBe("yes");
  expect(row.values.convoy.detail).toMatch(/linear or exponential/);
  wrap(<Compare />);
  const main = screen.getByRole("main");
  expect(main).toHaveTextContent(/optional hookway-replay/i);
  expect(main).toHaveTextContent(/scoped operations console/i);
  expect(main).toHaveTextContent(/no automatic re-drive/i);
  expect(
    screen.getByRole("rowheader", {
      name: "Inspect and retry failed deliveries",
    }),
  ).toBeInTheDocument();
});

test("the matrix separates payload retention from object publish", () => {
  const retained = COMPARE_ROWS.find((r) => r.id === "payload-retention")!;
  expect(retained.capability).toBe("Payload limits");
  expect(retained.note).toMatch(/separate limits/);
  expect(retained.values.hookway.level).toBe("yes");
  expect(retained.values.hookway.detail).toMatch(/16 MiB/);
  expect(retained.values.hookdeck.level).toBe("partial");
  expect(retained.values.hookdeck.detail).toMatch(/10 MiB/);
  expect(retained.values.hookdeck.detail).toMatch(/2\.5 MB/);
  expect(retained.values.convoy.level).toBe("n/a");
  expect(retained.values.convoy.detail).toMatch(
    /does not state an enforced payload-size ceiling/,
  );

  const files = COMPARE_ROWS.find((r) => r.id === "object-store")!;
  expect(files.capability).toBe("Multipart / decoded file → object storage");
  expect(files.values.hookway.level).toBe("yes");
  expect(files.values.hookway.detail).toMatch(/publish\.payload: object/);
  expect(files.values.hookway.detail).toMatch(/S3 or redis_kv/);
  expect(files.values.hookdeck.level).toBe("no");
  expect(files.values.hookdeck.detail).toMatch(/Outpost is a separate product/);
  expect(files.values.convoy.level).toBe("no");
  expect(files.values.convoy.detail).toMatch(/S3 retention archive/);

  wrap(<Compare />);
  const main = screen.getByRole("main");
  expect(main).toHaveTextContent(/Payload limits/);
  expect(main).toHaveTextContent(/Multipart \/ decoded file → object storage/);
  expect(main).toHaveTextContent(/10 MiB/);
  expect(main).toHaveTextContent(/2\.5 MB/);
  expect(main).not.toHaveTextContent(/1–5 MB/);
  expect(COMPARE_ROWS.some((r) => /operator surface/i.test(r.capability))).toBe(
    false,
  );
});

test("the compare page leads with the publish wedge and where Hookway differs", () => {
  wrap(<Compare />);
  const main = screen.getByRole("main");
  expect(main).toHaveTextContent(
    /webhook delivery, API routing and MCP tools/i,
  );
  expect(main).toHaveTextContent(
    /sends events directly to queues, streams, databases, and object storage/i,
  );
  expect(main).toHaveTextContent(/15 authentication formats/);
  expect(main).toHaveTextContent(/HTTP Basic/);
  expect(main).toHaveTextContent(/HS256/);
  expect(main).toHaveTextContent(/unknown schemes are rejected/i);
  expect(
    screen.getByRole("heading", { name: /where hookway differs/i }),
  ).toBeInTheDocument();
  expect(main).toHaveTextContent(/Native destinations/);
  expect(main).toHaveTextContent(/Object and file publish/);
  expect(main).toHaveTextContent(/Optional hookway-replay/);
  expect(main).toHaveTextContent(/This product/);
  const table = screen.getByRole("table", {
    name: /feature and integration comparison/i,
  });
  expect(
    within(table).getByRole("columnheader", { name: "Hookway" }),
  ).toHaveAttribute("data-product", "hookway");
});

test("comparison leads with supported destination counts", () => {
  expect(COMPARE_ROWS.some((r) => r.values.hookway.level === "partial")).toBe(
    false,
  );
  expect(COMPARE_ROWS.some((r) => r.id === "automatic-recovery")).toBe(false);
  const destinations = COMPARE_ROWS.find((r) => r.id === "destinations")!;
  expect(destinations.values.hookway.count).toBe(15);
  expect(destinations.values.hookdeck.count).toBe(3);
  expect(destinations.values.convoy.count).toBe(1);
  expect(destinations.values.hookdeck.detail).toMatch(
    /Outpost.*11 destination types/,
  );
  wrap(<Compare />);
  const row = screen
    .getByRole("rowheader", { name: /Native delivery destinations/ })
    .closest("tr")!;
  expect(within(row).getByText("15 types")).toBeInTheDocument();
  expect(within(row).getByText("3 types")).toBeInTheDocument();
  expect(within(row).getByText("1 type")).toBeInTheDocument();
  expect(within(row).queryByText("Yes")).not.toBeInTheDocument();
});

test("each comparison cell links to its supporting source", () => {
  wrap(<Compare />);
  for (const row of COMPARE_ROWS) {
    for (const c of COMPETITORS) {
      expect(
        screen.getByRole("link", {
          name: `${c.name}: ${row.capability} documentation`,
        }),
      ).toHaveAttribute("href", row.sources[c.id]);
    }
  }
});

test("the MCP comparison distinguishes custom tools from gateway administration without unsupported absence claims", () => {
  const row = COMPARE_ROWS.find((row) => row.id === "mcp")!;
  expect(row.values.hookway.level).toBe("yes");
  expect(row.values.hookway.detail).toMatch(
    /API keys and external-provider OAuth/,
  );
  expect(row.values.hookdeck.level).toBe("yes");
  expect(row.values.hookdeck.detail).toMatch(/stdio/);
  expect(row.values.convoy.level).toBe("n/a");
  expect(row.note).toContain("October 16, 2026");
  expect(row.sources.hookway).toBe(docsHref("docs/guides/mcp-gateway.md"));
  wrap(<UseCases />);
  expect(
    screen.getByRole("link", { name: "mcp-gateway.yaml" }),
  ).toHaveAttribute("href", docsHref("examples/templates/mcp-gateway.yaml"));
});

test("sampled inspection copy points to the runnable observation branch", () => {
  wrap(<UseCases />);
  fireEvent.change(
    screen.getByLabelText("Choose a use case", { selector: "select" }),
    {
      target: { value: "sampled-inspection" },
    },
  );
  const pattern = USE_CASES.find((item) => item.id === "sampled-inspection")!;
  expect(pattern.examplePath).toBe("examples/inspect.yaml");
  expect(pattern.detail).toMatch(/separate observation branch/);
  expect(pattern.detail).toMatch(/Publish receives every event/);
  expect(pattern.detail).toMatch(/view only/);
  expect(pattern.detail).toMatch(/best effort/);
  expect(screen.getByRole("link", { name: "inspect.yaml" })).toHaveAttribute(
    "href",
    docsHref("examples/inspect.yaml"),
  );
});
