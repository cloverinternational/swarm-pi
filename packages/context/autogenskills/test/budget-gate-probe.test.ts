import { describe, expect, it } from "vitest";
import { AutoSkillManager, registerAutoSkills } from "../src/index.js";
import { mkdtempSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";

describe("over-budget tool_call probe", () => {
  it("returns a Pi-valid block at the exact next tool attempt", () => {
    const manager = new AutoSkillManager({ mode: "auto", dir: mkdtempSync(join(tmpdir(), "autogen-gate-")), toolCallBudget: 5, workingBudget: 90 });
    manager.observeTool(true, "Skill", { skill: "existing-skill" });
    for (let i = 0; i < 90; i++) manager.observeToolAttempt("bash", { command: `printf ${i}` }, `probe-${i}`);
    expect(manager.budgetStatus()).toMatchObject({ skilled: true, used: 90, budget: 90 });
    const decision = manager.gateTool("bash", { command: "printf MUST_BLOCK" });
    expect(decision).toEqual({ block: true, reason: expect.stringContaining("working budget") });
    expect(manager.budgetStatus().used).toBe(90);
  });

  it("blocks through the actual registerAutoSkills tool_call adapter", async () => {
    const handlers = new Map<string, any[]>();
    const pi = {
      appendEntry() {},
      on(event: string, handler: any) { handlers.set(event, [...(handlers.get(event) ?? []), handler]); },
      registerTool() {},
    };
    const manager = registerAutoSkills(pi, { mode: "auto", dir: mkdtempSync(join(tmpdir(), "autogen-adapter-")), workingBudget: 3 });
    manager.execute({ action: "create", name: "probe-skill", description: "probe", instructions: "A reusable production procedure. ".repeat(8) });
    manager.invokeSkill("probe-skill");
    const toolCall = handlers.get("tool_call")![0];
    for (let i = 0; i < 3; i++) expect(await toolCall({ toolName: "bash", toolCallId: `adapter-${i}`, input: { command: `printf ${i}` } }, {})).toBeUndefined();
    const blocked = await toolCall({ toolName: "bash", toolCallId: "adapter-blocked", input: { command: "printf SHOULD_NOT_RUN" } }, {});
    expect(blocked).toMatchObject({ block: true, reason: expect.stringContaining("working budget") });
    expect(manager.budgetStatus().used).toBe(3);
  });
});
