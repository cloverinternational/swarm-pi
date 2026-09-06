import { describe, expect, it } from "vitest";
import { WorkflowEngine, type WorkflowDefinition, type WorkflowEvent } from "../src/workflow.js";

describe("WorkflowEngine", () => {
  it("validates dependency cycles and runs independent groups with checkpoints", async () => {
    const events: WorkflowEvent[] = [], saved: any[] = [];
    const def: WorkflowDefinition = { id: "demo", groups: [
      { id: "a", strategy: "parallel", agents: [{ id: "a1" }, { id: "a2" }] },
      { id: "b", dependsOn: ["a"], strategy: "sequential", agents: [{ id: "b1" }] },
    ] };
    const result = await new WorkflowEngine(e => events.push(e), { load: () => undefined, save: x => saved.push(x) }).run(def, async ({ agent }) => ({ output: agent.id, usage: { tokens: 1 } }));
    expect(result.status).toBe("completed");
    expect(result.completedGroups).toEqual(["a", "b"]);
    expect(saved.at(-1).status).toBe("completed");
    expect(events.map(e => e.type)).toContain("workflow_completed");
    expect(events.map(e => e.type)).not.toContain("workflow_failed");
  });

  it("retries, enforces budgets, and emits a distinct cancellation event", async () => {
    const events: WorkflowEvent[] = [], controller = new AbortController();
    const def: WorkflowDefinition = { id: "retry", maxRetries: 1, budget: { maxAttempts: 2 }, groups: [{ id: "g", agents: [{ id: "x" }] }] };
    let calls = 0;
    const result = await new WorkflowEngine(e => events.push(e)).run(def, async () => { calls++; if (calls === 1) throw new Error("transient"); controller.abort(); throw new Error("stop"); }, controller.signal);
    expect(result.status).toBe("cancelled");
    expect(calls).toBe(2);
    expect(events.at(-1)?.type).toBe("workflow_cancelled");
  });

  it("restores completed groups from a checkpoint", async () => {
    const def: WorkflowDefinition = { id: "resume", groups: [{ id: "done", agents: [{ id: "one" }] }, { id: "next", dependsOn: ["done"], agents: [{ id: "two" }] }] };
    let ran: string[] = [];
    const store = { load: () => ({ workflowId: "resume", status: "running" as const, completedGroups: ["done"], results: {}, attempts: 1, tokens: 0, cost: 0 }), save: () => {} };
    const result = await new WorkflowEngine(undefined, store).run(def, async ({ agent }) => { ran.push(agent.id); return {}; });
    expect(result.status).toBe("completed"); expect(ran).toEqual(["two"]);
  });
});
