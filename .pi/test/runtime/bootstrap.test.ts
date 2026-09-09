import { describe, expect, it, vi } from "vitest";
import { runBootstrap, readBootstrapSettings, settingsPath, writeBootstrapSettings } from "../../../packages/runtime/bootstrap/src/index.ts";
import { mkdtempSync, readFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";

describe("bootstrap runtime", () => {
  const selection = async (kind: string) => ({ memories: kind === "memory" ? [{ text: "remembered" }] : [], skills: kind === "skills" ? [{ name: "loop", body: "instructions" }] : [], evidence: [kind] });
  it("runs parallel selectors concurrently and drafts once", async () => {
    const draft = vi.fn(async (_task, picked) => ({ subject: "task", description: picked.evidence.join(",") }));
    const result = await runBootstrap("parallel", "build", { memory: () => selection("memory"), skills: () => selection("skills") }, draft);
    expect(result.status).toBe("ready"); expect(result.selection?.memories).toHaveLength(1); expect(result.selection?.skills).toHaveLength(1); expect(draft).toHaveBeenCalledTimes(1);
  });
  it("handles cancellation and selector failure without pretending ready", async () => {
    const controller = new AbortController(); controller.abort();
    expect((await runBootstrap("combined", "x", selection, undefined, controller.signal)).status).toBe("cancelled");
    expect((await runBootstrap("combined", "x", async () => { throw new Error("no recall"); }, undefined)).status).toBe("degraded");
  });
  it("persists settings atomically and reloads", () => {
    const cwd = mkdtempSync(join(tmpdir(), "pi-bootstrap-")); writeBootstrapSettings(cwd, "combined");
    expect(readBootstrapSettings(cwd).mode).toBe("combined"); expect(readFileSync(settingsPath(cwd), "utf8")).toContain('"mode":"combined"');
  });
  it("preserves a model override across mode changes and can restore inheritance", () => {
    const cwd = mkdtempSync(join(tmpdir(), "pi-bootstrap-model-"));
    expect(readBootstrapSettings(cwd).model).toBeUndefined();
    writeBootstrapSettings(cwd, "parallel", "provider/chosen");
    writeBootstrapSettings(cwd, "combined");
    expect(readBootstrapSettings(cwd).model).toBe("provider/chosen");
    writeBootstrapSettings(cwd, "combined", "");
    expect(readBootstrapSettings(cwd).model).toBeUndefined();
  });
});
