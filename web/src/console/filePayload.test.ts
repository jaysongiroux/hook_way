import { Blob as NodeBlob, File as NodeFile } from "node:buffer";
import { afterAll, expect, test, vi } from "vitest";
import { capturedFiles, safeFilename } from "./filePayload";

// jsdom lacks Blob.text/arrayBuffer; browsers and Node's Blob implement both.
vi.stubGlobal("Blob", NodeBlob);
// Node 26 fetch requires its own File brand when decoding multipart data.
vi.stubGlobal("File", NodeFile);
afterAll(() => vi.unstubAllGlobals());

const binary = [0, 1, 2, 255];
const upload = { fields: { account: "synthetic" }, files: {
  document: { filename: "receipt.pdf", content_type: "application/pdf", size: 4, content: "AAEC/w==" },
  "document#2": { filename: "empty.txt", content_type: "text/plain", size: 0, content: "" },
  notes: { filename: "notes.txt", content_type: "text/plain", size: 12, content_omitted: true },
} };
function inspection(body: unknown, type = "application/json", encoding = "utf8", headers = {}) {
  return new Blob([JSON.stringify({ version: 1, body: { content: typeof body === "string" ? body : JSON.stringify(body), content_type: type, encoding }, headers, variables: {}, method: "POST", path: "/upload" })]);
}
test("multipart inspections and broker envelopes expose exact individual bytes and omitted files", async () => {
  for (const [blob, inspect] of [[inspection(upload), true], [new Blob([JSON.stringify({ body: upload })]), false]] as const) {
    const files = await capturedFiles(blob, inspect, "application/json");
    expect(files.map((file) => file.field)).toEqual(["document", "document#2", "notes"]);
    expect(Array.from(new Uint8Array(await files[0].blob!.arrayBuffer()))).toEqual(binary);
    expect(files[0]).toMatchObject({ filename: "receipt.pdf", contentType: "application/pdf", size: 4 });
    expect(files[1].blob!.size).toBe(0);
    expect(files[2].blob).toBeUndefined();
    expect(files[2].unavailable).toMatch(/omitted/);
  }
});
test("binary inspection bodies and object payloads preserve file bytes and download names", async () => {
  const [body] = await capturedFiles(inspection("AAEC/w==", "application/pdf", "base64", { "content-disposition": ["attachment; filename*=UTF-8''r%C3%A9sum%C3%A9.pdf"] }), true, "application/json");
  expect(body.filename).toBe("résumé.pdf");
  expect(Array.from(new Uint8Array(await body.blob!.arrayBuffer()))).toEqual(binary);
  const [object] = await capturedFiles(new Blob([new Uint8Array(binary)]), false, "application/pdf", "original.pdf");
  expect(object.filename).toBe("original.pdf");
  expect(object.size).toBe(4);
});
test("a current body containing one file object exposes its file rather than a JSON download", async () => {
  const [file] = await capturedFiles(inspection(upload.files.document), true, "application/json");
  expect(file.filename).toBe("receipt.pdf");
  expect(file.field).toBeUndefined();
  expect(Array.from(new Uint8Array(await file.blob!.arrayBuffer()))).toEqual(binary);
});
test("invalid base64 and mismatched file sizes cannot be downloaded", async () => {
  const files = await capturedFiles(inspection({ files: {
    broken: { filename: "bad.bin", content_type: "", size: 4, content: "!bad" },
    mismatch: { filename: "short.bin", content_type: "", size: 2, content: "AAEC/w==" },
  } }), true, "application/json");
  expect(files.every((file) => !file.blob)).toBe(true);
  expect(files[0].unavailable).toMatch(/base64/);
  expect(files[1].unavailable).toMatch(/size/);
});
test("raw multipart payloads yield file downloads without including form fields", async () => {
  const blob = new Blob(['--upload\r\nContent-Disposition: form-data; name="account"\r\n\r\nsynthetic\r\n--upload\r\nContent-Disposition: form-data; name="document"; filename="raw.pdf"\r\nContent-Type: application/pdf\r\n\r\n', new Uint8Array(binary), '\r\n--upload--\r\n']);
  const files = await capturedFiles(blob, false, "multipart/form-data; boundary=upload");
  expect(files).toHaveLength(1);
  expect(files[0].filename).toBe("raw.pdf");
  expect(Array.from(new Uint8Array(await files[0].blob!.arrayBuffer()))).toEqual(binary);
});
test("current text body downloads preserve JSON number precision without reserializing it", async () => {
  const exact = '{"id":9007199254740993,"decimal":0.1234567890123456789}';
  const [file] = await capturedFiles(inspection(exact), true, "application/json");
  expect(await file.blob!.text()).toBe(exact);
});
test("download filenames cannot carry paths or control characters", () => {
  expect(safeFilename("../../reports/receipt.pdf")).toBe("receipt.pdf");
  expect(safeFilename("C:\\reports\\receipt.pdf")).toBe("receipt.pdf");
  expect(safeFilename("bad\u0000name.pdf")).toBe("bad_name.pdf");
  expect(safeFilename("..")).toBe("file.bin");
});

test("current-body files take precedence over an unrelated nested body object", async () => {
  const body = { body: { note: "operator data" }, files: { document: { filename: "empty.txt", content_type: "text/plain", size: 0, content: "" } } };
  const files = await capturedFiles(new Blob([JSON.stringify(body)]), false, "application/json");
  expect(files).toHaveLength(1);
  expect(files[0].filename).toBe("empty.txt");
  expect(files[0].blob?.size).toBe(0);
});
