import { expect, test } from "vitest";
import { scanEnvVars } from "./envScan";

test("extracts unique sorted ${VAR} names", () => {
  const text = "url: ${RABBITMQ_URL}\nsecret: ${GH_SECRET}\nother: ${RABBITMQ_URL}";
  expect(scanEnvVars(text)).toEqual(["GH_SECRET", "RABBITMQ_URL"]);
});

test("returns empty for no references", () => {
  expect(scanEnvVars("addr: :8080")).toEqual([]);
});
