import { describe, expect, it } from "vitest";
import { GENERAL_AGENT_TASK, validateGeneralAgentInput } from "../src/general-agent.js";

describe("general-agent contract", () => {
  it("accepts the durable task input and preserves identity", () => {
    expect(validateGeneralAgentInput({ prompt: "inspect", workspace: "/repo", sessionId: "s1", conversationId: "c1", provider: "p", model: "m" })).toMatchObject({ sessionId: "s1", conversationId: "c1" });
    expect(GENERAL_AGENT_TASK).toBe("pi-swarm.general-agent");
  });
  it("rejects missing, non-positive, and excessive limits", () => {
    expect(() => validateGeneralAgentInput({ prompt: "x" })).toThrow("required strings");
    const base = { prompt: "x", workspace: "/r", sessionId: "s", conversationId: "c", provider: "p", model: "m" };
    expect(() => validateGeneralAgentInput({ ...base, maxTurns: 0 })).toThrow("maxTurns");
    expect(() => validateGeneralAgentInput({ ...base, timeoutSeconds: 86401 })).toThrow("timeoutSeconds");
  });
});
