import { editorAPIFallbackEnabled } from "./validationMode";
import { createEditorClient } from "../contracts/editorClient";
import type { components } from "../contracts/generated/editor";
import { hasWasmCapability, invokeWasm, loadWasm } from "./loader";

export type ScalarCoercion = components["schemas"]["ScalarCoercion"];
export type JSONSampleInput = components["schemas"]["JSONSampleValidationInput"];
export type JSONSampleResult = components["schemas"]["JSONSampleValidationResult"];

const editor = createEditorClient();

function resultOf(value: unknown): JSONSampleResult {
  if (!value || typeof value !== "object") throw new Error("Invalid JSON sample response");
  const result = value as Partial<JSONSampleResult>;
  if (typeof result.valid !== "boolean" || !Array.isArray(result.issues) ||
    result.issues.some((issue) => !issue || typeof issue !== "object" ||
      typeof issue.field !== "string" || typeof issue.path !== "string" ||
      typeof issue.keyword !== "string" || typeof issue.message !== "string") ||
    (result.valid && (result.issues.length !== 0 || typeof result.output !== "string")) ||
    (!result.valid && result.issues.length === 0) ||
    (result.output !== undefined && typeof result.output !== "string")) {
    throw new Error("Invalid JSON sample response");
  }
  return result as JSONSampleResult;
}

/** Validate a raw JSON sample with Go. The string is never parsed by JS, so
 * large numeric literals and original body text reach the Go validator. */
export async function validateJSONSample(input: JSONSampleInput): Promise<JSONSampleResult | null> {
  try {
    await loadWasm();
    if (hasWasmCapability("json_sample_validate")) {
      return resultOf(JSON.parse(invokeWasm("json_sample_validate", JSON.stringify(input))));
    }
  } catch {
    // The editor API shares the Go validator and is the fallback authority.
  }
  if (!editorAPIFallbackEnabled()) return null;
  try {
    const { data } = await editor.POST("/json-sample-validation", { body: input });
    return data ? resultOf(data) : null;
  } catch {
    return null;
  }
}
