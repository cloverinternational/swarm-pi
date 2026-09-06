import { join } from "node:path";
import { swarmForgeSystemPrompt } from "../../swarm-prompt/src/index.ts";
import { loadPromptContextConfig, savePromptContextConfig } from "../lib/swarm-prompt-context-config.ts";

/**
 * The canonical prompt now comes from the vendored @pi-swarm/swarm-prompt
 * package. It previously came from docs/FORGE_SWARM_SYSTEM_PROMPT.md, which
 * lags the Go source badly -- the doc is roughly half the size and still names
 * the retired task_create/task_update tools.
 */
export const canonicalSwarmSystemPrompt = swarmForgeSystemPrompt;

/** A named system prompt shown by the /sp picker. */
export interface SystemPromptPreset {
  name: string;
  content: string;
}

export interface PromptStore {
  prompts: SystemPromptPreset[];
  active?: string;
}

export const PI_DEFAULT_PROMPT = "Pi default / current base";
export const FORGE_PROMPT = "Forge Swarm Agent";

export interface SystemPromptUI {
  select(title: string, options: string[]): Promise<string | undefined>;
  input(title: string, placeholder?: string): Promise<string | undefined>;
  editor(title: string, prefill?: string): Promise<string | undefined>;
  notify(message: string, type?: "info" | "warning" | "error"): void;
}

export interface SystemPromptContext {
  ui: SystemPromptUI;
  cwd: string;
  /** The effective prompt after Pi and other extensions have assembled it. */
  getSystemPrompt(): string;
  /** Pi's underlying prompt configuration, including customPrompt/appendSystemPrompt. */
  getSystemPromptOptions?: () => { customPrompt?: string; appendSystemPrompt?: string };
}

export interface SystemPromptAPI {
  registerCommand(name: string, options: {
    description: string;
    handler: (args: string, ctx: SystemPromptContext) => Promise<void>;
  }): void;
}

const normalize = (value: string) => value.replace(/\r\n/g, "\n").trim();

export function promptStorePath(cwd: string): string {
  return join(cwd, ".pi", "prompt-context.json");
}

export function loadPromptStore(cwd: string): PromptStore {
  const config = loadPromptContextConfig(cwd);
  return { prompts: config.prompts, active: config.activePrompt ?? FORGE_PROMPT };
}

export type ActiveSystemPrompt =
  | { kind: "forge"; content: string }
  | { kind: "pi" }
  | { kind: "custom"; name: string; content: string };

/**
 * Resolve selection only; this module never mutates `before_agent_start`.
 * `swarm-prompt.ts` is the single owner of base system-prompt composition.
 */
export function resolveActiveSystemPrompt(cwd: string): ActiveSystemPrompt {
  const store = loadPromptStore(cwd);
  const custom = store.prompts.find(prompt => prompt.name === store.active);
  // An explicitly saved override wins even when it uses a builtin display name.
  if (custom) return { kind: "custom", name: custom.name, content: custom.content };
  if (store.active === PI_DEFAULT_PROMPT) return { kind: "pi" };
  return { kind: "forge", content: canonicalSwarmSystemPrompt };
}

export function savePromptStore(cwd: string, store: PromptStore): void {
  const config = loadPromptContextConfig(cwd);
  savePromptContextConfig(cwd, { ...config, prompts: store.prompts, ...(store.active ? { activePrompt: store.active } : {}) });
}

function describe(prompt: SystemPromptPreset, active?: string): string {
  return `${prompt.name === active ? "●" : "○"} ${prompt.name}${prompt.name === active ? " · current" : ""}`;
}

async function editPrompt(cwd: string, store: PromptStore, name: string, ui: SystemPromptUI): Promise<void> {
  const prompt = store.prompts.find(p => p.name === name);
  if (!prompt) {
    ui.notify(`System prompt not found: ${name}`, "error");
    return;
  }
  const content = await ui.editor(`Edit system prompt · ${name}`, prompt.content);
  if (content === undefined) return;
  const cleaned = normalize(content);
  if (!cleaned) {
    ui.notify("A system prompt cannot be empty.", "warning");
    return;
  }
  prompt.content = cleaned;
  savePromptStore(cwd, store);
  ui.notify(`Updated system prompt: ${name}`, "info");
}

async function addPrompt(cwd: string, store: PromptStore, ui: SystemPromptUI): Promise<void> {
  const name = (await ui.input("New system prompt · name", "e.g. reviewer"))?.trim();
  if (!name) return;
  if (store.prompts.some(p => p.name === name)) {
    ui.notify(`A system prompt named “${name}” already exists.`, "warning");
    return;
  }
  const content = normalize((await ui.editor(`New system prompt · ${name}`, "")) ?? "");
  if (!content) {
    ui.notify("A system prompt cannot be empty.", "warning");
    return;
  }
  store.prompts.push({ name, content });
  savePromptStore(cwd, store);
  ui.notify(`Added system prompt: ${name}`, "info");
}

async function showCurrent(ctx: SystemPromptContext): Promise<void> {
  const prompt = ctx.getSystemPrompt();
  if (!prompt) {
    ctx.ui.notify("The agent currently has no system prompt.", "warning");
    return;
  }
  // The multiline editor makes the complete prompt inspectable without putting
  // sensitive prompt text into notifications or logs. Exiting does not save it.
  await ctx.ui.editor("Current system prompt · read-only", prompt);
}

