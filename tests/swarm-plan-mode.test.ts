import { describe, expect, it } from "vitest";
import { mkdirSync, mkdtempSync, writeFileSync } from "node:fs";
import { join } from "node:path";
import { tmpdir } from "node:os";
import { registerPlanMode } from "./swarm-plan-mode.ts";

class FakePi {
  cwd: string;
  tools: any[] = [];
  handlers = new Map<string, (event: any, ctx: any) => any>();
  entries: any[] = [];
  constructor(cwd: string) { this.cwd = cwd; }
  getCwd() { return this.cwd; }
  registerTool(tool: any) { this.tools.push(tool); }
  appendEntry(type: string, data: unknown) { this.entries.push({ type, data }); }
  on(event: string, handler: any) { this.handlers.set(event, handler); }
  registerCommand() { /* not needed by this fixture */ }
}

describe("Pi Plan Mode adapter", () => {
  it("registers tools and injects prompt/breakdown exactly once", async () => {
    const root = mkdtempSync(join(tmpdir(), "pi-plan-adapter-"));
    const pi = new FakePi(root);
    const controller = registerPlanMode(pi);
    expect(pi.tools.map(t => t.name)).toEqual(["enter_plan_mode", "exit_plan_mode"]);

    const enter = pi.tools.find(t => t.name === "enter_plan_mode");
    const start = await enter.execute("call-enter", {});
    expect(start.details.state).toBe("active");
    expect(pi.entries.some(e => e.type === "pi-swarm-plan-mode")).toBe(true);

    const before = pi.handlers.get("before_agent_start")!;
    const prompt = await before({ systemPrompt: "BASE" }, {});
    expect(prompt.systemPrompt).toContain("## Plan Mode - ACTIVE");
    const promptAgain = await before({ systemPrompt: prompt.systemPrompt }, {});
    expect((promptAgain.systemPrompt.match(/## Plan Mode - ACTIVE/g) ?? []).length).toBe(1);

    const toolCall = pi.handlers.get("tool_call")!;
    const first = await toolCall({ toolName: "Read", toolCallId: "c1" }, {});
    expect(first.message.content).toContain("PROBLEM BREAKDOWN REQUIRED");
    expect(await toolCall({ toolName: "Grep", toolCallId: "c2" }, {})).toBeUndefined();
    expect(controller.snapshotOf().firstToolUsed).toBe(true);
  });

  it("routes plan-file approval and rejection without leaving plan mode on rejection", async () => {
    const root = mkdtempSync(join(tmpdir(), "pi-plan-approval-"));
    writeFileSync(join(root, "PLAN.md"), "# Approved plan\n\n- Step one\n");
    const pi = new FakePi(root);
    const controller = registerPlanMode(pi);
    await pi.tools.find(t => t.name === "enter_plan_mode").execute("id", {});
    const exit = pi.tools.find(t => t.name === "exit_plan_mode");
    const rejected = await exit.execute("id", { plan_file: "PLAN.md" }, undefined, undefined, { ui: { confirm: async () => false, input: async () => "Needs a safer migration." } });
    expect(rejected.details.state).toBe("active");
    expect(JSON.parse(rejected.content[0].text).approved).toBe(false);

    const approved = await exit.execute("id", { plan_file: "PLAN.md" }, undefined, undefined, { ui: { confirm: async (_title: string, message: string) => message.includes("Approve"), input: async () => "" } });
    expect(approved.details.state).toBe("idle");
    expect(JSON.parse(approved.content[0].text).approved).toBe(true);
    expect(controller.snapshotOf().planIdHistory).toHaveLength(1);
  });

  it("hydrates the latest persisted snapshot on session_start", async () => {
    const root = mkdtempSync(join(tmpdir(), "pi-plan-hydrate-"));
    const pi = new FakePi(root);
    const first = registerPlanMode(pi);
    await pi.tools.find(t => t.name === "enter_plan_mode").execute("id", {});
    const saved = pi.entries.at(-1).data;
    const resumed = new FakePi(root);
    registerPlanMode(resumed);
    await resumed.handlers.get("session_start")!({}, { sessionManager: { getEntries: () => [{ type: "pi-swarm-plan-mode", data: saved }] } });
    expect(resumed.tools).toHaveLength(2);
    expect((await resumed.handlers.get("tool_call")!({ toolName: "Read", toolCallId: "r1" }, {}))).toBeDefined();
    expect(first.snapshotOf().planId).toBe(saved.planId);
  });
});
