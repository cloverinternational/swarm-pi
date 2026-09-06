import { PERMISSIVE_PARAMETERS, overlaySwarmToolSchemas } from "../../.pi/lib/swarm-tool-surface.ts";
import { readFileSync } from "node:fs";
import { resolve } from "node:path";
import { describe, expect, it } from "vitest";
import { AgentManager, type Runner } from "../../agents/src/index.ts";
import { SwarmAgentTools } from "../../.pi/lib/swarm-agent-tools.ts";
import { registerSwarmAgentTools } from "../../.pi/extensions/swarm-agent-tools.ts";

const root = resolve(import.meta.dirname, "../..");
const names = ["BackgroundTask", "Subagent", "SubagentOutput", "TaskOutput", "Delegate", "DelegateOutput", "multi_agent_wait", "wait_for_agent"];
const fixtures = new Map(
  JSON.parse(readFileSync(resolve(root, "tools/parity/fixtures/swarm-tools.json"), "utf8"))
    .map((entry: any) => [entry.function.name, entry.function]),
);
const deferred = () => {
  let resolve!: (value: string) => void;
  const promise = new Promise<string>(r => { resolve = r; });
  return { promise, resolve };
};
const harness = (runner: Runner) => {
  const tools: any[] = [];
  const pi = { getCwd: () => root, registerTool: (tool: any) => tools.push(tool) };
  const logic = registerSwarmAgentTools(pi, { manager: new AgentManager({ cwd: root, runner }) });
  return { tools, logic };
};

