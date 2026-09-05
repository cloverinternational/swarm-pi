import { describe, expect, it } from "vitest";
import extension, { registerTaskManageExtension } from "../../.pi/extensions/taskmanage.ts";
import promptExtension from "../../.pi/extensions/swarm-prompt.ts";
import thinkingExtension from "../../.pi/extensions/swarm-thinking.ts";
import { taskManageSchema, InteractionBroker } from "../src/index.js";

type Handler = (event: any, ctx: any) => unknown;

function fakePi(entries: any[] = []) {
  const tools: any[] = [];
  const handlers = new Map<string, Handler[]>();
  const appended: any[] = [];
  const pi = {
    registerTool(tool: unknown) {
      tools.push(tool);
    },
    appendEntry(type: string, data: unknown) {
      const entry = { type: "custom", customType: type, data };
      appended.push(entry);
      entries.push(entry);
    },
    on(event: string, handler: Handler) {
      handlers.set(event, [...(handlers.get(event) ?? []), handler]);
    },
  };
  return { pi, tools, handlers, appended, entries };
}

describe("root Pi TaskManage extension", () => {
  it("is a discoverable default-exported Pi factory", () => {
    const runtime = fakePi();

    extension(runtime.pi);

    expect(runtime.tools).toHaveLength(5);
    expect(runtime.tools[0]).toMatchObject({
      name: "TaskManage",
      parameters: taskManageSchema,
      promptSnippet: expect.any(String),
      renderCall: expect.any(Function),
      renderResult: expect.any(Function),
    });
    expect(runtime.handlers.get("session_start")).toHaveLength(2);
    expect(runtime.handlers.get("tool_call")).toHaveLength(1);
    expect(runtime.handlers.get("tool_result")).toHaveLength(1);
    expect(runtime.handlers.get("turn_end")).toHaveLength(1);
  });

  it("validates questions, denies headless approvals, and emits updates", async () => {
    const sent: any[] = [];
    const broker = new InteractionBroker({ sendMessage: (message) => sent.push(message) }, { headless: true });
    await expect(broker.ask({ question: "Choose", kind: "single" })).rejects.toThrow("choices");
    expect(await broker.approve({ title: "Deploy" })).toMatchObject({ approved: false, status: "headless" });
    expect(broker.update({ message: "Working", progress: 0.5 })).toMatchObject({ message: "Working" });
    expect(sent[0]).toMatchObject({ customType: "swarm-agent-update" });
  });

  it("times out unanswered questions and approvals", async () => {
    const broker = new InteractionBroker({ ui: { input: async () => new Promise<string>(() => {}) , confirm: async () => new Promise<boolean>(() => {}) } }, { timeoutMs: 10 });
    expect(await broker.ask({ question: "Wait" })).toMatchObject({ status: "timed_out" });
    expect(await broker.approve({ title: "Wait" })).toMatchObject({ approved: false, status: "timed_out" });
  });

  it("shares one manager and persists task state through session entries", async () => {
    const first = fakePi();
    const { manager } = registerTaskManageExtension(first.pi);
    const tool = first.tools[0];

    await tool.execute("call-1", {
      operations: [{ key: "build", op: "create", subject: "Build adapter" }],
    });

    expect(manager.execute({ operations: [{ key: "get", op: "get", taskId: { ref: "build" } }] }).status)
      .toBe("succeeded");
    expect(first.appended.some(entry => entry.customType === "pi-swarm-task-state")).toBe(true);

    const second = fakePi(first.entries);
    registerTaskManageExtension(second.pi);
    const sessionStart = second.handlers.get("session_start")!;
    for (const handler of sessionStart) {
      await handler({}, { sessionManager: { getEntries: () => second.entries } });
    }

    const restored = await second.tools[0].execute("call-2", {
      operations: [{ key: "list", op: "list" }],
    });
    const payload = JSON.parse(restored.content[0].text);
    expect(payload.status).toBe("succeeded");
    expect(payload.results[0].data.tasks).toHaveLength(1);
    expect(payload.results[0].data.tasks[0].subject).toBe("Build adapter");
  });

  it("handles Pi lifecycle payloads whose event name is not in the payload", async () => {
    const runtime = fakePi();
    registerTaskManageExtension(runtime.pi, { enforcementMode: "block" });
    const toolCall = runtime.handlers.get("tool_call")![0];

    await expect(toolCall({ toolName: "write", input: {} }, {})).resolves.toMatchObject({
      block: true,
    });
  });

  it("overrides Pi's base prompt with the TUI-equivalent Forge prompt once", async () => {
    const runtime = fakePi();
    promptExtension(runtime.pi as any);
    const handler = runtime.handlers.get("before_agent_start")![0];
    const result: any = await handler({
      systemPrompt: "Pi's existing system instructions\n## Core Principles:\nhost-owned section",
      systemPromptOptions: { cwd: "/workspace/project" },
    }, {});

    expect(result.systemPrompt).not.toContain("Pi's existing system instructions");
    expect(result.systemPrompt).toContain("You are an expert software engineering assistant");
    expect(result.systemPrompt).toContain("# Delegation (the Task tool)");
    expect(result.systemPrompt.match(/You are an expert software engineering assistant/g)).toHaveLength(1);
    expect(result.systemPrompt).toContain("Current working directory: /workspace/project");
    await expect(handler({ systemPrompt: result.systemPrompt }, {})).resolves.toBeUndefined();
  });

  it("prefers Pi's active context cwd for Forge workspace metadata", async () => {
    const runtime = fakePi();
    promptExtension(runtime.pi as any);
    const handler = runtime.handlers.get("before_agent_start")![0];
    const result: any = await handler({
      systemPrompt: "base",
      systemPromptOptions: { cwd: "/stale/workspace" },
    }, { cwd: "/active/workspace" });
    expect(result.systemPrompt).toContain("Current working directory: /active/workspace");
    expect(result.systemPrompt).not.toContain("Current working directory: /stale/workspace");
  });

  it("renders Swarm-style task rows without operation plumbing", async () => {
    const runtime = fakePi();
    extension(runtime.pi);
    const tool = runtime.tools[0];
    const call = tool.renderCall({
      mode: "atomic",
      operations: [{ key: "plan", op: "create", subject: "Design the renderer" }],
    }, {}, {});
    expect(call.render(80).join("\n")).toContain("TaskManage");
    expect(call.render(80).join("\n")).toContain("Managing tasks");

    const result = await tool.execute("call-3", {
      operations: [{ key: "plan", op: "create", subject: "Design the renderer" }],
    });
    const panel = tool.renderResult(result, { isError: false, expanded: false }, {});
    expect(panel.render(100).join("\n")).toContain("○ #");
    expect(panel.render(100).join("\n")).toContain("Design the renderer");
    expect(panel.render(100).join("\n")).not.toContain("plan");
    expect(tool.renderShell).toBe("self");
  });

  it("keeps a compact open-task widget above the editor", async () => {
    const runtime = fakePi();
    extension(runtime.pi);
    const widgets: any[] = [];
    await runtime.tools[0].execute("call-widget", {
      operations: [
        { key: "build", op: "create", subject: "Build the renderer", category: "acting" },
        { key: "start", op: "update", taskId: { ref: "build" }, status: "in_progress", active: true },
      ],
    }, undefined, undefined, { ui: { setWidget: (key: string, content: unknown) => widgets.push({ key, content }) } });
    const latest = widgets.at(-1);
    expect(latest.key).toBe("swarm-tasks");
    const widget = latest.content({}, {});
    const lines = widget.render(80).join("\n");
    expect(lines).toContain("Tasks   0/1 done");
    expect(lines).toContain("● [A] Build the renderer");
  });

  it("keeps task output within narrow widths for wide subjects", async () => {
    const runtime = fakePi();
    extension(runtime.pi);
    const tool = runtime.tools[0];
    const result = await tool.execute("wide", {
      operations: [{ key: "wide", op: "create", subject: "界界界界界界界界" }],
    });
    const lines = tool.renderResult(result, {}, {}).render(12);
    const width = (line: string) => Array.from(line).reduce((total, character) =>
      total + (character.codePointAt(0)! >= 0x2e80 ? 2 : 1), 0);
    expect(lines.every((line: string) => width(line) <= 12)).toBe(true);
  });

  it("renders partial and malformed error results without stale-state wording", () => {
    const runtime = fakePi();
    extension(runtime.pi);
    const tool = runtime.tools[0];
    expect(tool.renderResult({ content: [] }, { isPartial: true }, {}).render(80).join("\n"))
      .toContain("Managing tasks");
    expect(tool.renderResult({ content: [] }, { isError: true }, {}).render(80).join("\n"))
      .toContain("Task update failed");
  });

  it("opens thinking settings from Ctrl+T and applies the selected level", async () => {
    let level = "off";
    let selectedTitle = "";
    let selectedOptions: string[] = [];
    const notifications: string[] = [];
    const shortcuts: any[] = [];
    const commands: any[] = [];
    const thinkingPi = {
      getThinkingLevel: () => level,
      setThinkingLevel: (next: string) => { level = next; },
      registerShortcut: (shortcut: string, options: any) => shortcuts.push({ shortcut, options }),
      registerCommand: (name: string, options: any) => commands.push({ name, options }),
    };
    thinkingExtension(thinkingPi as any);
    const ctx = {
      ui: {
        select: async (title: string, options: string[]) => {
          selectedTitle = title;
          selectedOptions = options;
          return options.find(option => option.includes("High"));
        },
        notify: (message: string) => notifications.push(message),
      },
    };

    await shortcuts[0].options.handler(ctx);
    expect(shortcuts[0].shortcut).toBe("ctrl+alt+t");
    expect(commands[0].name).toBe("thinking");
    expect(selectedTitle).toContain("Thinking settings");
    expect(selectedOptions).toHaveLength(6);
    expect(level).toBe("high");
    expect(notifications).toEqual(["Thinking level: high."]);
  });
});
