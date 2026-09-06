import { describe, expect, it, vi } from "vitest";
import { registerControlTaskTools } from "../../.pi/extensions/control-task-tools.ts";

describe("Pi control-task tools", () => {
  it("leaves goal_create ownership to the reviewed goal-loop adapter", () => {
    const tools: Array<{ name: string }> = [];
    const control = new Proxy({}, { get: () => vi.fn() });

    registerControlTaskTools(
      { registerTool: tool => tools.push(tool as { name: string }) },
      control as any,
    );

    expect(tools.map(tool => tool.name)).not.toContain("goal_create");
    expect(tools.map(tool => tool.name)).toEqual([
      "goal_get",
      "task_create",
      "task_get",
      "task_status",
      "task_cancel",
      "run_create",
      "run_get",
      "run_status",
      "run_cancel",
    ]);
  });
});
