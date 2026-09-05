import { describe, expect, it } from "vitest";
import { registerGoalLoop } from "../src/goal-loop.js";
import type { GoalLoopControlPlane } from "../src/goal-loop.js";

describe("goal/loop adapters", () => {
  it("registers typed tools and keeps create queued at the authoritative boundary", async () => {
    const calls: string[] = []; const tools: any[] = []; const commands: any[] = [];
    const control: GoalLoopControlPlane = {
      goalCreate: async input => { calls.push(`goal:${input.doneWhenReviewed}`); return { id: "goal:1" as any, ...input, state: "queued", createdAt: "", updatedAt: "" } as any; },
      goalStatus: async id => ({ id, description: "x", doneWhen: "x", doneWhenReviewed: true, state: "queued", createdAt: "", updatedAt: "" }), goalPause: async id => (await control.goalStatus(id)), goalResume: async id => (await control.goalStatus(id)), goalComplete: async id => (await control.goalStatus(id)),
      loopCreate: async input => ({ id: "loop:1" as any, ...input, state: "queued", createdAt: "", updatedAt: "" } as any), loopStatus: async id => ({ id, prompt: "x", cadence: "5m", state: "queued", createdAt: "", updatedAt: "" }), loopPause: async id => (await control.loopStatus(id)), loopResume: async id => (await control.loopStatus(id)), loopStop: async id => (await control.loopStatus(id)),
    };
    registerGoalLoop({ registerTool: t => tools.push(t), registerCommand: (name, options) => commands.push({ name, options }) }, control);
    expect(tools).toHaveLength(10); expect(commands.map(x => x.name)).toEqual(["goal", "loop"]);
    const create = tools.find(t => t.name === "goal_create"); expect(create.parameters.required).toContain("doneWhenReviewed");
    await create.execute("1", { description: "d", doneWhen: "tests pass", doneWhenReviewed: true }); expect(calls).toEqual(["goal:true"]);
    const bad = await commands[0].options.handler("resume"); expect(JSON.parse(bad.content[0].text).error).toContain("operation is required");
  });
});
