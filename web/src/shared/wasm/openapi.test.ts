import { expect, test, vi } from "vitest";
import { checkDocument, MAX_OPENAPI_BYTES, OpenAPIImporter } from "./openapi";
function harness() {
  const worker = {
    onmessage: null as ((event: MessageEvent) => void) | null,
    onerror: null as (() => void) | null,
    postMessage: vi.fn(),
    terminate: vi.fn(),
  };
  const importer = new OpenAPIImporter(() => worker as unknown as Worker);
  return {
    worker,
    importer,
    respond: (id: number, result: unknown) =>
      worker.onmessage?.({ data: { id, result } } as MessageEvent),
  };
}
test("byte bounds count UTF-8 and reject blank documents", () => {
  expect(() => checkDocument(" ")).toThrow();
  expect(() => checkDocument("a".repeat(MAX_OPENAPI_BYTES))).not.toThrow();
  expect(() => checkDocument("é".repeat(MAX_OPENAPI_BYTES / 2 + 1))).toThrow();
});
test("messages retain original text, correlate responses and ignore stale replies", async () => {
  const h = harness();
  const first = h.importer.inspect("original: 9007199254740993");
  const second = h.importer.generate(
    "original: 9007199254740993",
    "settings: literal",
  );
  expect(h.worker.postMessage.mock.calls[0][0].input.document).toBe(
    "original: 9007199254740993",
  );
  h.respond(99, {});
  h.respond(2, { yaml: "generated", diagnostics: [] });
  await expect(second).resolves.toEqual({ yaml: "generated", diagnostics: [] });
  h.respond(1, { operations: [], diagnostics: [] });
  await expect(first).resolves.toEqual({ operations: [], diagnostics: [] });
});
test("cancellation rejects pending work, terminates runtime and permits retry", async () => {
  const h = harness();
  const first = h.importer.inspect("doc: first");
  const canceled = expect(first).rejects.toMatchObject({ name: "AbortError" });
  h.importer.cancel();
  await canceled;
  expect(h.worker.terminate).toHaveBeenCalledOnce();
  const second = h.importer.inspect("doc: second");
  h.respond(1, { operations: ["stale"] });
  h.respond(2, { operations: [] });
  await expect(second).resolves.toEqual({ operations: [] });
});
test("worker crashes reject pending work with actionable guidance", async () => {
  const h = harness();
  const promise = h.importer.inspect("doc: first");
  h.worker.onerror?.();
  await expect(promise).rejects.toThrow(/Retry.*hookway import-openapi/);
  expect(h.worker.terminate).toHaveBeenCalledOnce();
});
