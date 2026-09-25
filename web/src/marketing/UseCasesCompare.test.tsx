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
  expect(screen.getByRole("heading", { level: 1 })).toHaveTextContent(/patterns that stay in your infrastructure/i);
  const main = screen.getByRole("main");
  expect(main).toHaveTextContent(/store upload attachments in s3/i);
  expect(main).toHaveTextContent(/payload: object/i);
  expect(main).toHaveTextContent(/route each environment to its own queue/i);
  expect(main).toHaveTextContent(/filtered headers or the parsed body/i);
  expect(main).toHaveTextContent(/unknown, unsafe, or unresolved targets fail closed/i);
  for (const uc of USE_CASES) {
    expect(existsSync(resolve(repoRoot, uc.examplePath)), `${uc.examplePath} missing`).toBe(true);
    expect(screen.getByRole("link", { name: uc.exampleLabel })).toHaveAttribute("href", uc.exampleHref);
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
  expect(screen.getByRole("heading", { level: 1 })).toHaveTextContent(/how hookway compares/i);
  const main = screen.getByRole("main");
  expect(main).toHaveTextContent(/Hookdeck Event Gateway/);
  expect(main).toHaveTextContent(/Convoy/);
  expect(main).toHaveTextContent(/Outpost/);
  const lower = main.textContent!.toLowerCase();
  for (const banned of ["$/mo", "free tier", "pricing page", "per month", "subscribe now"]) {
    expect(lower, `must not discuss commercial packaging ("${banned}")`).not.toContain(banned);
  }
  for (const phrase of HYPE) {
    expect(lower, `must not use "${phrase}"`).not.toContain(phrase);
  }
});

test("compare matrix covers every competitor column and cites sources", () => {
  wrap(<Compare />);
  expect(screen.getByRole("region", { name: /scrollable comparison table/i })).toHaveAttribute("tabindex", "0");
  const table = screen.getByRole("table", { name: /feature and integration comparison/i });
  for (const c of COMPETITORS) {
    expect(within(table).getByRole("columnheader", { name: c.name })).toBeInTheDocument();
  }
  expect(COMPARE_ROWS.length).toBeGreaterThanOrEqual(8);
  expect(screen.getAllByRole("link", { name: /Hookdeck Destinations/i }).length).toBeGreaterThan(0);
  expect(screen.getByRole("link", { name: /Hookdeck Transformations/i })).toBeInTheDocument();
  expect(screen.getAllByRole("link", { name: /Hookdeck Outpost/i }).length).toBeGreaterThan(0);
  expect(screen.getByRole("link", { name: /Convoy Sources/i })).toBeInTheDocument();
  expect(screen.getByRole("link", { name: /Convoy Functions/i })).toBeInTheDocument();
  expect(screen.getByRole("link", { name: /Convoy Metrics/i })).toBeInTheDocument();
  expect(screen.getByRole("link", { name: "Hookdeck Limits" })).toHaveAttribute("href", "https://hookdeck.com/docs/limits");
  expect(screen.getByRole("link", { name: "Hookdeck Events" })).toHaveAttribute("href", "https://hookdeck.com/docs/events");
  expect(screen.getByRole("link", { name: "Hookdeck Retries" })).toHaveAttribute("href", "https://hookdeck.com/docs/retries");
  expect(screen.getByRole("link", { name: "Hookdeck Requests" })).toHaveAttribute("href", "https://hookdeck.com/docs/requests");
  expect(screen.getByRole("link", { name: "Convoy events and event deliveries" })).toHaveAttribute(
    "href",
    "https://www.getconvoy.io/docs/product-manual/events-and-event-deliveries",
  );
  expect(screen.getByRole("link", { name: "Hookway replay service" })).toHaveAttribute(
    "href",
    repoBlob("docs/replay-service.md"),
  );
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
  expect(row?.note).toMatch(/Temporal workflow settings are not yet exposed in standalone YAML/);
  wrap(<Compare />);
  expect(screen.getByRole("main")).toHaveTextContent(
    /Temporal workflow settings are not yet exposed in standalone YAML/,
  );
});

test("Hookway replay is the full optional service, not a partial", () => {
  const row = COMPARE_ROWS.find((r) => r.id === "replay")!;
  expect(row.values.hookway.level).toBe("yes");
  expect(row.values.hookway.level).not.toBe("partial");
  expect(row.capability).toMatch(/Inspect, download, and re-deliver failures/);
  expect(row.values.hookway.detail).toMatch(/optional replay service/i);
  expect(row.values.hookway.detail).toMatch(/16 MiB/);
  expect(row.values.hookway.detail).toMatch(/no product UI/i);
  expect(row.values.hookway.detail).toMatch(/no automatic re-drive/i);
  wrap(<Compare />);
  const main = screen.getByRole("main");
  expect(main).toHaveTextContent(/optional replay service/i);
  expect(main).toHaveTextContent(/no product UI/i);
  expect(main).toHaveTextContent(/no automatic re-drive/i);
  expect(screen.getByRole("rowheader", { name: /Inspect, download, and re-deliver failures/i })).toBeInTheDocument();
});

test("the matrix states large-payload retention with sourced limits", () => {
  const row = COMPARE_ROWS.find((r) => r.id === "large-payloads")!;
  expect(row.capability).toMatch(/Retain large \/ binary payloads for redelivery/);
  expect(row.values.hookway.level).toBe("yes");
  expect(row.values.hookway.detail).toMatch(/16 MiB/);
  expect(row.values.hookway.detail).toMatch(/publish\.payload: object|objects to S3 or redis_kv/);
  expect(row.values.hookdeck.level).toBe("partial");
  expect(row.values.hookdeck.detail).toMatch(/10 MiB/);
  expect(row.values.hookdeck.detail).toMatch(/PAYLOAD_TOO_LARGE/);
  expect(row.values.hookdeck.detail).toMatch(/2\.5 MB/);
  expect(row.values.convoy.level).toBe("partial");
  expect(row.values.convoy.detail).toMatch(/1–5 MB/);
  wrap(<Compare />);
  const main = screen.getByRole("main");
  expect(main).toHaveTextContent(/Retain large \/ binary payloads for redelivery/);
  expect(main).toHaveTextContent(/10 MiB/);
  expect(main).toHaveTextContent(/2\.5 MB/);
  expect(main).toHaveTextContent(/PAYLOAD_TOO_LARGE/);
  expect(main).toHaveTextContent(/1–5 MB/);
});

test("the compare page leads with wire-format verification and where Hookway differs", () => {
  wrap(<Compare />);
  const main = screen.getByRole("main");
  expect(main).toHaveTextContent(/self-hosted inbound gateway/i);
  expect(main).toHaveTextContent(/wire format/i);
  expect(main).toHaveTextContent(/11 wire-format schemes/);
  expect(main).toHaveTextContent(/HTTP Basic/);
  expect(main).toHaveTextContent(/HS256/);
  expect(main).toHaveTextContent(/fail closed/i);
  expect(screen.getByRole("heading", { name: /where hookway differs/i })).toBeInTheDocument();
  expect(main).toHaveTextContent(/Native destinations/);
  expect(main).toHaveTextContent(/Object and file publish/);
  expect(main).toHaveTextContent(/Optional large-artifact replay API/);
  expect(main).toHaveTextContent(/This product/);
  const table = screen.getByRole("table", { name: /feature and integration comparison/i });
  expect(within(table).getByRole("columnheader", { name: "Hookway" })).toHaveAttribute("data-product", "hookway");
});
