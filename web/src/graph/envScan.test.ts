import { expect, test } from "vitest";
import { scanEnvVars } from "./envScan";

test("extracts unique sorted ${VAR} names", () => {
  const text = "url: ${RABBITMQ_URL}\nsecret: ${GH_SECRET}\nother: ${RABBITMQ_URL}";
  expect(scanEnvVars(text)).toEqual(["GH_SECRET", "RABBITMQ_URL"]);
});

test("returns empty for no references", () => {
  expect(scanEnvVars("addr: :8080")).toEqual([]);
});

test("includes env secret_ref names from inline and block mappings", () => {
  const inline = "secret_ref: {provider: env, name: STRIPE_WEBHOOK_SECRET, refresh: 15m}";
  expect(scanEnvVars(inline)).toEqual(["STRIPE_WEBHOOK_SECRET"]);
  const block = ["secret_ref:", "  provider: env", "  name: PADDLE_WEBHOOK_SECRET", "  overlap: 5m"].join("\n");
  expect(scanEnvVars(block)).toEqual(["PADDLE_WEBHOOK_SECRET"]);
});

test("ignores secret_ref names that are not environment variables", () => {
  expect(scanEnvVars("secret_ref: {provider: file, name: /run/secrets/hook}")).toEqual([]);
  expect(scanEnvVars("secret_ref:\n  provider: aws_secrets_manager\n  name: prod/stripe")).toEqual([]);
});
