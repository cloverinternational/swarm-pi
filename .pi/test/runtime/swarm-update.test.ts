import { describe, expect, it, vi } from "vitest";
import { compareVersions, validateManifest } from "../../extensions/00-runtime/swarm-update.ts";

describe("swarm update metadata", () => {
  it("compares release and prerelease versions", () => {
    expect(compareVersions("1.2.0", "1.1.9")).toBe(1);
    expect(compareVersions("1.0.0", "1.0.0-rc.1")).toBe(1);
    expect(compareVersions("1.0.0-rc.2", "1.0.0-rc.10")).toBe(-1);
    expect(compareVersions("1.0.0", "1.0.0")).toBe(0);
  });

  it("accepts only the documented manifest shape", () => {
    const value = { schemaVersion: 1, package: "@pi-swarm/integration", version: "1.2.3", releasedAt: "2026-01-01T00:00:00Z", changelog: "https://example.test/changelog", source: "git:github.com/example/repo@main", updateCommand: "pi update --extensions" };
    expect(validateManifest(value)).toEqual(value);
    expect(validateManifest({ ...value, version: "latest" })).toBeUndefined();
    expect(validateManifest({ ...value, schemaVersion: 2 })).toBeUndefined();
  });
});

describe("swarm update extension", () => {
  it("checks at startup and does not check again before the hourly interval", async () => {
    vi.stubGlobal("fetch", vi.fn().mockResolvedValue({ ok: true, json: async () => ({ schemaVersion: 1, package: "@pi-swarm/integration", version: "0.2.0", releasedAt: "2026-01-01T00:00:00Z", changelog: "https://example.test/changelog", source: "git:github.com/example/repo@main", updateCommand: "pi update --extensions" }) }));
    const handlers = new Map<string, (event: unknown, ctx: any) => void>(); const commands = new Map<string, any>(); const notify = vi.fn();
    const pi = { on: (event: string, handler: any) => handlers.set(event, handler), registerCommand: (name: string, command: any) => commands.set(name, command) };
    const extension = (await import("../../extensions/00-runtime/swarm-update.ts")).default(pi);
    const ctx = { ui: { notify } }; handlers.get("session_start")?.({}, ctx); await new Promise((resolve) => setTimeout(resolve, 0));
    expect(fetch).toHaveBeenCalledTimes(1); expect(notify).toHaveBeenCalledWith(expect.stringContaining("0.2.0"), "warning");
    await extension.checkForUpdate(); expect(fetch).toHaveBeenCalledTimes(1);
    handlers.get("session_shutdown")?.({}, ctx); vi.unstubAllGlobals();
  });
});
