import { describe, expect, it } from "vitest";
import { assertAllowed, createRuntimeProfile, hashPrompt, provenance } from "../src/index.js";

describe("immutable runtime profiles", () => {
  it("normalizes and freezes every allowlist", () => {
    const tools = ["bash", "read", "bash"];
    const profile = createRuntimeProfile({ tools, skills: ["forge"], context: { files: ["b", "a"] } });
    expect(profile.tools).toEqual(["bash", "read"]);
    expect(profile.context.files).toEqual(["a", "b"]);
    expect(Object.isFrozen(profile)).toBe(true);
    expect(Object.isFrozen(profile.tools)).toBe(true);
    expect(() => (profile.tools as string[]).push("write")).toThrow();
    tools.push("write");
    expect(profile.tools).not.toContain("write");
  });

  it("produces the same digest independent of input ordering", () => {
    const a = createRuntimeProfile({ tools: ["b", "a"], packages: [provenance("x", "1", "npm")] });
    const b = createRuntimeProfile({ tools: ["a", "b"], packages: [provenance("x", "1", "npm")] });
    expect(a.digest).toBe(b.digest);
  });

  it("checks exact kind allowlists and does not leak prompt material", () => {
    const profile = createRuntimeProfile({ tools: ["read"], capabilities: ["filesystem.read" ] });
    expect(() => assertAllowed(profile, "tool", "read")).not.toThrow();
    expect(() => assertAllowed(profile, "tool", "write")).toThrow("profile denied tool:write");
    expect(hashPrompt("secret prompt")).toMatch(/^sha256:[0-9a-f]{64}$/);
    expect(hashPrompt("secret prompt")).not.toContain("secret");
  });
});
