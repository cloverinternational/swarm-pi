import { describe, expect, it } from "vitest";
import { createGeneralAgentHandler } from "../src/general-agent-adapter.js";

const input = { prompt: "hello", workspace: "/tmp", sessionId: "s", conversationId: "c", provider: "test", model: "test" };
const ctx = { step: async (_name: string, fn: () => Promise<unknown>) => fn() } as any;

describe("general-agent adapter", () => {
  it("checkpoints through Absurd steps and runs injected runtime", async () => {
    const calls: string[] = [];
    const result = await createGeneralAgentHandler((_input, _ctx, checkpoint) => ({
      sessionId: "s", conversationId: "c", resumable: false,
      run: async (_p, _s) => { calls.push("run"); }, continue: async () => { calls.push("continue"); },
    } as any))(input, ctx);
    expect(result.status).toBe("completed");
    expect(calls).toEqual(["run"]);
  });

  it("uses continuation only when runtime marks a resumable boundary", async () => {
    const calls: string[] = [];
    await createGeneralAgentHandler((_input, _ctx, checkpoint) => ({
      sessionId: "s", conversationId: "c", resumable: true,
      run: async () => calls.push("run"), continue: async () => calls.push("continue"),
    } as any))(input, ctx);
    expect(calls).toEqual(["continue"]);
  });
});
