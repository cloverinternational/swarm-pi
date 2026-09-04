import { describe, expect, it, vi } from "vitest";
import { HookRuntimeCoordinator, normalizeHookEvent } from "../src/swarm-hook-runtime.js";

describe("Swarm hook runtime", () => {
  it("normalizes Pi aliases and outcome fields", () => {
    const e = normalizeHookEvent({ toolName: "Write", toolCallId: "c1", isError: true }, "tool_result");
    expect(e).toMatchObject({ type: "tool.after_execute", phase: "after", tool: "Write", toolCallId: "c1", failed: true });
  });
  it("orders priorities, filters scopes, and blocks deterministically", async () => {
    const seen: string[] = [], h = new HookRuntimeCoordinator();
    h.register({ name: "low", priority: 1, handle: () => { seen.push("low"); } });
    h.register({ name: "gate", priority: 99, scopes: ["project"], handle: () => { seen.push("gate"); return { action: "block", message: "denied" }; } });
    const result = await h.dispatch({ type: "tool.before_execute", scope: "project", toolName: "write" });
    expect(seen).toEqual(["gate"]); expect(result.blocked?.message).toBe("denied");
  });
  it("shares budgets and deduplicates terminal events", async () => {
    const h = new HookRuntimeCoordinator(), calls = vi.fn();
    h.register({ name: "once", budgetKey: "shared", maxInvocations: 1, handle: calls });
    await h.dispatch({ type: "tool.after_execute", toolCallId: "x", toolName: "bash" });
    await h.dispatch({ type: "tool.after_execute", toolCallId: "x", toolName: "bash" });
    await h.dispatch({ type: "tool.after_execute", toolCallId: "y", toolName: "bash" });
    expect(calls).toHaveBeenCalledTimes(1);
    expect(h.auditSnapshot().filter(a => a.outcome === "skipped")).toHaveLength(1);
  });
  it("times out and applies failure modes", async () => {
    const h = new HookRuntimeCoordinator({ defaultTimeoutMs: 5 });
    h.register({ name: "unsafe", failureMode: "block", handle: () => new Promise(() => {}) });
    const result = await h.dispatch({ type: "tool.before_execute" });
    expect(result.blocked?.outcome).toBe("blocked");
    expect(result.blocked?.message).toContain("timeout");
  });
  it("routes subagents and persists audit", async () => {
    const audits: any[] = [], routed = vi.fn(async (_e, c) => ({ ...c, route: "child" }));
    const h = new HookRuntimeCoordinator({ appendAudit: a => audits.push(a), routeSubagent: routed });
    const seen: unknown[] = [];
    h.register({ name: "observer", handle: (_e, c) => { seen.push(c.route); } });
    await h.dispatch({ type: "agent.started", subagent: true });
    expect(routed).toHaveBeenCalled(); expect(seen).toEqual(["child"]); expect(audits.at(-1).outcome).toBe("executed");
  });
});
