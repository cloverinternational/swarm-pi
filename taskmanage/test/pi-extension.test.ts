import { describe, expect, it } from "vitest";
import extension, { registerTaskManageExtension } from "../../.pi/extensions/taskmanage.ts";
import promptExtension from "../../.pi/extensions/swarm-prompt.ts";
import thinkingExtension from "../../.pi/extensions/swarm-thinking.ts";
import { taskManageSchema } from "../src/index.js";

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
      appended.push({ type, data });
      entries.push({ type, data });
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

    expect(runtime.tools).toHaveLength(1);
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

  it("shares one manager and persists task state through session entries", async () => {
    const first = fakePi();
    const { manager } = registerTaskManageExtension(first.pi);
    const tool = first.tools[0];

    await tool.execute("call-1", {
      operations: [{ key: "build", op: "create", subject: "Build adapter" }],
    });

    expect(manager.execute({ operations: [{ key: "get", op: "get", taskId: { ref: "build" } }] }).status)
      .toBe("succeeded");
    expect(first.appended.some(entry => entry.type === "pi-swarm-task-state")).toBe(true);

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

  it("handles Pi lifecycle payloads whose event name is not in the payload", () => {
    const runtime = fakePi();
    registerTaskManageExtension(runtime.pi, { enforcementMode: "block" });
    const toolCall = runtime.handlers.get("tool_call")![0];

    expect(toolCall({ toolName: "write", input: {} }, {})).toMatchObject({
      block: true,
    });
  });

  it("injects the canonical Forge prompt once and preserves Pi's base prompt", () => {
    const runtime = fakePi();
    promptExtension(runtime.pi as any);
    const handler = runtime.handlers.get("before_agent_start")![0];
    const result = handler({
      systemPrompt: "Pi's existing system instructions",
      systemPromptOptions: { cwd: "/workspace/project" },
    }, {});

    expect(result.systemPrompt).toContain("Pi's existing system instructions");
    expect(result.systemPrompt).toContain("You are an expert software engineering assistant");
    expect(result.systemPrompt).toContain("Current working directory: /workspace/project");
    expect(handler({ systemPrompt: result.systemPrompt }, {})).toBeUndefined();
  });

  it("renders task calls and results as compact readable status panels", async () => {
    const runtime = fakePi();
    extension(runtime.pi);
    const tool = runtime.tools[0];
    const call = tool.renderCall({
      mode: "atomic",
      operations: [{ key: "plan", op: "create", subject: "Design the renderer" }],
    }, {}, {});
    expect(call.render(80).join("\n")).toContain("TaskManage");
    expect(call.render(80).join("\n")).toContain("Design the renderer");

    const result = await tool.execute("call-3", {
      operations: [{ key: "plan", op: "create", subject: "Design the renderer" }],
    });
    const panel = tool.renderResult(result, { isError: false, expanded: false }, {});
    expect(panel.render(100).join("\n")).toContain("SUCCEEDED");
    expect(panel.render(100).join("\n")).toContain("plan");
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
