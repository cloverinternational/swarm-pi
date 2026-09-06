import { describe, expect, it, vi } from "vitest";
import extension, { fetchEvidence, validateURL } from "../../../../.pi/extensions/research-tools.ts";

describe("research tool policy", () => {
  it("allows HTTPS and rejects private or non-HTTPS URLs", () => {
    expect(validateURL("https://example.com/a").hostname).toBe("example.com");
    expect(() => validateURL("http://example.com")).toThrow("https");
    expect(() => validateURL("https://127.0.0.1")).toThrow("private");
    expect(() => validateURL("https://example.com", { allowedHosts: ["other.test"] })).toThrow("allowlist");
  });

  it("bounds response bytes and records provenance", async () => {
    vi.stubGlobal("fetch", vi.fn(async () => new Response("hello", { status: 200, headers: { "content-type": "text/plain" } })));
    const value = await fetchEvidence("https://example.com", {}, { maxBytes: 1024 });
    expect(value.text).toBe("hello");
    expect(value.provenance).toMatchObject({ url: "https://example.com/", status: 200, contentType: "text/plain", bytes: 5 });
    expect(value.provenance.sha256).toHaveLength(64);
    vi.unstubAllGlobals();
  });

  it("registers search-adjacent read tools without making network calls", () => {
    const tools: any[] = [];
    extension({ registerTool: (tool: any) => tools.push(tool) });
    expect(tools.map(t => t.name)).toEqual(["web_fetch", "deepwiki", "browser_get_page"]);
  });
});