describe("Swarm agent orchestration tools", () => {
  it("advertises fixture descriptions and schemas byte-for-byte", () => {
    const { tools } = harness(async () => "done");
    expect(tools.map(t => t.name)).toEqual(names);
    for (const tool of tools) {
      const fixture: any = fixtures.get(tool.name);
      expect(tool.description).toBe(fixture.description);
      expect(tool.parameters).toEqual(PERMISSIVE_PARAMETERS);
      expect(JSON.stringify(overlaySwarmToolSchemas({ tools: [{ type: "function", function: { name: tool.name, description: tool.description, parameters: tool.parameters } }] })!.tools[0].function.parameters)).toBe(JSON.stringify(fixture.parameters));
    }
    expect(tools.some(t => t.name === "Agent" || t.name === "AgentControl")).toBe(false);
  });

  it("matches representative Swarm validation strings", async () => {
    const logic = new SwarmAgentTools(new AgentManager({ runner: async () => "done" }));
    await expect(logic.backgroundTask({})).rejects.toThrow("task parameter is required");
    await expect(logic.subagent({ task: "x", agent_id: "a", preset: "b" })).rejects.toThrow(
      "Cannot specify both 'agent_id' and 'preset'. The 'preset' parameter is deprecated - use 'agent_id' instead.",
    );
    await expect(logic.subagent({ task: "x", run_in_background: true, auto_background_seconds: 1 })).rejects.toThrow(
      "Cannot specify both 'run_in_background' and 'auto_background_seconds'. Choose one background mode.",
    );
    await expect(logic.waitForAgent({})).rejects.toThrow("agent_id parameter is required");
    await expect(logic.multiWait({ agent_ids: [] })).rejects.toThrow("agent_ids array cannot be empty");
  });

  it("BackgroundTask returns immediately with agent_id and cache output path", async () => {
    const d = deferred();
    const logic = new SwarmAgentTools(new AgentManager({ runner: async () => d.promise }));
    const result = await logic.backgroundTask({ task: "slow" });
    const body = JSON.parse(result.text);
    expect(body.status).toBe("async_launched");
    expect(body.agent_id).toMatch(/^bg-\d{19}$/);
    expect(body.output_file).toMatch(/[/\\]\.cache[/\\]swarm[/\\]tasks[/\\]bg-\d+\.output$/);
    expect(Object.keys(body)).toEqual(["agent_id", "can_read_output", "description", "message", "output_file", "status"]);
    d.resolve("done");
  });

  it("TaskOutput and SubagentOutput support byte-offset incremental reads", async () => {
    const logic = new SwarmAgentTools(new AgentManager({ runner: async () => "alpha\nbeta" }));
    const launched = JSON.parse((await logic.backgroundTask({ task: "read" })).text);
    await logic.waitForAgent({ agent_id: launched.agent_id, timeout_seconds: 1 });
    const first = await logic.taskOutput({ agent_id: launched.agent_id, action: "result", offset: 0 });
    const offset = Number(first.text.match(/new_offset:\s+(\d+)/)?.[1]);
    expect(first.text).toContain("alpha\nbeta");
    expect(first.text).not.toContain("alpha\nbetaalpha\nbeta");
    const second = await logic.taskOutput({ agent_id: launched.agent_id, action: "result", offset });
    expect(second.text).toContain(`offset:      ${offset}`);
    expect(second.text).toContain("(no output at this offset)");
  });

  it("wait_for_agent times out without cancelling execution", async () => {
    const d = deferred();
    const logic = new SwarmAgentTools(new AgentManager({ runner: async () => d.promise }));
    const launched = JSON.parse((await logic.backgroundTask({ task: "slow" })).text);
    const result = JSON.parse((await logic.waitForAgent({ agent_id: launched.agent_id, timeout_seconds: 0.001 })).text);
    expect(result).toMatchObject({ wait_status: "timeout", timeout_seconds: 0.001 });
    expect(result.agent.status).toBe("running");
    d.resolve("done");
  });

  it("multi_agent_wait collects all terminal states", async () => {
    const logic = new SwarmAgentTools(new AgentManager({ runner: async ctx => `done:${ctx.task}` }));
    const a = JSON.parse((await logic.backgroundTask({ task: "a" })).text).agent_id;
    const b = JSON.parse((await logic.backgroundTask({ task: "b" })).text).agent_id;
    const result = JSON.parse((await logic.multiWait({ agent_ids: [a, b], timeout_seconds: 1 })).text);
    expect(result).toMatchObject({ wait_status: "completed", agent_count: 2, completed_count: 2, agents_cancelled: false });
    expect(result.agents.map((x: any) => x.status)).toEqual(["completed", "completed"]);
  });

  it("uses the tool call id, enriched task, and excludes synchronous Subagent runs from tracking", async () => {
    const seen: string[] = [];
    const { tools, logic } = harness(async ctx => { seen.push(ctx.task); return "SUBAGENT_OK"; });
    const subagent = tools.find(tool => tool.name === "Subagent");
    expect((await subagent.execute("call_bg", { task: "say hi", run_in_background: true })).content[0].text)
      .toContain('"agent_id": "subagent-call_bg"');
    await logic.waitForAgent({ agent_id: "subagent-call_bg", timeout_seconds: 1 });
    await subagent.execute("call_sync", { task: "say sync", agent_id: "general-assistant" });
    const listed = JSON.parse((await logic.taskOutput({})).text);
    expect(listed.total_agents).toBe(1);
    expect(listed.agents[0].task).toContain("[REPORTING DIRECTIVE]");
    expect(seen[0]).toContain(`[CONTEXT]\nWorking Directory: ${root}\nProject: Pi-Swarm\nProject Type: Node.js/JavaScript`);
    expect(seen[0]).toContain("[TASK]\nsay hi\n[/TASK]");
  });

  it("supports Delegate question and answer round trip", async () => {
    const d = deferred();
    const logic = new SwarmAgentTools(new AgentManager({ runner: async () => d.promise }));
    const launched = await logic.delegate({ task: "ambiguous" });
    const id = launched.text.match(/agent_id=([^\n]+)/)![1];
    const answer = logic.askParent(id, "Which branch?", "Need a target");
    const polled = await logic.delegateOutput({ agent_id: id, action: "poll" });
    const qid = polled.text.match(/question_id: ([^\n]+)/)![1];
    expect(polled.text).toContain("Which branch?");
    await logic.delegateOutput({ agent_id: id, action: "answer", answer: "main", question_id: qid });
    await expect(answer).resolves.toBe("main");
    d.resolve("done");
  });
});
