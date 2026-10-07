import { expect, it } from "vitest";
import { readFileSync } from "node:fs";
import { resolve } from "node:path";
import { vi } from "vitest";
const mockPrepare = vi.fn(() => ({ messagesToSummarize: [{ role: "user", content: "history" }], turnPrefixMessages: [], firstKeptEntryId: "keep", previousSummary: undefined, tokensBefore: 330000, fileOps: {} }));
vi.mock("@earendil-works/pi-coding-agent", () => ({
  convertToLlm: (messages: any[]) => messages,
  estimateTokens: () => 1,
  prepareCompaction: (...args: any[]) => mockPrepare(...args),
  serializeConversation: () => "old conversation",
}));
import extension from "../../../extensions/mandatory-compaction/extension.ts";

it("registers the organization-wide threshold extension in the context package", () => {
  const packageJson = JSON.parse(readFileSync(resolve("package.json"), "utf8"));
  expect(packageJson.pi.extensions).toContain("extensions/mandatory-compaction/index.ts");
  const handlers = new Map<string, Function>();
  extension({ on: (name: string, handler: Function) => handlers.set(name, handler) } as any);
  expect(handlers.has("turn_end")).toBe(true);
});

it("compacts at the fixed target and requests one continuation after a successful summary", async () => {
  let handler: any;
  const pi: any = { on: (name: string, fn: any) => { if (name === "turn_end") handler = fn; } };
  extension(pi);
  const complete = vi.fn(async () => ({ stopReason: "stop", content: [{ type: "text", text: "checkpoint" }], usage: {} }));
  const result = await handler({ entries: [], message: { role: "assistant" } }, {
    model: { contextWindow: 1_000_000, maxTokens: 8192 },
    getContextUsage: () => ({ tokens: 330_000, contextWindow: 1_000_000 }),
    getSettings: () => ({ compaction: {} }),
    sessionManager: { getBranch: () => [{ type: "message", id: "keep", message: { role: "user", content: "recent" } }] },
    modelRegistry: { complete }, signal: undefined,
  });
  expect(complete).toHaveBeenCalledOnce();
  expect(result.continue).toBe(true);
  expect(result.entries.map((entry: any) => entry.type)).toEqual(["compaction", "custom_message"]);
});

it("does not compact below threshold or for unknown/small-window models", async () => {
  let handler: any;
  extension({ on: (_: string, fn: any) => { handler = fn; } } as any);
  const complete = vi.fn();
  const ctx: any = {
    model: { contextWindow: 1_000_000 }, getContextUsage: () => ({ tokens: 324_999 }),
    sessionManager: { getBranch: () => [{ type: "message", id: "keep", message: {} }] },
    modelRegistry: { complete },
  };
  expect(await handler({ entries: [] }, ctx)).toBeUndefined();
  ctx.getContextUsage = () => ({ tokens: null });
  expect(await handler({ entries: [] }, ctx)).toBeUndefined();
  ctx.getContextUsage = () => ({ tokens: 400_000 });
  ctx.model = { contextWindow: 200_000 };
  expect(await handler({ entries: [] }, ctx)).toBeUndefined();
  expect(complete).not.toHaveBeenCalled();
});

it("latches failed or no-progress attempts instead of retrying every turn", async () => {
  let handler: any;
  extension({ on: (_: string, fn: any) => { handler = fn; } } as any);
  const complete = vi.fn().mockRejectedValue(new Error("summary unavailable"));
  const ctx: any = {
    model: { contextWindow: 1_000_000, maxTokens: 8192 },
    getContextUsage: () => ({ tokens: 330_000 }), getSettings: () => ({ compaction: {} }),
    sessionManager: { getBranch: () => [{ type: "message", id: "keep", message: {} }] },
    modelRegistry: { complete },
  };
  expect(await handler({ entries: [] }, ctx)).toBeUndefined();
  expect(await handler({ entries: [] }, ctx)).toBeUndefined();
  expect(complete).toHaveBeenCalledOnce();
  ctx.getContextUsage = () => ({ tokens: 324_999 });
  await handler({ entries: [] }, ctx);
  ctx.getContextUsage = () => ({ tokens: 330_000 });
  await handler({ entries: [] }, ctx);
  expect(complete).toHaveBeenCalledTimes(2);
});
