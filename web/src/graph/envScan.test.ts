import { expect, test } from "vitest";
import { scanEnvVars } from "./envScan";

test("extracts unique sorted ${VAR} names", () => {
  const text =
    "url: ${RABBITMQ_URL}\nsecret: ${GH_SECRET}\nother: ${RABBITMQ_URL}";
  expect(scanEnvVars(text)).toEqual(["GH_SECRET", "RABBITMQ_URL"]);
});

test("returns empty for no references", () => {
  expect(scanEnvVars("addr: :8080")).toEqual([]);
});

test("includes env secret_ref names from inline and block mappings", () => {
  const inline =
    "secret_ref: {provider: env, name: STRIPE_WEBHOOK_SECRET, refresh: 15m}";
  expect(scanEnvVars(inline)).toEqual(["STRIPE_WEBHOOK_SECRET"]);
  const block = [
    "secret_ref:",
    "  provider: env",
    "  name: PADDLE_WEBHOOK_SECRET",
    "  overlap: 5m",
  ].join("\n");
  expect(scanEnvVars(block)).toEqual(["PADDLE_WEBHOOK_SECRET"]);
});

test("ignores secret_ref names that are not environment variables", () => {
  expect(
    scanEnvVars("secret_ref: {provider: file, name: /run/secrets/hook}"),
  ).toEqual([]);
  expect(
    scanEnvVars(
      "secret_ref:\n  provider: aws_secrets_manager\n  name: prod/stripe",
    ),
  ).toEqual([]);
});

test("includes native credential references in YAML and JSON", () => {
  const config = {
    credentials: {
      sample: {
        client_id: { provider: "env", name: "SAMPLE_CLIENT" },
        client_secret: { provider: "file", name: "/run/secrets/sample" },
        secret_parameters: {
          password: { provider: "env", name: "SAMPLE_PASSWORD" },
        },
        headers: {
          "X-Sample-Key": {
            secret_ref: { provider: "env", name: "SAMPLE_KEY" },
          },
        },
      },
    },
  };
  expect(scanEnvVars(JSON.stringify(config))).toEqual([
    "SAMPLE_CLIENT",
    "SAMPLE_KEY",
    "SAMPLE_PASSWORD",
  ]);
  expect(
    scanEnvVars(
      "credentials:\n  sample:\n    client_id: {provider: env, name: SAMPLE_CLIENT}",
    ),
  ).toEqual(["SAMPLE_CLIENT"]);
});

test("handles alias cycles and incomplete YAML", () => {
  expect(
    scanEnvVars(
      "sample: &sample {provider: env, name: SAMPLE_KEY, nested: *sample}",
    ),
  ).toEqual(["SAMPLE_KEY"]);
  expect(scanEnvVars("invalid: [\nsecret: ${SAMPLE_TOKEN}")).toEqual([
    "SAMPLE_TOKEN",
  ]);
});
