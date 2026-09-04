import { describe, expect, it } from "vitest";
import { mkdtempSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { assembleForgePrompt, comparePromptGolden } from "./swarm-prompt";

describe("Forge prompt assembly", () => {
  it("assembles deterministic ordered sections and redacted provenance", () => {
    const cwd = mkdtempSync(join(tmpdir(), "pi-prompt-"));
    writeFileSync(join(cwd, "AGENTS.md"), "workspace instructions");
    const result = assembleForgePrompt("host", { cwd, userPrompt: "request", tools: [{ name: "read", guidance: "read safely" }], skills: [{ name: "review", instructions: "review carefully" }], restrictions: ["stay in workspace"] });
    expect(result.prompt.indexOf("host")).toBeLessThan(result.prompt.indexOf("## Core Principles:"));
    expect(result.prompt).toContain("workspace instructions");
    expect(result.prompt).toContain("stay in workspace");
    expect(result.hash).toMatch(/^sha256:/);
    expect(result.provenance.map((entry) => entry.section)).toEqual(["base", "workspace", "forge", "user", "tools", "skills", "context", "restrictions"]);
    expect(result.provenance.every((entry) => !entry.ref.includes("workspace instructions"))).toBe(true);
  });
  it("rejects context traversal and supports golden comparison", () => {
    const cwd = mkdtempSync(join(tmpdir(), "pi-prompt-"));
    const result = assembleForgePrompt("host", { cwd, contextFiles: ["../secret"] });
    expect(result.contextFiles).toEqual([]);
    expect(comparePromptGolden(result, result.prompt)).toEqual({ equal: true, actualHash: result.hash, goldenHash: result.hash });
  });
});
