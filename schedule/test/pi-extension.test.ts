import { mkdtemp, rm } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { registerScheduleExtension } from "../../.pi/extensions/schedule.js";

const roots: string[] = [];
beforeEach(() => {
  vi.useFakeTimers();
  vi.setSystemTime(new Date("2026-01-01T00:00:00.000Z"));
});
afterEach(async () => {
  vi.useRealTimers();
  await Promise.all(roots.splice(0).map((root) => rm(root, { recursive: true, force: true })));
});

describe("Pi schedule extension", () => {
  it("delivers fired prompts as follow-up user messages and shuts down cleanly", async () => {
    const root = await mkdtemp(join(tmpdir(), "pi-schedule-extension-"));
    roots.push(root);
    const tools = new Map<string, any>();
    const messages: unknown[] = [];
    const handlers = new Map<string, (...args: any[]) => unknown>();
    const pi = {
      getCwd: () => root,
      registerTool: (tool: any) => tools.set(tool.name, tool),
      sendUserMessage: (content: string, options: unknown) => messages.push({ content, options }),
      on: (event: string, handler: (...args: any[]) => unknown) => handlers.set(event, handler),
    };
    const scheduler = await registerScheduleExtension(pi, {
      idFactory: (kind) => `${kind}-12345678`,
    });
    expect(tools.size).toBe(4);

    await tools.get("schedule_wakeup").execute("call", { prompt: "continue", delay: "1s" });
    await vi.advanceTimersByTimeAsync(1_000);
    await scheduler.idle();
    expect(messages).toEqual([{ content: "continue", options: { deliverAs: "followUp" } }]);

    await handlers.get("session_shutdown")?.();
    expect(scheduler.list()).toEqual([]);
  });
});
