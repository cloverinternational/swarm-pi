import { describe, expect, it } from "vitest";
import { mkdtempSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { AgentManager, createPiRunner } from "../src/index.js";

describe("AgentManager", () => {
  it("runs a real Pi child with isolated control tools and inherited execution context", async () => {
    let seen: any;
    const cwd = mkdtempSync(join(tmpdir(), "pi-agent-runner-"));
    const runner = createPiRunner({ exec: async (...args: any[]) => { seen = args; return { code: 0, stdout: "child result", stderr: "", killed: false }; } });
    expect(await runner({ signal: new AbortController().signal, spec: { id: "x", task: "inspect" }, task: "inspect", cwd, instructions: [], steering: [] })).toBe("child result");
    expect(seen[0]).toBe("pi");
    expect(seen[1]).toEqual(expect.arrayContaining(["--mode", "text", "--session", `${cwd}/.pi/agent-sessions/x.jsonl`, "--exclude-tools", "Agent,AgentControl", "-p", "inspect"]));
    expect(seen[2]).toMatchObject({ cwd });
  });

  it("inherits profile capabilities and preserves parent identity", async () => {
    let seen: any;
    const manager = new AgentManager({ profiles: [{ name: "review", systemPrompt: "be precise", capabilities: ["read"] }], runner: async ctx => { seen = ctx; return "done"; } });
    const parent = manager.spawn({ id: "parent", task: "parent", profile: "review", provider: "openai", model: "gpt-test" });
    await parent.wait();
    const child = manager.spawn({ id: "child", task: "child", parentId: parent.id });
    expect(await child.wait()).toMatchObject({ id: "child", status: "completed", output: "done" });
    expect(seen.spec.parentId).toBe("parent");
    expect(seen.provider).toBe("openai");
    expect(seen.model).toBe("gpt-test");
    expect(seen.instructions).toContain("be precise");
    expect(seen.instructions).toContain("Capability: read");
  });
  it("supports cancellation and bounded concurrency", async () => {
    let running = 0, maximum = 0;
    const manager = new AgentManager({ concurrency: 1, runner: async ({ signal }) => { running++; maximum = Math.max(maximum, running); await new Promise<void>(resolve => { const timer = setTimeout(resolve, 30); signal.addEventListener("abort", () => { clearTimeout(timer); resolve(); }, { once: true }); }); running--; return "ok"; } });
    const first = manager.spawn({ id: "a", task: "a" });
    const second = manager.spawn({ id: "b", task: "b" });
    first.cancel();
    expect((await first.wait()).status).toBe("cancelled");
    expect((await second.wait()).status).toBe("completed");
    expect(maximum).toBe(1);
  });
  it("steers a running agent and emits completion", async () => {
    const manager = new AgentManager({ runner: async ({ signal, spec }) => { await new Promise(r => setTimeout(r, 5)); if (signal.aborted) throw new Error("cancelled"); return spec.task; } });
    const handle = manager.spawn({ id: "x", task: "work" });
    expect(handle.steer("focus")).toBe(true);
    let completed = false; manager.onComplete("x", () => { completed = true; });
    expect((await handle.wait()).output).toBe("work"); expect(completed).toBe(true);
  });
});
