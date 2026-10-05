import { describe, expect, it } from "vitest";
import { parseRuntimeConfig } from "./runtimeConfig";

describe("deployment API URL", () => {
  it("accepts an absolute API URL with an optional deployment prefix", () => {
    expect(parseRuntimeConfig({ replayApiUrl: "https://api.example/replay/" }).replayApiUrl).toBe("https://api.example/replay");
    expect(parseRuntimeConfig({ replayApiUrl: "https://api.example/v1" }).replayApiUrl).toBe("https://api.example/v1");
  });
  it.each(["", "/v1", "ftp://api.example", "https://user:secret@api.example", "https://api.example?key=value", "https://api.example#fragment", " https://api.example", "https://api.example?", "https://api.example#", "https://api.example\\\\elsewhere"])("rejects unsafe or incomplete configuration %s", (replayApiUrl) => {
    expect(() => parseRuntimeConfig({ replayApiUrl })).toThrow();
  });
});
