import { existsSync, readdirSync, readFileSync } from "node:fs";
import { join, resolve } from "node:path";
import { SkillLoader } from "../../skills/src/index.ts";
import { resolveActiveSystemPrompt } from "./system-prompts.ts";

/** Inspect the exact prompt/resources exposed by the current Pi-Swarm process. */
export interface InspectorContext {
  cwd?: string;
  ui?: { editor?: (title: string, content?: string) => Promise<string | undefined>; notify?: (message: string, type?: string) => void };
}

function packageNames(cwd: string): string[] {
  const result: string[] = [];
  try {
    for (const name of readdirSync(cwd)) {
      const path = join(cwd, name, "package.json");
      if (!existsSync(path)) continue;
      try {
        const pkg = JSON.parse(readFileSync(path, "utf8"));
        if (typeof pkg.name === "string") result.push(`${pkg.name} (${name})`);
      } catch { /* ignore malformed unrelated package files */ }
    }
  } catch { /* workspace may be unavailable during early startup */ }
  return result.sort();
}

function extensionNames(cwd: string): string[] {
  try {
    return readdirSync(join(cwd, ".pi", "extensions"), { withFileTypes: true })
      .filter(entry => (entry.isFile() && entry.name.endsWith(".ts")) || entry.isDirectory())
      .map(entry => entry.name.replace(/\.ts$/, ""))
      .filter(name => name !== "system-inspector")
      .sort();
  } catch { return []; }
}

function toolNames(pi: any): string[] {
  try { return (pi.getAllTools?.() ?? []).map((tool: any) => tool.name).filter((name: any) => typeof name === "string").sort(); }
  catch { return []; }
}

function snapshot(pi: any, cwd: string, prompt: string): string {
  const tools = toolNames(pi);
  // Only Pi-local skills belong in this inspector; Swarm skills are explicit.
  const skills = new SkillLoader({ cwd, home: process.env.HOME, closed: true, cliPaths: [join(cwd, ".pi", "skills")] }).load();
  const extensions = extensionNames(cwd);
  const packages = packageNames(cwd);
  return [
    "PI-SWARM RUNTIME INSPECTOR",
    `Workspace: ${cwd}`,
    `Generated: ${new Date().toISOString()}`,
    "",
    `SYSTEM PROMPT (${prompt.length} characters)`,
    prompt || "(not captured yet; send a prompt or inspect after agent start)",
    "",
    `TOOLS (${tools.length})`,
    tools.join("\n") || "(none)",
    "",
    `SKILLS (${skills.skills.length})`,
    skills.skills.map(skill => `- ${skill.name} [${skill.source}] — ${skill.description || "(no description)"}`).join("\n") || "(none)",
    skills.diagnostics.length ? `\nSkill diagnostics:\n${skills.diagnostics.map(d => `- ${d.path}: ${d.message}`).join("\n")}` : "",
    "",
    `EXTENSIONS (${extensions.length})`,
    extensions.join("\n") || "(none)",
    "",
    `BUILT PACKAGES / CAPABILITIES (${packages.length})`,
    packages.join("\n") || "(none)",
  ].join("\n");
}

export function registerSystemInspector(pi: any): void {
  pi.registerEntryRenderer?.("pi-swarm-system-inspector", (entry: any) => {
    const lines = String(entry.data?.content ?? "").split("\\n");
    // Keep this renderer dependency-free: Pi accepts the small Component
    // contract, and this avoids importing Pi's private TUI package in tests.
    return {
      render(width: number): string[] {
        if (width <= 0) return lines;
        const wrapped: string[] = [];
        for (const line of lines) {
          if (!line) { wrapped.push(""); continue; }
          for (let offset = 0; offset < line.length; offset += width) wrapped.push(line.slice(offset, offset + width));
        }
        return wrapped;
      },
      invalidate() {},
    };
  });
  let currentPrompt = "";
  let published = false;
  let cwd = resolve(pi.getCwd?.() ?? process.cwd());
  const open = async (ctx?: InspectorContext) => {
    const text = snapshot(pi, resolve(ctx?.cwd ?? cwd), currentPrompt);
    if (ctx?.ui?.editor) await ctx.ui.editor("Pi-Swarm system · prompt, tools, skills, build", text);
    else ctx?.ui?.notify?.(`Pi-Swarm system inspected: ${toolNames(pi).length} tools, ${new SkillLoader({ cwd }).load().skills.length} skills`, "info");
  };
  pi.on?.("before_agent_start", (event: any, ctx: any) => {
    currentPrompt = typeof event?.systemPrompt === "string" ? event.systemPrompt : currentPrompt;
    cwd = resolve(ctx?.cwd ?? cwd);
    // This is the first lifecycle point with Pi's fully assembled prompt.
    // Emit it as a visible, non-turn entry so it is present in the transcript
    // when the built-in Ctrl+O startup/resources view is expanded.
    if (published) return;
    published = true;
    const text = snapshot(pi, cwd, currentPrompt);
    pi.appendEntry?.("pi-swarm-system-inspector", { content: text, cwd });
  });
  pi.on?.("session_start", (_event: any, ctx: any) => {
    cwd = resolve(ctx?.cwd ?? pi.getCwd?.() ?? process.cwd());
    published = false;
    const skills = new SkillLoader({ cwd, home: process.env.HOME, closed: true, cliPaths: [join(cwd, ".pi", "skills")] }).load();
    ctx?.ui?.notify?.(`Pi-Swarm ready · ${toolNames(pi).length} tools · ${skills.skills.length} Pi skills · Ctrl+O: Pi startup resources · /system: full prompt and inventory`, "info");
  });
  // Ctrl+O is owned by Pi's built-in `app.tools.expand` shortcut. Do not
  // register it here: Pi's expanded startup view already shows loaded skills,
  // extensions, themes, and tools. Use /system for the additional full prompt
  // and workspace-package inventory without colliding with that view.
  pi.registerCommand?.("system", { description: "Inspect the current prompt and all built runtime resources", handler: async (_args: string, ctx: any) => open(ctx) });
}

export default function systemInspectorExtension(pi: any): void { registerSystemInspector(pi); }
