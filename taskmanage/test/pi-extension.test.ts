import { describe, expect, it } from "vitest";
import extension, { registerTaskManageExtension } from "../../.pi/extensions/taskmanage.ts";
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
});
