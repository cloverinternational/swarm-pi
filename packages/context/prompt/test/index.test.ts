import { describe, expect, it } from "vitest";
import { readFileSync } from "node:fs";
import { fileURLToPath } from "node:url";
// @ts-expect-error -- plain ESM helper, intentionally untyped
import { parseGoStringConst } from "../scripts/go-const.mjs";
import {
  UPSTREAM_SOURCE,
  forgeSwarmSystemPrompt,
  swarmForgeDelegationAddendum,
  swarmForgeSystemPrompt,
  swarmPromptPresets,
} from "../src/index.js";

const goSource = readFileSync(
  fileURLToPath(new URL(`../../../../${UPSTREAM_SOURCE}`, import.meta.url)),
  "utf8",
);

describe("vendored SwarmForge prompt", () => {
  it("matches the upstream Go constants byte-for-byte", () => {
    expect(forgeSwarmSystemPrompt).toBe(parseGoStringConst(goSource, "forgeSwarmSystemPrompt"));
    expect(swarmForgeDelegationAddendum).toBe(
      parseGoStringConst(goSource, "swarmForgeDelegationAddendum"),
    );
  });

  it("composes exactly as upstream does", () => {
    expect(swarmForgeSystemPrompt).toBe(forgeSwarmSystemPrompt + swarmForgeDelegationAddendum);
    expect(swarmForgeSystemPrompt).toBe(parseGoStringConst(goSource, "swarmForgeSystemPrompt"));
  });

  it("preserves embedded markdown code spans", () => {
    // Regression guard: reading only the first raw string truncates the body
    // at the first spliced backtick, which lands mid-sentence in the shell
    // guidance section.
    expect(forgeSwarmSystemPrompt).toContain("`rg PATTERN`");
    expect(forgeSwarmSystemPrompt.trimEnd()).toMatch(/<\/non_negotiable_rules>$/);
    expect(swarmForgeDelegationAddendum).toContain("`general-assistant`");
  });

  it("carries current tool names, not the stale documented ones", () => {
    // docs/FORGE_SWARM_SYSTEM_PROMPT.md lags the source and still names
    // task_create/task_update; the shipped prompt uses TaskManage.
    expect(forgeSwarmSystemPrompt).toContain("TaskManage");
    expect(forgeSwarmSystemPrompt).not.toContain("task_create");
  });

  it("excludes the runtime workspace block, which the host prepends", () => {
    expect(swarmForgeSystemPrompt).not.toContain("<system_information>");
  });

  it("exposes the two workspace-context builtins the TUI registers", () => {
    expect(swarmPromptPresets.map((preset) => preset.name)).toEqual([
      "Accumulated Context Engineering",
      "SwarmForge",
    ]);
    for (const preset of swarmPromptPresets) {
      expect(preset.workspaceContext).toBe(true);
      expect(goSource).toContain(`Content:          ${preset.constant},`);
      expect(goSource).toContain(`Name:             "${preset.name}",`);
    }
  });

  it("does not read upstream sources at runtime", () => {
    const loader = readFileSync(fileURLToPath(new URL("../src/index.ts", import.meta.url)), "utf8");
    // UPSTREAM_SOURCE records provenance as a string, so the mention of
    // vendor/ is expected. What must not happen is opening it: every path
    // the loader resolves has to live under ../assets/.
    const resolved = [...loader.matchAll(/new URL\(\s*`([^`]*)`/g)].map((match) => match[1]);
    expect(resolved.length).toBeGreaterThan(0);
    for (const path of resolved) expect(path).toMatch(/^\.\.\/assets\//);
    expect(loader).not.toMatch(/readFileSync\([^)]*vendor/);
  });
});
