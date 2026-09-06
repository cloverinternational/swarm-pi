import { describe, expect, it } from "vitest";
import { mkdirSync, mkdtempSync, writeFileSync } from "node:fs";
import { join } from "node:path";
import { tmpdir } from "node:os";
import { registerSwarmSkills } from "../../../../.pi/extensions/swarm-skills.ts";
import { savePromptContextConfig } from "../../../../.pi/lib/swarm-prompt-context-config.ts";

function makeSkill(root: string) {
  const dir = join(root, "probe-skill");
  mkdirSync(join(dir, "references"), { recursive: true });
  writeFileSync(join(dir, "SKILL.md"), "---\nname: probe-skill\ndescription: Probe skill\n---\nFull instructions");
  writeFileSync(join(dir, "references", "detail.md"), "Reference detail");
}

describe("Pi skill progressive disclosure", () => {
  it("registers metadata and full-content tools against the same discovered registry", async () => {
    const root = mkdtempSync(join(tmpdir(), "pi-skill-exposure-"));
    makeSkill(root);
    const tools = new Map<string, any>();
    const pi = { getCwd: () => root, registerTool: (tool: any) => tools.set(tool.name, tool), on() {}, registerCommand() {} };
    registerSwarmSkills(pi, { closed: true, cliPaths: [root] });

    expect(tools.has("skills_list")).toBe(true);
    expect(tools.has("skill_view")).toBe(true);
    const listed = await tools.get("skills_list").execute("list", {});
    expect(listed.content[0].text).toContain("probe-skill");
    const viewed = await tools.get("skill_view").execute("view", { name: "probe-skill" });
    expect(viewed.content[0].text).toContain("Full instructions");
    const reference = await tools.get("skill_view").execute("reference", { name: "probe-skill", file_path: "references/detail.md" });
    expect(reference.content[0].text).toContain("Reference detail");
  });

  it("rejects traversal and unknown skills without exposing filesystem errors", async () => {
    const root = mkdtempSync(join(tmpdir(), "pi-skill-exposure-"));
    makeSkill(root);
    const tools = new Map<string, any>();
    const pi = { getCwd: () => root, registerTool: (tool: any) => tools.set(tool.name, tool), on() {}, registerCommand() {} };
    registerSwarmSkills(pi, { closed: true, cliPaths: [root] });
    const view = tools.get("skill_view");
    const traversal = await view.execute("bad", { name: "probe-skill", file_path: "../SKILL.md" });
    expect(traversal.isError).toBe(true);
    const missing = await view.execute("missing", { name: "does-not-exist" });
    expect(missing.isError).toBe(true);
  });

  it("applies the persisted skill allowlist to inspection and viewing", async () => {
    const root = mkdtempSync(join(tmpdir(), "pi-skill-exposure-"));
    makeSkill(root);
    const other = join(root, "other-skill");
    mkdirSync(other, { recursive: true });
    writeFileSync(join(other, "SKILL.md"), "---\nname: other-skill\ndescription: Other\n---\nOther instructions");
    savePromptContextConfig(root, { version: 1, prompts: [], skills: { mode: "allowlist", names: ["probe-skill"] } });
    const tools = new Map<string, any>();
    const pi = { getCwd: () => root, registerTool: (tool: any) => tools.set(tool.name, tool), on() {}, registerCommand() {} };
    registerSwarmSkills(pi, { closed: true, cliPaths: [root] });
    const listed = await tools.get("skills_list").execute("list", {});
    expect(listed.content[0].text).toContain("probe-skill");
    expect(listed.content[0].text).not.toContain("other-skill");
    const hidden = await tools.get("skill_view").execute("view", { name: "other-skill" });
    expect(hidden.isError).toBe(true);
  });
});
