import { afterEach, describe, expect, it, vi } from "vitest";
import { existsSync, readFileSync } from "node:fs";
import { grepTranscript, readTranscriptLines, registerSwarmGoal } from "../../lib/tools/swarm-goal.ts";
function harness(evaluate: any = async () => "MET") {
  const tools = new Map<string, any>(), commands = new Map<string, any>(), handlers = new Map<string, any[]>();
  const ctx = { ui: { notify: vi.fn() }, sessionManager: { getBranch: () => [] } };
  const pi = { registerTool: (t: any) => tools.set(t.name, t), registerCommand: (n: string, c: any) => commands.set(n, c), on: (n: string, h: any) => handlers.set(n, [...handlers.get(n) ?? [], h]), sendMessage: vi.fn(), appendEntry: vi.fn() };
  const api = registerSwarmGoal(pi, { evaluate });
  return { pi, api, ctx, tool: (p: any) => tools.get("scheduler").execute("test", p), command: (n: string, args: string) => commands.get(n).handler(args, ctx), emit: async (n: string, e: any = {}) => { for (const h of handlers.get(n) ?? []) await h(e, ctx); } };
}
afterEach(() => vi.useRealTimers());
describe("swarm-goal", () => {
  it("fires a precise one-shot and cancels another without polling", async () => {
    vi.useFakeTimers(); const h = harness(); await h.emit("session_start");
    await h.tool({ action: "create", delay: "15s", interval: "", cron: "", prompt: "resume" });
    const second = await h.tool({ action: "create", delay: "16s", prompt: "cancelled" });
    await h.tool({ action: "cancel", id: second.details.id });
    await vi.advanceTimersByTimeAsync(14999); expect(h.pi.sendMessage).not.toHaveBeenCalled();
    await vi.advanceTimersByTimeAsync(1001); expect(h.pi.sendMessage).toHaveBeenCalledTimes(1);
    expect((await h.tool({ action: "list" })).details).toEqual([]);
  });
  it("rejects malformed schedules and invalidates timers on reload", async () => {
    vi.useFakeTimers(); const h = harness();
    await expect(h.tool({ action: "create", prompt: "x", delay: "1s", interval: "2s" })).rejects.toThrow("exactly one");
    await expect(h.tool({ action: "create", prompt: "x", interval: "1ms" })).rejects.toThrow("at least");
    await h.tool({ action: "create", prompt: "x", delay: "1s" });
    await h.emit("session_shutdown"); await h.emit("session_start"); await vi.advanceTimersByTimeAsync(2000);
    expect(h.pi.sendMessage).not.toHaveBeenCalled();
  });
  it("loop runs immediately, repeats, and stop preserves independent schedules", async () => {
    vi.useFakeTimers(); const h = harness();
    await h.command("loop", "1s report"); expect(h.pi.sendMessage).toHaveBeenCalledTimes(1);
    await vi.advanceTimersByTimeAsync(1000); expect(h.pi.sendMessage).toHaveBeenCalledTimes(2);
    await h.tool({ action: "create", delay: "10s", prompt: "other" });
    await h.command("loop", "stop"); expect((await h.tool({ action: "list" })).details).toHaveLength(1);
    await h.emit("session_shutdown");
  });
  it("goal continues on NOT_MET, stops on MET, writes full transcript to a temp file and cleans it up", async () => {
    const seen: { path: string; content: string }[] = [];
    const verdicts = ["NOT_MET", "MET", "MET"];
    const evaluate = vi.fn(async (_c: string, path: string) => { seen.push({ path, content: readFileSync(path, "utf8") }); return verdicts[seen.length - 1]; });
    const h = harness(evaluate);
    await h.command("goal", "verify result");
    await h.emit("agent_end", { messages: [{ role: "toolResult", toolName: "Bash", content: "observed" }] });
    expect(h.pi.sendMessage).toHaveBeenCalledTimes(2); expect(seen[0].content).toContain("observed");
    expect(existsSync(seen[0].path)).toBe(false); // temp file removed after evaluation
    await h.emit("agent_end", { messages: [] }); expect(h.api.getGoal()?.status).toBe("met");
    // Large transcripts are written in full — no truncation, no size failure.
    await h.command("goal", "new goal");
    await h.emit("agent_end", { messages: [{ role: "toolResult", content: "x".repeat(200_000) }, { role: "toolResult", content: "recent evidence" }] });
    expect(h.api.getGoal()?.status).toBe("met");
    expect(seen[2].content).toContain("recent evidence");
    expect(seen[2].content.length).toBeGreaterThan(200_000);
    expect(seen[2].content.trimEnd().split("\n")).toHaveLength(2);
    expect(existsSync(seen[2].path)).toBe(false);
  });
  it("grep and read helpers are bounded and injection-safe", () => {
    const lines = Array.from({ length: 300 }, (_, i) => `line ${i + 1} ${i % 2 ? "even" : "odd"}`);
    expect(grepTranscript(lines, "line 42 ")).toBe("42: line 42 even");
    expect(grepTranscript(lines, "line", 999).split("\n")).toHaveLength(50); // hard cap
    expect(grepTranscript(lines, "(unclosed")).toMatch(/^invalid regex/);
    expect(grepTranscript(lines, "no such text")).toBe("no matches");
    expect(grepTranscript(["x".repeat(1000)], "x")).toContain("[clipped]");
    expect(readTranscriptLines(lines, 1, 300).split("\n")).toHaveLength(200); // 200-line cap
    expect(readTranscriptLines(lines, 299, 300)).toBe("299: line 299 odd\n300: line 300 even");
    expect(readTranscriptLines(lines, 999, 1000)).toMatch(/out of range/);
  });
  it("clear invalidates an in-flight evaluator", async () => {
    let resolve!: Function; let invoked!: Function; const called = new Promise(r => { invoked = r; });
    const h = harness(() => { invoked(); return new Promise(r => { resolve = r; }); });
    await h.command("goal", "work"); const pending = h.emit("agent_end", { messages: [] }); await called;
    await h.command("goal", "clear"); resolve("NOT_MET"); await pending;
    expect(h.pi.sendMessage).toHaveBeenCalledTimes(1); expect(h.api.getGoal()).toBeUndefined();
  });
});
