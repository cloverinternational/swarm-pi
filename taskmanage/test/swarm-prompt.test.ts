import { describe, expect, it } from "vitest";
import { mkdtempSync, readFileSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { assembleForgePrompt, comparePromptGolden, extractGoStringConstant, forgeSwarmSystemPrompt, swarmForgeSystemPrompt } from "../../.pi/extensions/swarm-prompt";

describe("Forge prompt assembly", () => {
  it("loads the exact live Forge constant rather than the stale documentation copy", () => {
    const source = readFileSync(new URL("../../upstream/swarm-sdk/swarm-tui/internal/chat/settings/system_prompt.go", import.meta.url), "utf8");
    expect(forgeSwarmSystemPrompt).toBe(extractGoStringConstant(source, "forgeSwarmSystemPrompt"));
    expect(forgeSwarmSystemPrompt).toContain("You have access to TaskManage");
    expect(forgeSwarmSystemPrompt).toContain("## Planning and Requirement Discovery");
    expect(forgeSwarmSystemPrompt).not.toContain("task_create");
    expect(swarmForgeSystemPrompt).toBe(extractGoStringConstant(source, "swarmForgeSystemPrompt"));
    expect(swarmForgeSystemPrompt).toContain("# Delegation (the Task tool)");
  });

  it("replaces Pi's base prompt with the TUI-equivalent Forge prompt", () => {
    const cwd = mkdtempSync(join(tmpdir(), "pi-prompt-"));
    writeFileSync(join(cwd, "AGENTS.md"), "workspace instructions");
    const result = assembleForgePrompt("Pi's original system prompt", { cwd, userPrompt: "request", tools: [{ name: "read", guidance: "read safely" }], skills: [{ name: "review", instructions: "review carefully" }], restrictions: ["stay in workspace"] });
    expect(result.prompt).not.toContain("Pi's original system prompt");
    expect(result.prompt.indexOf("## Core Principles:")).toBeLessThan(result.prompt.indexOf("# Delegation (the Task tool)"));
    expect(result.prompt).toContain("workspace instructions");
    expect(result.prompt).toContain("stay in workspace");
    expect(result.hash).toMatch(/^sha256:/);
    expect(result.provenance.map((entry) => entry.section)).toEqual(["workspace", "forge", "user", "tools", "skills", "context", "restrictions"]);
    expect(result.provenance.every((entry) => !entry.ref.includes("workspace instructions"))).toBe(true);
  });
  it("rejects context traversal and supports golden comparison", () => {
    const cwd = mkdtempSync(join(tmpdir(), "pi-prompt-"));
    const result = assembleForgePrompt("host", { cwd, contextFiles: ["../secret"] });
    expect(result.contextFiles).toEqual([]);
    expect(comparePromptGolden(result, result.prompt)).toEqual({ equal: true, actualHash: result.hash, goldenHash: result.hash });
  });
});
