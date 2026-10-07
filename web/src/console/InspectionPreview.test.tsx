import { fireEvent, render, screen } from "@testing-library/react";
import { MantineProvider } from "@mantine/core";
import { expect, test, vi } from "vitest";
import { decodeInspection, InspectionPreview, type Inspection } from "./InspectionPreview";

const inspection: Inspection = {
  version: 1,
  body: { content_type: "application/json", encoding: "utf8", content: '{"amount":9007199254740993,"decimal":0.1234567890123456789,"html":"<script>window.executed=true</script>"}' },
  headers: { authorization: ["[REDACTED]"] },
  variables: { tenant: "north" },
  method: "POST", path: "/hooks/orders",
};
function show(value = inspection) {
  const close = vi.fn();
  render(<MantineProvider><InspectionPreview value={value} onClose={close} /></MantineProvider>);
  return close;
}
test("wrapper decoding preserves body precision and rendering escapes active content", () => {
  const decoded = decodeInspection(JSON.stringify(inspection));
  const close = show(decoded);
  expect(screen.getByText(inspection.body.content)).toHaveTextContent("9007199254740993");
  expect(document.querySelector("script")).toBeNull();
  expect(screen.getByText("[REDACTED]")).toBeVisible();
  expect(screen.getByText("north")).toBeVisible();
  fireEvent.click(screen.getByRole("button", { name: "Close preview" }));
  expect(close).toHaveBeenCalledOnce();
});
test("binary and empty bodies have explicit states", () => {
  const { unmount } = render(<MantineProvider><InspectionPreview value={{ ...inspection, body: { content: "AAEC/w==", encoding: "base64", content_type: "application/octet-stream" } }} onClose={() => {}} /></MantineProvider>);
  expect(screen.getByText(/Base64 · binary body/)).toBeVisible();
  expect(screen.getByText("AAEC/w==")).toBeVisible();
  unmount();
  show({ ...inspection, body: { ...inspection.body, content: "" } });
  expect(screen.getByText("The captured body is empty.")).toBeVisible();
});
test("body preview is bounded independently of context", () => {
  show({ ...inspection, body: { ...inspection.body, content: "x".repeat(80 * 1024) } });
  expect(screen.getByText(/Showing the first 64 KiB/)).toBeVisible();
  expect(document.querySelector("pre")?.textContent?.length).toBe(64 * 1024);
  expect(screen.getByText("north")).toBeVisible();
});
test("unknown versions and malformed context cannot be displayed as valid inspections", () => {
  expect(() => decodeInspection(JSON.stringify({ ...inspection, version: 2 }))).toThrow(/version is not supported/);
  expect(() => decodeInspection(JSON.stringify({ ...inspection, body: { content: 42 } }))).toThrow(/invalid/);
  expect(() => decodeInspection(JSON.stringify({ ...inspection, headers: { secret: 3 } }))).toThrow(/invalid/);
  expect(() => decodeInspection(JSON.stringify({ ...inspection, variables: { bad: {} } }))).toThrow(/invalid/);
});
