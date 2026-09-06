import { describe, expect, it } from "vitest";
import { mkdirSync, mkdtempSync, readFileSync, symlinkSync, writeFileSync } from "node:fs";
import { join } from "node:path";
import { tmpdir } from "node:os";
import {
  MAX_SUBMITTED_PLAN_BYTES, PlanModeController, enterPlanToolResult, exitPlanApprovedResult, exitPlanNoPlanError,
  exitPlanRejectedResult, planExitDetected, problemBreakdownPrompt, readSubmittedPlan, simulationReminderMessage, validatePlanContent,
} from "../../lib/context/swarm-plan-mode.ts";
import { registerPlanMode } from "../../extensions/10-context/swarm-plan-mode.ts";
import { createSwarmBuiltinPipeline } from "../../lib/runtime/swarm-builtin-hooks.ts";
import { resetReminderSequences } from "../../lib/policy/swarm-annoyance-nudge.ts";

class FakePi {
  tools: any[] = []; handlers = new Map<string, any>(); entries: any[] = [];
  constructor(public cwd: string) {}
  getCwd() { return this.cwd; }
  registerTool(tool: any) { this.tools.push(tool); }
  appendEntry(type: string, data: unknown) { this.entries.push({ type, data }); }
  on(event: string, handler: any) { this.handlers.set(event, handler); }
  registerCommand() {}
}

