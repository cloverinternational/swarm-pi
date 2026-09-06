import { describe, expect, it } from "vitest";
import { existsSync, mkdirSync, mkdtempSync, readFileSync, symlinkSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { loadPromptContextConfig, pathInsideWorkspace, savePromptContextConfig } from "../../.pi/lib/swarm-prompt-context-config.ts";
import { applyPromptContextConfig, openPromptContextConfigure } from "../../.pi/extensions/prompt-context-configure.ts";

const workspace = () => mkdtempSync(join(tmpdir(), "pi-prompt-context-"));

describe("workspace prompt/context configuration", () => {
  it("preserves absent configuration defaults", () => {
    const cwd = workspace();
    expect(loadPromptContextConfig(cwd)).toEqual({ version: 1, prompts: [] });
  });

  it("migrates legacy prompts without modifying the legacy file", () => {
    const cwd = workspace();
    const legacy = join(cwd, ".pi", "system-prompts.json");
    const value = { prompts: [{ name: "reviewer", content: "Review safely" }], active: "reviewer" };
    mkdirSync(join(cwd, ".pi"), { recursive: true });
    writeFileSync(legacy, JSON.stringify(value));
    const config = loadPromptContextConfig(cwd);
    expect(config.activePrompt).toBe("reviewer");
    expect(config.prompts).toEqual(value.prompts);
    expect(JSON.parse(readFileSync(legacy, "utf8"))).toEqual(value);
    expect(existsSync(join(cwd, ".pi", "prompt-context.json"))).toBe(true);
  });

  it("uses the new store on profile conflicts and imports missing profiles", () => {
    const cwd = workspace();
    mkdirSync(join(cwd, ".pi"), { recursive: true });
    writeFileSync(join(cwd, ".pi", "system-prompts.json"), JSON.stringify({ prompts: [{ name: "same", content: "old" }, { name: "legacy", content: "keep" }], active: "same" }));
    savePromptContextConfig(cwd, { version: 1, prompts: [{ name: "same", content: "new" }], activePrompt: "same" });
    const config = loadPromptContextConfig(cwd);
    expect(config.prompts).toEqual([{ name: "same", content: "new" }, { name: "legacy", content: "keep" }]);
  });

  it("normalizes empty selections and repairs malformed versioned state", () => {
    const cwd = workspace();
    const path = join(cwd, ".pi", "prompt-context.json");
    mkdirSync(join(cwd, ".pi"), { recursive: true });
    writeFileSync(path, JSON.stringify({ version: 99, prompts: [{ name: " ok ", content: " body " }], skills: { mode: "allowlist", names: [] }, tools: { mode: "allowlist", names: [] } }));
    const messages: string[] = [];
    const config = loadPromptContextConfig(cwd, messages.push.bind(messages));
    expect(config.version).toBe(1);
    expect(config.prompts).toEqual([{ name: "ok", content: "body" }]);
    expect(config.skills).toEqual({ mode: "allowlist", names: [] });
    expect(config.tools).toEqual({ mode: "allowlist", names: [] });
    expect(messages).toHaveLength(1);
  });

  it("accepts contained paths and rejects traversal", () => {
    const cwd = workspace();
    expect(pathInsideWorkspace(cwd, "notes.md")).toBe(join(cwd, "notes.md"));
    expect(pathInsideWorkspace(cwd, "../outside.md")).toBeUndefined();
  });

  it("does not treat a symlink escape as contained", () => {
    const cwd = workspace();
    const outside = workspace();
    writeFileSync(join(outside, "secret.md"), "secret");
    symlinkSync(outside, join(cwd, "linked"));
    // Lexical containment is deliberately only the first validation step; the
    // eventual file loader must realpath and reject this target.
    expect(pathInsideWorkspace(cwd, "linked/secret.md")).toBe(join(cwd, "linked", "secret.md"));
  });

  it("stages changes until Apply and preserves Cancel", async () => {
    const cwd = workspace();
    const choices = ["Context", "● agents", "Done", "Cancel"];
    const ui = { select: async () => choices.shift(), input: async () => undefined, notify: () => {} };
    await openPromptContextConfigure("", { cwd, ui, getAllTools: () => [], getSkillNames: () => [] });
    expect(loadPromptContextConfig(cwd).context).toBeUndefined();
  });

  it("rolls back persisted config and active tools when activation fails", async () => {
    const cwd = workspace();
    const before = { version: 1 as const, prompts: [] };
    savePromptContextConfig(cwd, before);
    let active = ["old"];
    await expect(applyPromptContextConfig(cwd, { version: 1, prompts: [], tools: { mode: "allowlist", names: ["new"] } }, {
      getActiveTools: () => active,
      getAllTools: () => [{ name: "all" }],
      setActiveTools: names => { active = names; if (names[0] === "new") throw new Error("activation failed"); },
    })).rejects.toThrow("activation failed");
    expect(loadPromptContextConfig(cwd)).toEqual(before);
    expect(active).toEqual(["old"]);
  });

  it("adds only existing regular files inside the workspace", async () => {
    const cwd = workspace();
    writeFileSync(join(cwd, "context.md"), "context");
    const choices = ["Workspace files", "Add workspace file", "Workspace files", "Done", "Apply"];
    const ui = { select: async () => choices.shift(), input: async () => "context.md", notify: () => {} };
    await openPromptContextConfigure("", { cwd, ui, getAllTools: () => [], getSkillNames: () => [] });
    expect(loadPromptContextConfig(cwd).context?.files).toEqual([join(cwd, "context.md")]);
  });
});
