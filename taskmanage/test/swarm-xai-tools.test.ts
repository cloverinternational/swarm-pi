import { afterEach, beforeEach, describe, expect, it } from "vitest";
import { mkdirSync, mkdtempSync, rmSync, writeFileSync } from "node:fs";
import { join } from "node:path";
import { tmpdir } from "node:os";
import { executeWebSearch, executeXSearch, hasCredentials } from "../../.pi/extensions/swarm-search.ts";
import { loadSwarmCanonicalTools, loadSwarmToolSurface, overlaySwarmToolSchemas } from "../../.pi/lib/swarm-tool-surface.ts";
import { xaiHasCredentials } from "../../.pi/lib/swarm-tool-gating.ts";

const env = { HOME: process.env.HOME, SWARM_HOME: process.env.SWARM_HOME, XAI_API_KEY: process.env.XAI_API_KEY };
let home: string;
beforeEach(() => { home = mkdtempSync(join(tmpdir(), "swarm-xai-")); process.env.HOME = home; delete process.env.SWARM_HOME; delete process.env.XAI_API_KEY; });
afterEach(() => { rmSync(home, { recursive: true, force: true }); for (const [k, v] of Object.entries(env)) { if (v === undefined) delete process.env[k]; else process.env[k] = v; } });
const text = (r: any) => r.content[0].text;
const err = (m: string) => `<error>\n  <message><![CDATA[${m}]]></message>\n</error>`;

describe("xAI tools (internal/tools/xai)", () => {
  it("keeps the conditional definitions out of the always-on surface but overlays them on the wire", () => {
    expect(loadSwarmToolSurface().has("x_search")).toBe(false);
    const canonical = loadSwarmCanonicalTools();
    expect(canonical.has("x_search") && canonical.has("xai_web_search")).toBe(true);
    const payload = { tools: [{ type: "function", function: { name: "x_search", description: "", parameters: { type: "object" } } }] };
    expect(overlaySwarmToolSchemas(payload)!.tools[0].function.description).toBe(canonical.get("x_search")!.description);
  });

  it("resolves credentials from <SWARM_HOME or ~/.swarm>/config/oauth/xai.json or XAI_API_KEY", () => {
    expect(hasCredentials()).toBe(false);
    expect(xaiHasCredentials(home, process.env)).toBe(false);
    const root = join(home, "custom"); process.env.SWARM_HOME = root;
    mkdirSync(join(root, "config", "oauth"), { recursive: true });
    writeFileSync(join(root, "config", "oauth", "xai.json"), JSON.stringify({ token: { access_token: "t", expires_at: 1 } }));
    expect(hasCredentials()).toBe(false); // expired, no refresh token
    expect(xaiHasCredentials(home, process.env)).toBe(false);
    writeFileSync(join(root, "config", "oauth", "xai.json"), JSON.stringify({ token: { access_token: "t", expires_at: 1, refresh_token: "r" } }));
    expect(hasCredentials()).toBe(true);
    expect(xaiHasCredentials(home, process.env)).toBe(true);
    delete process.env.SWARM_HOME; process.env.XAI_API_KEY = " k ";
    expect(hasCredentials()).toBe(true);
  });

  it("renders Swarm's validation errors in Go's order before any network call", async () => {
    expect(text(await executeXSearch({ query: " " }))).toBe(err("error: query is required"));
    expect(text(await executeXSearch({ query: "q" }))).toBe(err("error: no xAI credentials found — run /auth xai to sign in with SuperGrok OAuth"));
    process.env.XAI_API_KEY = "k";
    const eleven = Array.from({ length: 11 }, (_, i) => `h${i}`);
    expect(text(await executeXSearch({ query: "q", allowed_x_handles: ["@a", " ", ...eleven] }))).toBe(err("error: allowed_x_handles supports at most 10 handles"));
    expect(text(await executeXSearch({ query: "q", allowed_x_handles: ["@a"], excluded_x_handles: ["b"] }))).toBe(err("error: allowed_x_handles and excluded_x_handles cannot both be set"));
    // Blank/"@"-only allowed handles do not count as set.
    expect(text(await executeXSearch({ query: "q", allowed_x_handles: [" ", "@"], excluded_x_handles: eleven }))).toBe(err("error: excluded_x_handles supports at most 10 handles"));
    expect(text(await executeWebSearch({}))).toBe(err("error: query is required"));
    expect(text(await executeWebSearch({ query: "q", allowed_domains: ["a"], excluded_domains: ["b"] }))).toBe(err("error: allowed_domains and excluded_domains cannot both be set"));
    expect(text(await executeWebSearch({ query: "q", allowed_domains: ["1", "2", "3", "4", "5", "6"] }))).toBe(err("error: allowed_domains supports at most 5 domains"));
    expect(text(await executeWebSearch({ query: "q", allowed_domains: [" "], excluded_domains: ["1", "2", "3", "4", "5", "6"] }))).toBe(err("error: excluded_domains supports at most 5 domains"));
  });
});
