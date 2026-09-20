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
  const table = screen.getByRole("table");
  for (const c of COMPETITORS) {
    expect(within(table).getByRole("columnheader", { name: c.name })).toBeInTheDocument();
  }
  expect(COMPARE_ROWS.length).toBeGreaterThanOrEqual(8);
  expect(screen.getAllByRole("link", { name: /Hookdeck Destinations/i }).length).toBeGreaterThan(0);
  expect(screen.getByRole("link", { name: /Convoy Sources/i })).toBeInTheDocument();
});

test("compare page states Hookdeck Event Gateway destinations are HTTP-oriented", () => {
  wrap(<Compare />);
  expect(screen.getByRole("main")).toHaveTextContent(/HTTP, CLI \(local\), and Mock API only/);
});
