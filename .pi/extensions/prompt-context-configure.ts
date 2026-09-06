import { existsSync, lstatSync, realpathSync } from "node:fs";
import { BUILTIN_SOURCES } from "../lib/swarm-context.ts";
import { loadPromptContextConfig, savePromptContextConfig, type PromptContextConfig } from "../lib/swarm-prompt-context-config.ts";
import { pathInsideWorkspace } from "../lib/swarm-prompt-context-config.ts";
import { getSwarmSkillRegistry } from "../lib/swarm-skill-registry.ts";

type UI = { select(title: string, options: string[]): Promise<string | undefined>; input(title: string, placeholder?: string): Promise<string | undefined>; notify(message: string, type?: "info" | "warning" | "error"): void };

const selected = (name: string, yes: boolean) => `${yes ? "●" : "○"} ${name}`;
const names = (config: PromptContextConfig, category: string, tools: string[] = [], skills: string[] = []): string[] => {
  if (category === "Context") return BUILTIN_SOURCES.map(source => selected(source.id, config.context?.enabledSources?.[source.id] ?? source.enabled));
  if (category === "Skills") return skills.map(name => selected(name, config.skills?.mode !== "allowlist" || config.skills.names.includes(name)));
  if (category === "Tools") return tools.map(name => selected(name, config.tools?.mode !== "allowlist" || config.tools.names.includes(name)));
  if (category === "Workspace files") return (config.context?.files ?? []).map(file => selected(file, true));
  return config.prompts.map(prompt => selected(prompt.name, prompt.name === config.activePrompt));
};

const clone = <T>(value: T): T => structuredClone(value);
const allTools = (ctx: ConfigureContext): string[] => (ctx.getAllTools?.() ?? []).map(tool => tool.name).filter((name): name is string => Boolean(name));

export interface ConfigureContext {
  cwd: string;
  ui: UI;
  getAllTools?: () => Array<{ name?: string }>;
  getSkillNames?: () => string[];
  setActiveTools?: (names: string[]) => void | Promise<void>;
  getActiveTools?: () => string[];
}

/** Apply the whole draft as one transaction. On either side-effect failing, restore both stores. */
export async function applyPromptContextConfig(cwd: string, draft: PromptContextConfig, ctx: Pick<ConfigureContext, "getActiveTools" | "setActiveTools" | "getAllTools">): Promise<void> {
  const before = loadPromptContextConfig(cwd);
  const activeBefore = [...(ctx.getActiveTools?.() ?? [])];
  try {
    savePromptContextConfig(cwd, draft);
    if (draft.tools?.mode === "allowlist") await ctx.setActiveTools?.([...draft.tools.names]);
    else await ctx.setActiveTools?.(allTools(ctx));
  } catch (error) {
    try { savePromptContextConfig(cwd, before); } catch { /* preserve original error */ }
    try { await ctx.setActiveTools?.(activeBefore); } catch { /* preserve original error */ }
    throw error;
  }
}

function checkedWorkspaceFile(cwd: string, value: string): string | undefined {
  const lexical = pathInsideWorkspace(cwd, value.trim());
  if (!lexical || !existsSync(lexical) || !lstatSync(lexical).isFile()) return undefined;
  try {
    const real = realpathSync(lexical);
    return pathInsideWorkspace(cwd, real) ? lexical : undefined;
  } catch { return undefined; }
}

export async function openPromptContextConfigure(args: string, ctx: ConfigureContext): Promise<void> {
  const config = loadPromptContextConfig(ctx.cwd, message => ctx.ui.notify(message, "warning"));
  let draft = clone(config);
  let requested = args.trim();
  while (true) {
    const category = requested || await ctx.ui.select("Configure prompt and context · draft", ["System prompts", "Context", "Skills", "Tools", "Workspace files", "Restore defaults", "Apply", "Cancel"]);
    requested = "";
    if (!category || category === "Cancel") return ctx.ui.notify("Configuration cancelled; no changes applied.", "info");
    if (category === "Apply") {
      try { await applyPromptContextConfig(ctx.cwd, draft, ctx); ctx.ui.notify("Configuration applied.", "info"); }
      catch (error) { ctx.ui.notify(`Configuration rolled back: ${error instanceof Error ? error.message : String(error)}`, "error"); }
      return;
    }
    if (category === "Restore defaults") { draft = { version: 1, prompts: [] }; ctx.ui.notify("Defaults staged. Choose Apply to commit or Cancel to discard.", "info"); continue; }
    const normalized = category.replace(/^./, c => c.toUpperCase());
    const availableTools = allTools(ctx).sort();
    const availableSkills = (ctx.getSkillNames?.() ?? []).sort();
    if (normalized === "Workspace files") {
      const action = await ctx.ui.select("Workspace files · draft", ["Add workspace file", "Remove workspace file", "Done"]);
      if (action === "Add workspace file") {
        const value = await ctx.ui.input("Add workspace file (relative path)");
        if (value === undefined) continue;
        const file = checkedWorkspaceFile(ctx.cwd, value);
        if (!file) { ctx.ui.notify("File must be an existing regular file inside this workspace (symlink escapes are rejected).", "warning"); continue; }
        draft = { ...draft, context: { ...draft.context, files: [...new Set([...(draft.context?.files ?? []), file])] } };
      } else if (action === "Remove workspace file") {
        const file = await ctx.ui.select("Remove workspace file", draft.context?.files ?? []);
        if (file) draft = { ...draft, context: { ...draft.context, files: (draft.context?.files ?? []).filter(item => item !== file) } };
      }
      continue;
    }
    const options = names(draft, normalized, availableTools, availableSkills);
    if (!options.length) { ctx.ui.notify(`No ${normalized.toLowerCase()} are available.`, "warning"); continue; }
    const choice = await ctx.ui.select(`Configure ${normalized} · draft`, [...options, "Done"]);
    if (!choice || choice === "Done") continue;
    const name = choice.replace(/^[●○] /, "");
    if (normalized === "Context") {
      const enabledSources = { ...(draft.context?.enabledSources ?? Object.fromEntries(BUILTIN_SOURCES.map(source => [source.id, source.enabled]))) };
      enabledSources[name] = !enabledSources[name]; draft = { ...draft, context: { ...draft.context, enabledSources } };
    } else if (normalized === "Skills" || normalized === "Tools") {
      const available = normalized === "Skills" ? availableSkills : availableTools;
      const current = new Set(draft[normalized.toLowerCase() as "skills" | "tools"]?.mode === "allowlist" ? draft[normalized.toLowerCase() as "skills" | "tools"]!.names : available);
      current.has(name) ? current.delete(name) : current.add(name);
      draft = { ...draft, [normalized.toLowerCase()]: { mode: "allowlist", names: [...current].sort() } };
    } else draft = { ...draft, activePrompt: name };
  }
}

export default function promptContextConfigureExtension(pi: any): void {
  pi.registerCommand?.("configure", { description: "Configure prompt profiles, context, skills, and tools", handler: async (args: string, ctx: any) => {
    const cwd = ctx?.cwd ?? pi.getCwd?.() ?? process.cwd();
    await openPromptContextConfigure(args, { ...ctx, cwd, ui: ctx.ui, getAllTools: () => pi.getAllTools?.() ?? [], getActiveTools: () => pi.getActiveTools?.() ?? [], setActiveTools: (names: string[]) => pi.setActiveTools?.(names), getSkillNames: () => getSwarmSkillRegistry(pi, { cwd }).list().map(skill => skill.name) });
  } });
}
