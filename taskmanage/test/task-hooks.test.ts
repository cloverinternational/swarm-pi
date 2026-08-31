import { describe, expect, it } from "vitest";
import { TaskHooksCoordinator } from "../src/task-hooks.js";
import { TaskManager, type JournalEntry } from "../src/task-manage.js";

const pi = (entries: JournalEntry[] = []) => ({
  entries,
  handlers: new Map<string, any[]>(),
  on(name: string, fn: any) { this.handlers.set(name, [...(this.handlers.get(name) ?? []), fn]); },
  appendEntry(type: string, data: any) { this.entries.push({ type, data } as any); },
});
const event = (type: string, more: any = {}) => ({ type, ...more });

describe("TaskManage hooks coordinator", () => {
  it("orders the gate, allows exemptions, and bypasses subagents", () => {
    const p = pi(), m = new TaskManager(), h = new TaskHooksCoordinator(m, p, { enforcementMode: "block" });
    expect(h.on(event("tool_call", { toolName: "write", input: {} }), {})).toMatchObject({ block: true });
    expect(h.on(event("tool_call", { toolName: "read", input: {} }), {})).toBeUndefined();
    expect(h.on(event("tool_call", { toolName: "write", input: {} }), { isSubagent: true })).toBeUndefined();
    m.execute({ operations: [{ key: "a", op: "create", subject: "work", status: "in_progress" }] });
    expect(h.on(event("tool_call", { toolName: "write", input: {} }), {})).toBeUndefined();
  });
  it("guides task lifecycle and records redacted outcomes", () => {
    const p = pi(), m = new TaskManager(), h = new TaskHooksCoordinator(m, p);
    m.execute({ operations: [{ key: "a", op: "create", subject: "work" }] });
    expect(h.on(event("tool_result", { toolName: "TaskManage", input: { operations: [{ key: "a", op: "create", subject: "work" }] }, result: { results: [] } }))).toBeDefined();
    m.execute({ operations: [{ key: "focus", op: "update", taskId: "1", status: "in_progress" }] });
    h.on(event("tool_result", { toolName: "bash", input: { command: "curl -H 'token=abc' https://x" }, result: {} }));
    expect(h.auditSnapshot()[0].summary).toContain("[REDACTED]");
    expect(h.auditSnapshot()[0].outcome).toBe("success");
    h.on(event("tool_result", { toolName: "bash", input: {}, error: "nope" }));
    expect((h.on(event("tool_result", { toolName: "bash", input: {}, result: {} }))?.message)).toContain("resolved");
  });
  it("uses a cadence and budget for empty-task nudges, then rehydrates it", () => {
    const entries: JournalEntry[] = [], p = pi(entries), m = new TaskManager(), h = new TaskHooksCoordinator(m, p, { nudgeInterval: 2, nudgeToolThreshold: 1 });
    h.on(event("turn_start")); h.on(event("tool_result", { toolName: "bash", input: {}, result: {} })); h.on(event("turn_end"));
    h.on(event("turn_start")); expect(h.on(event("turn_end"))?.message).toContain("no tasks");
    h.on(event("turn_start")); expect(h.on(event("turn_end"))).toBeUndefined();
    const restored = new TaskHooksCoordinator(m, p, { nudgeInterval: 2, nudgeToolThreshold: 1 });
    restored.on(event("session_start"), { sessionManager: { getEntries: () => entries } });
    expect(restored.auditSnapshot()).toEqual(h.auditSnapshot());
  });
});
