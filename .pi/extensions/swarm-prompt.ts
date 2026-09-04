import { createHash } from "node:crypto";
import { existsSync, readFileSync, readdirSync, statSync } from "node:fs";
import { extname, isAbsolute, join, relative, resolve } from "node:path";

const promptSource = new URL("../../upstream/swarm-sdk/swarm-tui/docs/FORGE_SWARM_SYSTEM_PROMPT.md", import.meta.url);
const promptDocument = readFileSync(promptSource, "utf8");
const promptMatch = promptDocument.match(/```text\n([\s\S]*?)\n```/);
if (!promptMatch) throw new Error("Forge system prompt document does not contain a text block");
export const forgeSwarmSystemPrompt = promptMatch[1];
const forgeMarker = "## Core Principles:";

export interface PromptExtensionEvent {
  systemPrompt: string;
  systemPromptOptions?: { cwd?: string };
  /** Optional Forge prompt controls supplied by the host/bridge. */
  swarmPrompt?: PromptAssemblyOptions;
}
export interface PromptProvenance { section: string; origin: string; ref: string; hash: string; bytes: number; }
export interface WorkspaceContext { cwd: string; os: string; shell: string; home: string; extensions: string[]; }
export interface ContextFile { path: string; content: string; }
export interface PromptAssemblyOptions {
  cwd?: string;
  userPrompt?: string;
  tools?: Array<string | { name: string; guidance: string }>;
  toolGuidance?: Record<string, string>;
  skills?: Array<{ name: string; instructions: string; version?: string }>;
  contextFiles?: string[];
  contextFileNames?: string[];
  restrictions?: string[];
  /** Explicitly opt out of convention-file discovery. */
  discoverContextFiles?: boolean;
  maxContextFileBytes?: number;
}
export interface PromptAssembly {
  prompt: string;
  hash: string;
  workspace: WorkspaceContext;
  provenance: PromptProvenance[];
  contextFiles: string[];
}

const hash = (value: string) => `sha256:${createHash("sha256").update(value).digest("hex")}`;
const clean = (value: string) => value.replace(/\r\n/g, "\n").trim();
const contained = (root: string, candidate: string) => {
  const r = relative(root, candidate);
  return r === "" || (r !== ".." && !r.startsWith(`..${"/"}`) && !isAbsolute(r));
};
const safeRef = (root: string, path: string) => {
  const absolute = resolve(root, path);
  return contained(root, absolute) ? absolute : undefined;
};

export function resolveWorkspace(cwd = process.cwd()): WorkspaceContext {
  const root = resolve(cwd);
  const extensions = new Set<string>();
  const walk = (dir: string, depth: number) => {
    if (depth > 2 || !existsSync(dir)) return;
    for (const entry of readdirSync(dir)) {
      const path = join(dir, entry);
      if (entry.startsWith(".") && entry !== ".env") continue;
      try { statSync(path).isDirectory() ? walk(path, depth + 1) : extensions.add(extname(entry).slice(1)); } catch { /* race or unreadable */ }
    }
  };
  walk(root, 0);
  return { cwd: root, os: process.platform, shell: process.env.SHELL || "unknown", home: process.env.HOME || "unknown", extensions: [...extensions].filter(Boolean).sort() };
}

function workspaceBlock(workspace: WorkspaceContext): string {
  // Keep the human-readable legacy line while retaining the structured Forge
  // fields used by newer consumers.
  return [`Current working directory: ${workspace.cwd}`, "<system_information>", `<operating_system>${workspace.os}</operating_system>`, `<current_working_directory>${workspace.cwd}</current_working_directory>`, `<default_shell>${workspace.shell}</default_shell>`, `<home_directory>${workspace.home}</home_directory>`, `<workspace_extensions>${workspace.extensions.join(", ")}</workspace_extensions>`, "</system_information>"].join("\n");
}
function conventionalFiles(root: string, names: string[], max: number): ContextFile[] {
  return names.flatMap((name) => { const path = safeRef(root, name); if (!path || !existsSync(path)) return []; try { const content = readFileSync(path, "utf8"); return [{ path: name, content: content.slice(0, max) }]; } catch { return []; } });
}

export function assembleForgePrompt(base: string, options: PromptAssemblyOptions = {}): PromptAssembly {
  const workspace = resolveWorkspace(options.cwd);
  const root = workspace.cwd;
  const max = options.maxContextFileBytes ?? 128 * 1024;
  const provenance: PromptProvenance[] = [];
  const sections: string[] = [];
  const add = (section: string, content: string, origin: string, ref = "") => { const value = clean(content); if (!value) return; sections.push(value); provenance.push({ section, origin, ref, hash: hash(value), bytes: Buffer.byteLength(value) }); };
  add("base", base, "host");
  add("workspace", workspaceBlock(workspace), "runtime", "workspace");
  add("forge", forgeSwarmSystemPrompt, "forge", "upstream/swarm-sdk/swarm-tui/docs/FORGE_SWARM_SYSTEM_PROMPT.md");
  add("user", options.userPrompt || "", "configuration", "userPrompt");

  const tools = (options.tools || []).map((tool) => typeof tool === "string" ? { name: tool, guidance: options.toolGuidance?.[tool] || "" } : tool).filter((tool) => tool.guidance);
  add("tools", tools.map((tool) => `### ${tool.name}\n${tool.guidance}`).join("\n\n"), "configuration", "tools");
  add("skills", (options.skills || []).map((skill) => `### ${skill.name}${skill.version ? ` (v${skill.version})` : ""}\n${skill.instructions}`).join("\n\n"), "skill", "skills");

  const names = options.contextFileNames || ["AGENTS.md", "SWARM.md"];
  const files = [...(options.contextFiles || []).flatMap((name) => conventionalFiles(root, [name], max)), ...(options.discoverContextFiles === false ? [] : conventionalFiles(root, names, max))];
  const unique = [...new Map(files.map((file) => [file.path, file])).values()].sort((a, b) => a.path.localeCompare(b.path));
  add("context", unique.map((file) => `<context_file path="${file.path}">\n${file.content}\n</context_file>`).join("\n\n"), "context-file", unique.map((file) => file.path).join(","));
  add("restrictions", (options.restrictions || []).map((r) => `- ${r}`).join("\n"), "policy", "restrictions");
  const prompt = sections.join("\n\n");
  return { prompt, hash: hash(prompt), workspace, provenance, contextFiles: unique.map((file) => file.path) };
}

export function comparePromptGolden(actual: PromptAssembly | string, golden: string): { equal: boolean; actualHash: string; goldenHash: string } {
  const actualText = typeof actual === "string" ? actual : actual.prompt;
  return { equal: actualText === golden, actualHash: hash(actualText), goldenHash: hash(golden) };
}

export interface PromptExtensionAPI { on(event: "before_agent_start", handler: (event: PromptExtensionEvent, ctx: unknown) => unknown): void; }
export function registerSwarmPrompt(pi: PromptExtensionAPI): void {
  pi.on("before_agent_start", (event) => {
    if (event.systemPrompt.includes(forgeMarker)) return;
    const assembly = assembleForgePrompt(event.systemPrompt, { cwd: event.systemPromptOptions?.cwd, ...event.swarmPrompt });
    // before_agent_start only accepts systemPrompt/message. Keep provenance
    // internal to the assembly result instead of returning fields Pi ignores.
    return { systemPrompt: assembly.prompt };
  });
}
export default function swarmPromptExtension(pi: PromptExtensionAPI): void { registerSwarmPrompt(pi); }
