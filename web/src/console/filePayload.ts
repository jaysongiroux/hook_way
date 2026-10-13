import { decodeInspection } from "./InspectionPreview";

export type CapturedFile = {
  field?: string;
  filename: string;
  contentType: string;
  size: number;
  blob?: Blob;
  unavailable?: string;
  generatedName?: boolean;
};

export function safeFilename(value: string, fallback = "file.bin"): string {
  const name = value.split(/[\\/]/).pop()?.replace(/[\x00-\x1f\x7f]/g, "_");
  return !name || name === "." || name === ".." ? fallback : name;
}

export function downloadFile(blob: Blob, filename: string) {
  // Captured MIME types and contents are data, never an inline browser viewer.
  const url = URL.createObjectURL(new Blob([blob], { type: "application/octet-stream" }));
  const anchor = document.createElement("a");
  anchor.href = url;
  anchor.download = safeFilename(filename);
  anchor.click();
  URL.revokeObjectURL(url);
}

function base64Blob(content: string): Blob {
  const decoded = atob(content);
  return new Blob([Uint8Array.from(decoded, (c) => c.charCodeAt(0))]);
}
function object(value: unknown): value is Record<string, unknown> {
  return !!value && typeof value === "object" && !Array.isArray(value);
}
function embeddedFiles(value: unknown): CapturedFile[] | undefined {
  if (!object(value)) return undefined;
  // Captured multipart bodies are either the current body or a broker envelope's body.
  const body = object(value.files) || typeof value.filename === "string" ? value : object(value.body) ? value.body : value;
  const entries: [string | undefined, unknown][] = object(body.files) ? Object.entries(body.files) :
    typeof body.filename === "string" && typeof body.content_type === "string" && typeof body.size === "number" ? [[undefined, body]] : [];
  if (!object(body.files) && !entries.length) return undefined;
  return entries.flatMap(([field, value]) => {
    if (!object(value) || typeof value.filename !== "string" || typeof value.content_type !== "string" ||
      typeof value.size !== "number" || !Number.isSafeInteger(value.size) || value.size < 0) return [];
    const file: CapturedFile = { field, filename: value.filename, contentType: value.content_type, size: value.size };
    if (value.content_omitted === true) {
      file.unavailable = "File contents were omitted by the flow configuration.";
    } else if (typeof value.content === "string") {
      try {
        file.blob = base64Blob(value.content);
        if (file.blob.size !== file.size) {
          file.blob = undefined;
          file.unavailable = "Captured file size does not match its contents.";
        }
      } catch {
        file.unavailable = "Captured file contents are not valid base64.";
      }
    } else {
      file.unavailable = "File contents were not captured.";
    }
    return [file];
  });
}

function bodyFilename(headers: Record<string, string[] | null>): string {
  const disposition = Object.entries(headers).find(([name]) => name.toLowerCase() === "content-disposition")?.[1]?.[0] || "";
  const encoded = /filename\*=UTF-8''([^;]+)/i.exec(disposition)?.[1];
  if (encoded) {
    try { return safeFilename(decodeURIComponent(encoded.trim())); } catch { /* use plain filename */ }
  }
  const plain = /(?:^|;)\s*filename=(?:"((?:\\.|[^"\\])*)"|([^;]*))/i.exec(disposition);
  return plain ? safeFilename((plain[1] ?? plain[2]).trim().replace(/\\(.)/g, "$1")) : "";
}

function defaultFilename(prefix: string, contentType: string): string {
  const extension: Record<string, string> = { "application/json": "json", "application/xml": "xml", "text/xml": "xml", "text/plain": "txt", "text/csv": "csv", "application/pdf": "pdf", "image/png": "png", "image/jpeg": "jpg", "application/zip": "zip" };
  return `${prefix}.${extension[contentType.split(";")[0].trim().toLowerCase()] || "bin"}`;
}

// Decoding is explicit and permission-gated by the caller. File bytes are kept
// in memory only, and are never put in DOM previews, URLs, or persisted state.
export async function capturedFiles(blob: Blob, inspection: boolean, contentType: string, filename?: string): Promise<CapturedFile[]> {
  if (inspection) {
    const value = decodeInspection(await blob.text());
    if (value.body.encoding === "utf8" && value.body.content_type.split(";")[0] === "application/json") {
      try {
        const files = embeddedFiles(JSON.parse(value.body.content));
        if (files) return files;
      } catch { /* the complete current body remains downloadable */ }
    }
    const body = value.body.encoding === "base64" ? base64Blob(value.body.content) : new Blob([value.body.content]);
    const name = bodyFilename(value.headers || {});
    return [{ filename: name || defaultFilename("body", value.body.content_type), generatedName: !name, contentType: value.body.content_type, size: body.size, blob: body }];
  }
  if (contentType.split(";")[0] === "application/json") {
    try {
      const files = embeddedFiles(JSON.parse(await blob.text()));
      if (files) return files;
    } catch { /* stored bytes remain downloadable even if they are not JSON */ }
  }
  if (contentType.toLowerCase().startsWith("multipart/form-data")) {
    const form = await new Response(blob, { headers: { "Content-Type": contentType } }).formData();
    return Array.from(form.entries()).flatMap(([field, file]) => typeof file === "string" ? [] : [{ field, filename: file.name, contentType: file.type, size: file.size, blob: file }]);
  }
  return [{ filename: filename || defaultFilename("payload", contentType), generatedName: !filename, contentType, size: blob.size, blob }];
}
