import { describe, expect, it } from "vitest";
import { mkdtempSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { AgentManager, BUILTIN_AGENT_PROFILES, createPiRunner, registerAgents } from "../src/index.js";

describe("AgentManager", () => {
  it("loads the Swarm TUI general and code-reviewer profiles by default", () => {
    const manager = new AgentManager();
    expect(manager.profile("general-assistant")).toMatchObject({
      name: "general-assistant",
      systemPrompt: expect.stringContaining("helpful AI assistant"),
      tools: ["*"],
    });
    expect(manager.profile("code-reviewer")).toMatchObject({
      name: "code-reviewer",
      systemPrompt: expect.stringContaining("expert code reviewer"),
      capabilities: expect.arrayContaining(["read-only", "security analysis"]),
      tools: ["repository_inspect"],
    });
    expect(BUILTIN_AGENT_PROFILES).toHaveLength(2);
  });

  it("runs a real Pi child with isolated control tools and inherited execution context", async () => {
    let seen: any;
    const cwd = mkdtempSync(join(tmpdir(), "pi-agent-runner-"));
    const runner = createPiRunner({ exec: async (...args: any[]) => { seen = args; return { code: 0, stdout: "child result", stderr: "", killed: false }; } });
    // No child session file was written by the fake exec → turns floors at 1.
    expect(await runner({ signal: new AbortController().signal, spec: { id: "x", task: "inspect" }, task: "inspect", cwd, instructions: [], steering: [] })).toEqual({ output: "child result", turns: 1 });
    expect(seen[0]).toBe(process.platform === "win32" ? "cmd.exe" : "env");
    // A child that recorded three assistant messages reports three turns.
    const sessionPath = join(cwd, ".pi", "agent-sessions", "s.jsonl");
    const counting = createPiRunner({ exec: async () => { const { mkdirSync, writeFileSync } = await import("node:fs"); mkdirSync(join(cwd, ".pi", "agent-sessions"), { recursive: true }); writeFileSync(sessionPath, ["{\"type\":\"session\"}", "{\"type\":\"message\",\"message\":{\"role\":\"user\"}}", "{\"type\":\"message\",\"message\":{\"role\":\"assistant\"}}", "{\"type\":\"message\",\"message\":{\"role\":\"toolResult\"}}", "{\"type\":\"message\",\"message\":{\"role\":\"assistant\"}}", "{\"type\":\"message\",\"message\":{\"role\":\"assistant\"}}"].join("\n") + "\n"); return { code: 0, stdout: "done", stderr: "", killed: false }; } });
    expect(await counting({ signal: new AbortController().signal, spec: { id: "x", sessionId: "s", task: "t" }, task: "t", cwd, instructions: [], steering: [] })).toEqual({ output: "done", turns: 3 });
    if (process.platform === "win32") expect(seen[1][3]).toContain("PI_SWARM_SUBAGENT=1");
    // --approve: the child inherits the parent's workspace trust so the port's
    // project extensions load in print mode (Swarm sub-agents share the registry).
    else expect(seen[1]).toEqual(expect.arrayContaining(["PI_SWARM_SUBAGENT=1", "pi", "--mode", "text", "--print", "--approve", "--session", `${cwd}/.pi/agent-sessions/x.jsonl`, "--exclude-tools", "Agent,AgentControl", "-p", "inspect"]));
    expect(seen[2]).toMatchObject({ cwd });
  });

  it("inherits profile capabilities and preserves parent identity", async () => {
    let seen: any;
    const manager = new AgentManager({ profiles: [{ name: "review", systemPrompt: "be precise", capabilities: ["read"] }], runner: async ctx => { seen = ctx; return "done"; } });
    const parent = manager.spawn({ id: "parent", task: "parent", profile: "review", provider: "openai", model: "gpt-test" });
    await parent.wait();
    const child = manager.spawn({ id: "child", task: "child", parentId: parent.id });
    expect(await child.wait()).toMatchObject({ id: "child", status: "completed", output: "done" });
    expect(seen.spec.parentId).toBe("parent");
    expect(seen.provider).toBe("openai");
    expect(seen.model).toBe("gpt-test");
    expect(seen.instructions).toContain("be precise");
    expect(seen.instructions).toContain("Capability: read");
  });
  it("supports cancellation and bounded concurrency", async () => {
    let running = 0, maximum = 0;
    const manager = new AgentManager({ concurrency: 1, runner: async ({ signal }) => { running++; maximum = Math.max(maximum, running); await new Promise<void>(resolve => { const timer = setTimeout(resolve, 30); signal.addEventListener("abort", () => { clearTimeout(timer); resolve(); }, { once: true }); }); running--; return "ok"; } });
    const first = manager.spawn({ id: "a", task: "a" });
    const second = manager.spawn({ id: "b", task: "b" });
    first.cancel();
    expect((await first.wait()).status).toBe("cancelled");
    expect((await second.wait()).status).toBe("completed");
    expect(maximum).toBe(1);
  });
  it("notifies an injected parent event sink when background work completes", async () => {
    const events: any[] = [];
    const manager = new AgentManager({ runner: async () => "done", eventSink: event => { events.push(event); } });
    const handle = manager.spawn({ id: "child", parentId: "parent-session", parentSessionId: "parent-session", sessionId: "session-1", task: "work", background: true });
    await handle.wait();
    expect(events).toHaveLength(1);
    expect(events[0]).toMatchObject({ type: "agent.completed", agentId: "child", parentId: "parent-session", parentSessionId: "parent-session", sessionId: "session-1", background: true, result: { status: "completed", output: "done" } });
  });
  it("wires background completion to the exact parent Pi session once", async () => {
    const tools: any[] = [], messages: any[] = [];
    const pi = { sessionId: "parent-session", registerTool: (tool: any) => tools.push(tool), sendUserMessage: (content: string, options: any) => messages.push({ content, options }) };
    const manager = new AgentManager({ runner: async () => "finished" });
    registerAgents(pi, manager);
    const agent = tools.find(tool => tool.name === "Agent");
    const result = await agent.execute("call", { task: "work", background: true }, { sessionId: "parent-session" });
    await manager.control(result.details.handle, "wait");
    expect(messages).toHaveLength(1);
    expect(messages[0]).toEqual({ content: expect.stringContaining("id=agent"), options: { deliverAs: "followUp" } });
    expect(messages[0].content).toContain("status=completed");
    expect(messages[0].content).toContain("output: finished");
    await manager.control(result.details.handle, "wait");
    expect(messages).toHaveLength(1);

    const otherMessages: any[] = [];
    const otherPi = { sessionId: "other-session", registerTool: () => undefined, sendUserMessage: (content: string) => otherMessages.push(content) };
    registerAgents(otherPi, manager);
    expect(otherMessages).toHaveLength(0);
  });
  it("does not emit completion for foreground agents", async () => {
    const messages: any[] = [], pi = { sessionId: "parent", registerTool: (tool: any) => messages.push(tool), sendUserMessage: () => { throw new Error("must not notify"); } };
    const manager = new AgentManager({ runner: async () => "done" });
    registerAgents(pi, manager);
    const agent = messages.find(tool => tool.name === "Agent");
    await agent.execute("call", { task: "work" }, { sessionId: "parent" });
  });
  it("steers a running agent and emits completion", async () => {
    const manager = new AgentManager({ runner: async ({ signal, spec }) => { await new Promise(r => setTimeout(r, 5)); if (signal.aborted) throw new Error("cancelled"); return spec.task; } });
    const handle = manager.spawn({ id: "x", task: "work" });
    expect(handle.steer("focus")).toBe(true);
    let completed = false; manager.onComplete("x", () => { completed = true; });
    expect((await handle.wait()).output).toBe("work"); expect(completed).toBe(true);
  });
});
