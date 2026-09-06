import { describe, expect, it } from "vitest";
import { mkdirSync, mkdtempSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import promptExtension from "../../extensions/10-context/swarm-prompt.ts";
import systemPromptsExtension, {
  FORGE_PROMPT,
  PI_DEFAULT_PROMPT,
  resolveActiveSystemPrompt,
  savePromptStore,
} from "../../extensions/10-context/system-prompts.ts";

function fakePi() {
  const handlers = new Map<string, Array<(event: any, ctx: any) => any>>();
  const commands: string[] = [];
  return {
    handlers,
    commands,
    pi: {
      on(event: string, handler: (event: any, ctx: any) => any) {
        handlers.set(event, [...(handlers.get(event) ?? []), handler]);
      },
      registerCommand(name: string) { commands.push(name); },
    },
  };
}

describe("system-prompt ownership", () => {
  it("keeps mutation in swarm-prompt while system-prompts is UI-only", () => {
    const prompt = fakePi();
    const picker = fakePi();
    promptExtension(prompt.pi as any);
    systemPromptsExtension(picker.pi as any);

    expect(prompt.handlers.get("before_agent_start")).toHaveLength(1);
    expect(picker.handlers.has("before_agent_start")).toBe(false);
    expect(picker.commands).toEqual(["sp"]);
  });

  it("defaults a new workspace to the Forge owner", () => {
    const cwd = mkdtempSync(join(tmpdir(), "pi-prompt-owner-"));
    expect(resolveActiveSystemPrompt(cwd)).toMatchObject({ kind: "forge" });
  });

  it("honors the explicit Pi-base selection", async () => {
    const cwd = mkdtempSync(join(tmpdir(), "pi-prompt-owner-"));
    savePromptStore(cwd, { prompts: [], active: PI_DEFAULT_PROMPT });
    expect(resolveActiveSystemPrompt(cwd)).toEqual({ kind: "pi" });

    const runtime = fakePi();
    promptExtension(runtime.pi as any);
    const handler = runtime.handlers.get("before_agent_start")![0];
    const result: any = await handler({ systemPrompt: "Pi base", systemPromptOptions: { cwd } }, {});
    expect(result.systemPrompt).toContain("Pi base");
    expect(result.systemPrompt).toContain("<available_skills>");
  });

  it("migrates the old active-undefined representation as Pi base", () => {
    const cwd = mkdtempSync(join(tmpdir(), "pi-prompt-owner-"));
    const storePath = join(cwd, ".pi", "system-prompts.json");
    mkdirSync(join(cwd, ".pi"));
    writeFileSync(storePath, JSON.stringify({ prompts: [] }));
    expect(resolveActiveSystemPrompt(cwd)).toEqual({ kind: "pi" });
  });

  it("honors a custom prompt, including an override with Forge's display name", async () => {
    const cwd = mkdtempSync(join(tmpdir(), "pi-prompt-owner-"));
    savePromptStore(cwd, {
      prompts: [{ name: FORGE_PROMPT, content: "custom Forge override" }],
      active: FORGE_PROMPT,
    });
    expect(resolveActiveSystemPrompt(cwd)).toEqual({
      kind: "custom",
      name: FORGE_PROMPT,
      content: "custom Forge override",
    });

    const runtime = fakePi();
    promptExtension(runtime.pi as any);
    const handler = runtime.handlers.get("before_agent_start")![0];
    const result: any = await handler({ systemPrompt: "Pi base", systemPromptOptions: { cwd } }, {});
    expect(result.systemPrompt).toContain("custom Forge override");
    expect(result.systemPrompt).toContain("<available_skills>");
  });
});
