import { existsSync, readFileSync, writeFileSync } from "node:fs";
import { dirname, join } from "node:path";
import { mkdirSync } from "node:fs";

const swarmPromptDocument = new URL("../../upstream/swarm-sdk/swarm-tui/docs/FORGE_SWARM_SYSTEM_PROMPT.md", import.meta.url);

/** Extract the canonical prompt body from the upstream documentation verbatim. */
export function extractCanonicalSwarmPrompt(markdown: string): string {
  const match = markdown.match(/```text\n([\s\S]*?)\n```/);
  if (!match) throw new Error("Canonical Forge Swarm system prompt fenced block was not found");
  return match[1];
}

export const canonicalSwarmSystemPrompt = extractCanonicalSwarmPrompt(
  readFileSync(swarmPromptDocument, "utf8"),
);

/** A named system prompt shown by the /sp picker. */
export interface SystemPromptPreset {
  name: string;
  content: string;
}

interface PromptStore {
  prompts: SystemPromptPreset[];
  active?: string;
}

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
  on(event: "before_agent_start", handler: (event: { systemPrompt: string }, ctx: { cwd: string }) => unknown): void;
}

const fileName = ".pi/system-prompts.json";
const normalize = (value: string) => value.replace(/\r\n/g, "\n").trim();

export function promptStorePath(cwd: string): string {
  return join(cwd, fileName);
}

export function loadPromptStore(cwd: string): PromptStore {
  const path = promptStorePath(cwd);
  if (!existsSync(path)) return { prompts: [] };
  try {
    const parsed = JSON.parse(readFileSync(path, "utf8")) as Partial<PromptStore>;
    const prompts = Array.isArray(parsed.prompts)
      ? parsed.prompts.filter((p): p is SystemPromptPreset =>
        typeof p?.name === "string" && typeof p?.content === "string" && p.name.trim().length > 0)
      : [];
    const active = typeof parsed.active === "string" ? parsed.active : undefined;
    return { prompts, active: prompts.some(p => p.name === active) ? active : undefined };
  } catch {
    return { prompts: [] };
  }
}

export function savePromptStore(cwd: string, store: PromptStore): void {
  const path = promptStorePath(cwd);
  mkdirSync(dirname(path), { recursive: true });
  writeFileSync(path, `${JSON.stringify(store, null, 2)}\n`, "utf8");
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
  const builtIn: SystemPromptPreset = { name: "Pi default / current base", content: piPrompt || ctx.getSystemPrompt() };
  // The extension-owned list is augmented with Pi's existing prompt surfaces.
  // They are never copied into the persisted store: the source remains Pi.
  const saved = store.prompts.filter(p => p.name !== builtIn.name && p.name !== "Forge Swarm Agent");
  const swarm: SystemPromptPreset = { name: "Forge Swarm Agent", content: canonicalSwarmSystemPrompt };
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
        const existing = store.prompts.find(p => p.name === "Pi default / current base");
        if (existing) existing.content = cleaned;
        else store.prompts.unshift({ name: "Pi default / current base", content: cleaned });
        store.active = "Pi default / current base";
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
    // Selecting Pi's entry clears the extension override and restores Pi's
    // normal prompt construction (including its configured custom prompt).
    store.active = undefined;
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
  pi.on("before_agent_start", (event, ctx) => {
    const store = loadPromptStore(ctx.cwd || process.cwd());
    const active = store.active === "Forge Swarm Agent"
      ? { name: "Forge Swarm Agent", content: canonicalSwarmSystemPrompt }
      : store.prompts.find(p => p.name === store.active);
    return active ? { systemPrompt: active.content } : undefined;
  });
  pi.registerCommand("sp", {
    description: "List, select, add, or edit system prompts; use /sp current to inspect the exposed prompt",
    handler: openSystemPrompts,
  });
}