export async function openSystemPrompts(args: string, ctx: SystemPromptContext): Promise<void> {
  const cwd = ctx.cwd || process.cwd();
  const store = loadPromptStore(cwd);
  const piPrompt = ctx.getSystemPromptOptions?.()?.customPrompt;
  const builtIn: SystemPromptPreset = { name: PI_DEFAULT_PROMPT, content: piPrompt || ctx.getSystemPrompt() };
  // The extension-owned list is augmented with Pi's existing prompt surfaces.
  // They are never copied into the persisted store: the source remains Pi.
  const saved = store.prompts.filter(p => p.name !== builtIn.name && p.name !== FORGE_PROMPT);
  const swarm: SystemPromptPreset = { name: FORGE_PROMPT, content: canonicalSwarmSystemPrompt };
  const command = args.trim();

  if (command === "current" || command === "show") {
    await showCurrent(ctx);
    return;
  }
  if (command === "pi" || command === "default") {
    await ctx.ui.editor("Pi system prompt · current base", builtIn.content);
    return;
  }
  if (command === "swarm" || command === "forge") {
    store.active = swarm.name;
    savePromptStore(cwd, store);
    ctx.ui.notify("Forge Swarm Agent system prompt selected.", "info");
    return;
  }
  if (command === "add" || command === "new") {
    await addPrompt(cwd, store, ctx.ui);
    return;
  }
  if (command.startsWith("edit ")) {
    const name = command.slice(5).trim();
    if (name === builtIn.name || name === "pi" || name === "default") {
      await ctx.ui.editor("Edit Pi system prompt · session override", builtIn.content);
    } else await editPrompt(cwd, store, name, ctx.ui);
    return;
  }
  if (command.startsWith("use ")) {
    const name = command.slice(4).trim();
    if (name === "pi" || name === "default" || name === builtIn.name) {
      store.active = undefined;
      savePromptStore(cwd, store);
      ctx.ui.notify("Using Pi's existing system prompt.", "info");
      return;
    }
    if (name === swarm.name) {
      store.active = swarm.name;
      savePromptStore(cwd, store);
      ctx.ui.notify("Forge Swarm Agent system prompt selected.", "info");
      return;
    }
    if (!saved.some(p => p.name === name)) {
      ctx.ui.notify(`System prompt not found: ${name}`, "error");
      return;
    }
    store.active = name;
    savePromptStore(cwd, store);
    ctx.ui.notify(`System prompt selected: ${name}`, "info");
    return;
  }

  const selected = await ctx.ui.select(
    "System prompts · choose one (Pi and extension prompts)",
    [describe(builtIn, store.active ? "" : builtIn.name), describe(swarm, store.active), ...saved.map(p => describe(p, store.active)), "＋ Add system prompt", "✎ Edit a system prompt", "▣ Show current exposed prompt"],
  );
  if (!selected) return;
  if (selected === "＋ Add system prompt") return addPrompt(cwd, store, ctx.ui);
  if (selected === "▣ Show current exposed prompt") return showCurrent(ctx);
  if (selected === "✎ Edit a system prompt") {
    const name = await ctx.ui.select("Edit system prompt", [builtIn.name, swarm.name, ...saved.map(p => p.name)]);
    if (!name) return;
    if (name === swarm.name) {
      const content = await ctx.ui.editor("Edit Forge Swarm Agent · session override", swarm.content);
      const cleaned = normalize(content ?? "");
      if (cleaned) {
        store.prompts.unshift({ name: swarm.name, content: cleaned });
        store.active = swarm.name;
        savePromptStore(cwd, store);
        ctx.ui.notify("Saved and activated a Forge Swarm prompt override.", "info");
      }
      return;
    }
    if (name === builtIn.name) {
      const content = await ctx.ui.editor("Edit Pi system prompt · session override", builtIn.content);
      const cleaned = normalize(content ?? "");
      if (cleaned) {
        // Pi exposes its base options as read-only. Save an explicit override
        // rather than pretending to mutate Pi's own configuration.
        const existing = store.prompts.find(p => p.name === PI_DEFAULT_PROMPT);
        if (existing) existing.content = cleaned;
        else store.prompts.unshift({ name: PI_DEFAULT_PROMPT, content: cleaned });
        store.active = PI_DEFAULT_PROMPT;
        savePromptStore(cwd, store);
        ctx.ui.notify("Saved and activated a Pi system-prompt override.", "info");
      }
    } else await editPrompt(cwd, store, name, ctx.ui);
    return;
  }
  const name = selected.replace(/^[●○] /, "").replace(/ · current$/, "");
  if (name === swarm.name) {
    store.active = swarm.name;
    savePromptStore(cwd, store);
    ctx.ui.notify("Forge Swarm Agent system prompt selected.", "info");
    return;
  }
  if (name === builtIn.name) {
    // Persist this choice explicitly. An absent store means the project
    // default (Forge), so clearing active would make "use Pi" non-durable.
    store.active = PI_DEFAULT_PROMPT;
    savePromptStore(cwd, store);
    ctx.ui.notify("Using Pi's existing system prompt.", "info");
    return;
  }
  if (saved.some(p => p.name === name)) {
    store.active = name;
    savePromptStore(cwd, store);
    ctx.ui.notify(`System prompt selected: ${name}`, "info");
  }
}

export default function systemPromptsExtension(pi: SystemPromptAPI): void {
  pi.registerCommand("sp", {
    description: "List, select, add, or edit system prompts; use /sp current to inspect the exposed prompt",
    handler: openSystemPrompts,
  });
}
