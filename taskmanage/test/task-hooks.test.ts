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
  it("only bypasses genuinely simple read-only bash commands", () => {
    const h = new TaskHooksCoordinator(new TaskManager(), pi(), { enforcementMode: "block" });
    for (const command of [
      "pwd", "ls -la", "find src -type f", "grep -n TODO file", "rg --hidden pattern .",
      "git status --short", "git log --oneline", "git diff --stat", "git show HEAD:file",
      "cat file", "head -n 5 file", "tail -f log", "sed -n 1,5p file", "awk '{print $1}' file",
      "wc -l file", "which node", "type npm", "echo hello", "printf '%s' hello",
    ]) expect(h.on(event("tool_call", { toolName: "bash", input: { command } }))).toBeUndefined();
    for (const command of [
      "echo x > file", "pwd && rm -rf .", "pwd; rm file", "echo $(rm file)", "pwd | tee file", "echo `rm file`", "(pwd)",
      "sed -i s/a/b/ file", "sed --in-place file", "awk -i inplace '{print}' file",
      "find . -delete", "find . -exec rm {} \\;", "find . -execdir touch {} \\;",
      "find . -ok rm {} \\;", "find . -fprint output", "git branch -d old", "git branch -D old",
      "git branch -m old new", "git diff --output=out", "git diff --no-index a b",
    ]) {
      expect(h.on(event("tool_call", { toolName: "bash", input: { command } }))).toMatchObject({ block: true });
    }
  });
  it("guides task lifecycle and records redacted outcomes", () => {
    const p = pi(), m = new TaskManager(), h = new TaskHooksCoordinator(m, p);
    const createInput = { operations: [{ key: "a", op: "create", subject: "work" }] };
    const createResult = m.execute(createInput);
    expect(h.on(event("tool_result", { toolName: "TaskManage", input: createInput, result: createResult }))).toBeDefined();
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
    restored.on(event("turn_start"));
    expect(restored.on(event("turn_end"))).toBeUndefined();
  });

  it("deduplicates Pi terminal events and rejects failed or partial batches", () => {
    const p = pi(), m = new TaskManager(), h = new TaskHooksCoordinator(m, p);
    const input = { operations: [{ key: "a", op: "create", subject: "work", status: "in_progress" }] };
    const result = m.execute(input);
    const end = event("tool_execution_end", { toolCallId: "call-1", toolName: "TaskManage", input, result, isError: false });
    expect(h.on(event("tool_result", { ...end, content: [{ type: "text", text: JSON.stringify(result) }] }))).toBeDefined();
    expect(h.on(end)).toBeUndefined();
    expect(h.auditSnapshot()).toHaveLength(0);
    expect(h.on(event("tool_result", { toolCallId: "call-2", toolName: "TaskManage", input, content: [{ type: "text", text: JSON.stringify({ status: "partial", results: [{ key: "a", status: "succeeded" }] }) }] }))).toBeUndefined();
  });

  it("redacts secrets embedded in headers, paths, URLs, subjects, and commands", () => {
    const p = pi(), m = new TaskManager(), h = new TaskHooksCoordinator(m, p);
    m.execute({ operations: [{ key: "f", op: "create", subject: "work", status: "in_progress" }] });
    h.on(event("tool_result", { toolCallId: "x", toolName: "bash", input: {
      command: "curl --private_key=abc https://host/x?access_token=def", path: "/tmp/private_key=ghi",
      subject: "token=jkl", headers: { Authorization: "Bearer mno" }
    }, result: {} }));
    expect(h.auditSnapshot()[0].summary).not.toMatch(/abc|def|ghi|jkl|mno/);
  });

  it("redacts camel-case and embedded access key/token command forms", () => {
    const p = pi(), m = new TaskManager(), h = new TaskHooksCoordinator(m, p);
    m.execute({ operations: [{ key: "f", op: "create", subject: "work", status: "in_progress" }] });
    h.on(event("tool_result", { toolName: "bash", input: {
      command: `node -e "const accessToken='camel-secret'; const access_key=\"snake-secret\"; run --access-key kebab-secret --accessToken flag-secret"`,
    }, result: {} }));
    expect(h.auditSnapshot()[0].summary).not.toMatch(/camel-secret|snake-secret|kebab-secret|flag-secret/);
  });

  it("audits failed and partial TaskManage batches as failures from Pi-shaped results", () => {
    const p = pi(), m = new TaskManager(), h = new TaskHooksCoordinator(m, p);
    const input = { operations: [{ key: "a", op: "list" as const }] };
    for (const [toolCallId, status] of [["failed-1", "failed"], ["partial-1", "partial"]] as const) {
      h.on(event("tool_result", { toolCallId, toolName: "TaskManage", input,
        content: [{ type: "text", text: JSON.stringify({ status, results: [{ key: "a", status: status === "partial" ? "succeeded" : "failed" }] }) }] }));
    }
    expect(h.auditSnapshot().map(a => a.outcome)).toEqual(["failure", "failure"]);
  });

  it("exempts classifier aliases and resets maintenance when focus changes", () => {
    const p = pi(), m = new TaskManager(), h = new TaskHooksCoordinator(m, p, { enforcementMode: "block", maintenanceToolThreshold: 1 });
    expect(h.on(event("tool_call", { toolName: "readFile", input: {} }))).toBeUndefined();
    expect(h.on(event("tool_call", { toolName: "web_search", input: {} }))).toBeUndefined();
    for (const toolName of ["lsp", "lspSymbols", "recall", "skill", "skillManage", "skillReview", "patchSkill"])
      expect(h.on(event("tool_call", { toolName, input: {} }))).toBeUndefined();
    for (const toolName of ["lspwrite", "lspapply", "untrustedrecall"])
      expect(h.on(event("tool_call", { toolName, input: {} }))).toMatchObject({ block: true });
    m.execute({ operations: [{ key: "a", op: "create", subject: "a", status: "in_progress" }, { key: "b", op: "create", subject: "b" }] });
    h.on(event("tool_result", { toolName: "bash", input: {}, result: {} }));
    expect(h.on(event("turn_end"))).toBeUndefined(); // focus change establishes the baseline
    h.on(event("tool_result", { toolName: "bash", input: {}, result: {} }));
    expect(h.on(event("turn_end"))?.message).toContain("a");
    m.execute({ operations: [{ key: "f", op: "update", taskId: "2", status: "in_progress" }] });
    expect(h.on(event("turn_end"))).toBeUndefined();
  });

  it("keeps a separate maintenance baseline when switching away and back", () => {
    const p = pi(), m = new TaskManager(), h = new TaskHooksCoordinator(m, p, { maintenanceToolThreshold: 2 });
    m.execute({ operations: [{ key: "a", op: "create", subject: "a", status: "in_progress" }, { key: "b", op: "create", subject: "b" }] });
    h.on(event("tool_result", { toolName: "bash", input: {}, result: {} }));
    h.on(event("turn_end")); // establish task a's baseline at one tool
    h.on(event("tool_result", { toolName: "bash", input: {}, result: {} }));
    expect(h.on(event("turn_end"))).toBeUndefined(); // threshold is two tools after baseline
    h.on(event("tool_result", { toolName: "bash", input: {}, result: {} }));
    expect(h.on(event("turn_end"))?.message).toContain("a");
    m.execute({ operations: [{ key: "b", op: "update", taskId: "2", status: "in_progress" }] });
    expect(h.on(event("turn_end"))).toBeUndefined();
    m.execute({ operations: [{ key: "a", op: "update", taskId: "1", status: "in_progress" }] });
    expect(h.on(event("turn_end"))).toBeUndefined(); // a resumes its own baseline
    h.on(event("tool_result", { toolName: "bash", input: {}, result: {} }));
    expect(h.on(event("turn_end"))).toBeUndefined();
    h.on(event("tool_result", { toolName: "bash", input: {}, result: {} }));
    expect(h.on(event("turn_end"))?.message).toContain("a");
  });

  it("advances skill review bookkeeping only on success and nudges once overdue", () => {
    const p = pi(), m = new TaskManager(), h = new TaskHooksCoordinator(m, p);
    h.on(event("tool_result", { toolName: "skill", input: { name: "failed" }, error: "nope" }));
    for (let i = 0; i < 10; i++) h.on(event("tool_result", { toolName: "bash", input: {}, result: {} }));
    expect(h.on(event("turn_end"))).toBeUndefined(); // no successful skill usage yet
    h.on(event("tool_result", { toolName: "skill", input: { name: "useful" }, result: {} }));
    for (let i = 0; i < 9; i++) h.on(event("tool_result", { toolName: "bash", input: {}, result: {} }));
    expect(h.on(event("turn_end"))).toBeUndefined();
    h.on(event("tool_result", { toolName: "bash", input: {}, result: {} }));
    expect(h.on(event("turn_end"))?.message).toContain("Review reusable learning");
    expect(h.on(event("turn_end"))).toBeUndefined(); // bounded until another interval elapses
  });

  it("counts successful review and patch tools as completed skill reviews", () => {
    const h = new TaskHooksCoordinator(new TaskManager(), pi());
    h.on(event("tool_result", { toolName: "skill", input: {}, result: {} }));
    for (let i = 0; i < 9; i++) h.on(event("tool_result", { toolName: "bash", input: {}, result: {} }));
    h.on(event("tool_result", { toolName: "skillReview", input: {}, result: {} }));
    h.on(event("tool_result", { toolName: "patchSkill", input: {}, result: {} }));
    for (let i = 0; i < 9; i++) h.on(event("tool_result", { toolName: "bash", input: {}, result: {} }));
    expect(h.on(event("turn_end"))).toBeUndefined();
  });

  it("shares the empty-task nudge budget across coordinators in one session", () => {
    const p = pi(), m = new TaskManager(), h1 = new TaskHooksCoordinator(m, p, { nudgeInterval: 1, nudgeToolThreshold: 1 });
    const h2 = new TaskHooksCoordinator(m, p, { nudgeInterval: 1, nudgeToolThreshold: 1 });
    h1.on(event("turn_start")); h1.on(event("tool_result", { toolName: "bash", input: {}, result: {} }));
    expect(h1.on(event("turn_end"))).toBeUndefined(); // first turn is never nudged
    h1.on(event("turn_start")); expect(h1.on(event("turn_end"))?.message).toContain("no tasks");
    h2.on(event("turn_start")); h2.on(event("turn_start")); h2.on(event("tool_result", { toolName: "bash", input: {}, result: {} }));
    expect(h2.on(event("turn_end"))).toBeUndefined();
  });
});