describe("plan mode (internal/plan + hooks/builtin plan_mode_first_tool.go/simulation.go)", () => {
  it("enter_plan_mode returns Swarm's CEREMONY text with real newlines and adds no system prompt", async () => {
    const root = mkdtempSync(join(tmpdir(), "pi-plan-"));
    const pi = new FakePi(root);
    const controller = registerPlanMode(pi);
    expect(pi.tools.map(t => t.name)).toEqual(["enter_plan_mode", "exit_plan_mode"]);
    expect(pi.handlers.has("before_agent_start")).toBe(false);
    expect(pi.handlers.has("tool_call")).toBe(false);
    const start = await pi.tools[0].execute("c1", {});
    expect(start.content[0].text).toBe(enterPlanToolResult(root));
    expect(start.content[0].text).toContain(`\n- Keep the plan file inside the active workspace: ${root}\n`);
    expect(start.content[0].text).not.toContain("\\n");
    expect(controller.isActive()).toBe(true);
    expect(pi.entries.some(e => e.type === "pi-swarm-plan-mode")).toBe(true);
  });

  it("runs the first-tool breakdown (pre, 96) and the simulation post-hook through the builtin pipeline", () => {
    resetReminderSequences();
    const controller = new PlanModeController();
    const p = createSwarmBuiltinPipeline({ session: "conv", tasks: () => [], planMode: {
      firstTool: name => controller.beforeTool(name).inject, planExitDetected, simulationMessage: simulationReminderMessage,
    } });
    p.onUserPrompt({ messageAfterReceive: false, prompt: "PARITY_CAPTURE plan-enter" });
    // enter_plan_mode: silent pre-hook, simulation post-hook (kind=context).
    expect(p.preTool({ toolName: "enter_plan_mode", params: {}, toolCallId: "e" })).toEqual({ context: "" });
    controller.enter();
    const post = p.postTool({ toolName: "enter_plan_mode", params: {}, toolCallId: "e", failed: false, output: "x" });
    expect(post.startsWith('<system-reminder source="simulation" kind="context" seq="1">[PRE-EXECUTION SIMULATION - CHOREOGRAPH YOUR DANCE]\n')).toBe(true);
    expect(post.endsWith("Skip ANY step = you are NOT ready. Go back and rehearse.</system-reminder>")).toBe(true);
    expect(p.flushTurn()).toBe(post);
    // First tool after entering: breakdown embedded in the result; reminderKind
    // reads "block" out of the prompt body ("Identify blockers…").
    p.onUserPrompt({ messageAfterReceive: false, prompt: "PARITY_CAPTURE plan-write" });
    const pre = p.preTool({ toolName: "Bash", params: { command: "echo wrote" }, toolCallId: "w" });
    expect(pre.context.startsWith('<system-reminder source="plan-mode-first-tool-hook" kind="block" seq="1">[PLAN MODE — PROBLEM BREAKDOWN REQUIRED]\n')).toBe(true);
    expect(pre.context).toContain(problemBreakdownPrompt);
    expect(p.preTool({ toolName: "Bash", params: { command: "echo again" }, toolCallId: "w2" })).toEqual({ context: "" });
    // …and the raw prompt is queued after the next prompt's task-nudge, unwrapped
    // because the joined block already starts with a canonical reminder.
    p.postTool({ toolName: "Bash", params: {}, toolCallId: "w", failed: false, output: "wrote" });
    p.postTool({ toolName: "Bash", params: {}, toolCallId: "w2", failed: false, output: "again" });
    const next = p.onUserPrompt({ messageAfterReceive: false, prompt: "PARITY_CAPTURE plan-edit" }).injected;
    expect(next.startsWith('<system-reminder source="task-nudge" kind="nudge" seq="1">')).toBe(true);
    expect(next).toContain("</system-reminder>\n[PLAN MODE — PROBLEM BREAKDOWN REQUIRED]\n");
  });

  it("exit_plan_mode mirrors tools.go: Go-shaped results, error wording, and canonical plan copy", async () => {
    const root = mkdtempSync(join(tmpdir(), "pi-plan-"));
    const home = mkdtempSync(join(tmpdir(), "pi-plan-home-"));
    const prev = process.env.SWARM_HOME; process.env.SWARM_HOME = home;
    try {
      const pi = new FakePi(root);
      const controller = registerPlanMode(pi);
      const exit = pi.tools[1];
      const text = async (params: any, ctx: any = {}) => { const r = await exit.execute("x", params, new AbortController().signal, undefined, ctx); return [r.content[0].text, r.isError === true] as const; };
      // registry Validate → Go error envelope (annoyance-nudge territory), not an IsError result.
      await expect(exit.execute("x", { plan: "a", plan_file: "plan.md" }, new AbortController().signal, undefined, {})).rejects.toThrow(/^Error executing exit_plan_mode: validation failed for exit_plan_mode: provide either plan or plan_file, not both \(error_id=err_[0-9a-f]{20}\)$/);
      expect(await text({ plan_file: "missing.md" })).toEqual([`exit_plan_mode: open plan file "missing.md" inside workspace "${root}": openat missing.md: no such file or directory`, true]);
      expect(await text({ plan_file: "../x.md" })).toEqual([`exit_plan_mode: plan file "../x.md" is outside workspace "${root}"`, true]);
      const [none, noneErr] = await text({});
      expect(noneErr).toBe(true);
      const canonical = none.match(/checked: (.*)\.$/)![1];
      expect(canonical.startsWith(join(home, "conversations") + "/") && /\/[0-9a-f-]{36}\/plan\.md$/.test(canonical)).toBe(true);
      expect(none).toBe(exitPlanNoPlanError(canonical));
      writeFileSync(join(root, "plan.md"), "# Plan\n\n1. step one\n");
      controller.enter();
      // Headless (no ctx.ui): auto-approve with the interactive message shape (broker present).
      const [approved] = await text({ plan_file: "plan.md" });
      expect(approved).toBe('{\n  "approved": true,\n  "clear_context": false,\n  "edited_plan": "# Plan\\n\\n1. step one",\n  "message": "Plan approved. You may now begin implementing. Follow the approved plan precisely."\n}');
      expect(controller.isActive()).toBe(false);
      const copies = readFileSync(canonical, "utf8");
      expect(copies).toBe("# Plan\n\n1. step one\n");
      // Rejection keeps plan mode active; blank feedback gets app_plan.go's default.
      controller.enter();
      const ui = { confirm: async () => false, input: async () => "" };
      const [rejected] = await text({ plan: "<b>&" }, { ui });
      expect(rejected).toBe('{\n  "approved": false,\n  "feedback": "Please revise the plan and try again.",\n  "message": "Plan rejected.\\n\\nFeedback: Please revise the plan and try again.\\n\\nRevise your plan and call exit_plan_mode again."\n}');
      expect(controller.isActive()).toBe(true);
      expect(exitPlanApprovedResult("<b>&", true)).toContain('"edited_plan": "\\u003cb\\u003e\\u0026"');
      expect(exitPlanRejectedResult("f")).toContain('"feedback": "f"');
    } finally { if (prev === undefined) delete process.env.SWARM_HOME; else process.env.SWARM_HOME = prev; }
  });

  it("ReadSubmittedPlanFile / ValidatePlanContent wording", () => {
    const root = mkdtempSync(join(tmpdir(), "pi-plan-"));
    writeFileSync(join(root, "PLAN.md"), "# Plan\n");
    mkdirSync(join(root, "dir"));
    symlinkSync("/etc/hostname", join(root, "esc.md"));
    expect(readSubmittedPlan({ workspace: root }, "PLAN.md")).toBe("# Plan");
    expect(readSubmittedPlan({ workspace: root }, join(root, "PLAN.md"))).toBe("# Plan");
    expect(() => readSubmittedPlan({ workspace: root }, " ")).toThrow("plan file path is empty");
    expect(() => readSubmittedPlan({ workspace: root }, "dir")).toThrow(`plan file "dir" is not a regular file`);
    expect(() => readSubmittedPlan({ workspace: root }, "esc.md")).toThrow(`open plan file "esc.md" inside workspace "${root}": openat esc.md: path escapes from parent`);
    writeFileSync(join(root, "huge.md"), "x".repeat(MAX_SUBMITTED_PLAN_BYTES + 1));
    expect(() => readSubmittedPlan({ workspace: root }, "huge.md")).toThrow(`plan file "huge.md" exceeds the 1048576-byte limit`);
    writeFileSync(join(root, "empty.md"), " \n");
    expect(() => readSubmittedPlan({ workspace: root }, "empty.md")).toThrow(`plan file "empty.md" is empty`);
    expect(() => validatePlanContent("a\0b", "edited plan")).toThrow("edited plan is not valid UTF-8 text");
    expect(planExitDetected("enter_plan_mode") && planExitDetected("exit_plan_mode") && !planExitDetected("Bash")).toBe(true);
  });
});
