import { expect, test } from "vitest";
import { existsSync } from "node:fs";
import { resolve } from "node:path";
import { render, screen, within } from "@testing-library/react";
import { MantineProvider } from "@mantine/core";
import { MemoryRouter } from "react-router-dom";
import type { ReactElement } from "react";
import { theme } from "../theme";
import { Compare } from "./Compare";
import { UseCases } from "./UseCases";
import { USE_CASES } from "./useCasesCopy";
import { COMPARE_ROWS, COMPETITORS } from "./compareCopy";
import { repoBlob } from "./links";

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
  expect(main).toHaveTextContent(/store upload attachments in s3/i);
  expect(main).toHaveTextContent(/payload: object/i);
  expect(main).toHaveTextContent(/route each environment to its own queue/i);
  expect(main).toHaveTextContent(/parsed body, query, or headers/i);
  expect(main).toHaveTextContent(/stable sender event ID/i);
  expect(main).toHaveTextContent(
    /unknown, unsafe, or unresolved targets fail closed/i,
  );
  for (const uc of USE_CASES) {
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
  ).toHaveAttribute("href", repoBlob("docs/replay-service.md"));
  expect(existsSync(resolve(repoRoot, "docs/replay-service.md"))).toBe(true);
});

test("compare page states Hookdeck Event Gateway destinations are HTTP-oriented", () => {
  wrap(<Compare />);
  const main = screen.getByRole("main");
  expect(main).toHaveTextContent(/HTTP, CLI \(local\), and Mock API only/);
  expect(main).toHaveTextContent(/Preserves arbitrary request bodies/);
});

test("the destinations row notes that Temporal workflow settings are not in standalone YAML", () => {
  const row = COMPARE_ROWS.find((r) => r.id === "destinations");
  expect(row?.note).toMatch(
    /Temporal workflow settings are not yet exposed in standalone YAML/,
  );
  wrap(<Compare />);
  expect(screen.getByRole("main")).toHaveTextContent(
    /Temporal workflow settings are not yet exposed in standalone YAML/,
  );
});

test("Hookway replay is the full optional service, not a partial", () => {
  const row = COMPARE_ROWS.find((r) => r.id === "replay")!;
  expect(row.values.hookway.level).toBe("yes");
  expect(row.values.hookway.level).not.toBe("partial");
  expect(row.capability).toBe("Inspect and retry failed deliveries");
  expect(row.values.hookway.detail).toMatch(/optional hookway-replay/i);
  expect(row.values.hookway.detail).toMatch(/max_record_bytes 16 MiB/);
  expect(row.values.hookway.detail).toMatch(/resumes at the failed node/i);
  expect(row.values.hookway.detail).toMatch(/no product UI/i);
  expect(row.values.hookway.detail).toMatch(/no automatic re-drive/i);
  expect(row.values.hookdeck.level).toBe("yes");
  expect(row.values.hookdeck.detail).toMatch(/413/);
  expect(row.values.hookdeck.detail).toMatch(/bulk replay of requests/);
  expect(row.values.convoy.level).toBe("yes");
  expect(row.values.convoy.detail).toMatch(/linear or exponential/);
  wrap(<Compare />);
  const main = screen.getByRole("main");
  expect(main).toHaveTextContent(/optional hookway-replay/i);
  expect(main).toHaveTextContent(/no product UI/i);
  expect(main).toHaveTextContent(/no automatic re-drive/i);
  expect(
    screen.getByRole("rowheader", {
      name: "Inspect and retry failed deliveries",
    }),
  ).toBeInTheDocument();
});

test("the matrix separates payload retention from object publish", () => {
  const retained = COMPARE_ROWS.find((r) => r.id === "payload-retention")!;
  expect(retained.capability).toBe("Payload size retained for inspect/replay");
  expect(retained.values.hookway.level).toBe("yes");
  expect(retained.values.hookway.detail).toMatch(/16 MiB/);
  expect(retained.values.hookdeck.level).toBe("partial");
  expect(retained.values.hookdeck.detail).toMatch(/10 MiB/);
  expect(retained.values.hookdeck.detail).toMatch(/2\.5 MB/);
  expect(retained.values.convoy.level).toBe("partial");
  expect(retained.values.convoy.detail).toMatch(/1–5 MB/);

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
  expect(main).toHaveTextContent(/Payload size retained for inspect\/replay/);
  expect(main).toHaveTextContent(/Multipart \/ decoded file → object storage/);
  expect(main).toHaveTextContent(/10 MiB/);
  expect(main).toHaveTextContent(/2\.5 MB/);
  expect(main).toHaveTextContent(/1–5 MB/);
  expect(COMPARE_ROWS.some((r) => /operator surface/i.test(r.capability))).toBe(
    false,
  );
});

test("the compare page leads with the publish wedge and where Hookway differs", () => {
  wrap(<Compare />);
  const main = screen.getByRole("main");
  expect(main).toHaveTextContent(
    /gateways receive, verify, and deliver webhooks/i,
  );
  expect(main).toHaveTextContent(
    /sends events directly to queues, streams, databases, and object storage/i,
  );
  expect(main).toHaveTextContent(/13 authentication formats/);
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
