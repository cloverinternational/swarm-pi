import { createHash } from "node:crypto";
import { existsSync, readFileSync, readdirSync, statSync } from "node:fs";
import { extname, isAbsolute, join, relative, resolve } from "node:path";
import { registerHook } from "../hook-state.ts";
import { getSwarmSkillRegistry } from "../lib/swarm-skill-registry.ts";
import { BUILTIN_SOURCES, buildContextBlock, candidateContextPath, discoverAgentsMdPaths, injectSwarmContext } from "../lib/swarm-context.ts";

import {
  UPSTREAM_SOURCE,
  forgeSwarmSystemPrompt,
  swarmForgeSystemPrompt,
} from "../../swarm-prompt/src/index.ts";
import { resolveActiveSystemPrompt } from "./system-prompts.ts";

export { UPSTREAM_SOURCE, forgeSwarmSystemPrompt, swarmForgeSystemPrompt };

const forgeMarker = "<!-- pi-swarm:forge-prompt:v1 -->";

export interface PromptExtensionEvent {
  systemPrompt: string;
  prompt?: string;
  systemPromptOptions?: { cwd?: string };
  /** Optional Forge prompt controls supplied by the host/bridge. */
  swarmPrompt?: PromptAssemblyOptions;
}
export interface PromptExtensionContext { cwd?: string; }
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

const PROJECT_MEMORY_SOURCES = ["project_claude_md", "project_swarm_md", "agents_md", "index_md"] as const;

export function assembleForgePrompt(_base: string, options: PromptAssemblyOptions = {}): PromptAssembly {
  const workspace = resolveWorkspace(options.cwd);
  const root = workspace.cwd;
  const max = options.maxContextFileBytes ?? 128 * 1024;
  const provenance: PromptProvenance[] = [];
  const sections: string[] = [];
  const add = (section: string, content: string, origin: string, ref = "") => { const value = clean(content); if (!value) return; sections.push(value); provenance.push({ section, origin, ref, hash: hash(value), bytes: Buffer.byteLength(value) }); };
  add("workspace", workspaceBlock(workspace), "runtime", "workspace");
  add("forge", `${forgeMarker}\n${swarmForgeSystemPrompt}`, "forge", `${UPSTREAM_SOURCE}#swarmForgeSystemPrompt`);
  add("user", options.userPrompt || "", "configuration", "userPrompt");

  const tools = (options.tools || []).map((tool) => typeof tool === "string" ? { name: tool, guidance: options.toolGuidance?.[tool] || "" } : tool).filter((tool) => tool.guidance);
  add("tools", tools.map((tool) => `### ${tool.name}\n${tool.guidance}`).join("\n\n"), "configuration", "tools");
  add("skills", (options.skills || []).map((skill) => `### ${skill.name}${skill.version ? ` (v${skill.version})` : ""}\n${skill.instructions}`).join("\n\n"), "skill", "skills");

  add("restrictions", (options.restrictions || []).map((r) => `- ${r}`).join("\n"), "policy", "restrictions");
  // Swarm TUI appends its context orchestrator output (AGENTS.md / SWARM.md /
  // CLAUDE.md, projectName, gitStatus, currentDate) as tagged
  // <swarmos_cached_context> / <swarmos_context> blocks after the base prompt,
  // joined by "\n\n" (injection.go InjectContextBlocks). Mirror it exactly so
  // the bytes crossing the model boundary match `swarm -p`.
  // discoverContextFiles=false ⇔ Swarm --no-project-memory: only the file
  // sources are excluded; projectName/gitStatus/currentDate still inject.
  const enabled = options.discoverContextFiles === false ? Object.fromEntries(PROJECT_MEMORY_SOURCES.map((id) => [id, false])) : undefined;
  const context = buildContextBlock({ workDir: root, enabled });
  const contextPaths = BUILTIN_SOURCES.filter((source) => (enabled?.[source.id] ?? source.enabled)).flatMap((source) => source.id === "agents_md" ? discoverAgentsMdPaths(root) : [candidateContextPath(source.id, root)]).filter((path): path is string => Boolean(path));
  add("context", context.block, "swarm-context", contextPaths.join(","));
  const prompt = sections.join("\n\n");
  return { prompt, hash: hash(prompt), workspace, provenance, contextFiles: contextPaths };
}

export function comparePromptGolden(actual: PromptAssembly | string, golden: string): { equal: boolean; actualHash: string; goldenHash: string } {
  const actualText = typeof actual === "string" ? actual : actual.prompt;
  return { equal: actualText === golden, actualHash: hash(actualText), goldenHash: hash(golden) };
}

export interface PromptExtensionAPI { on(event: "before_agent_start", handler: (event: PromptExtensionEvent, ctx: PromptExtensionContext) => unknown): void; }
const promptRegistrations = new WeakSet<object>();
// Swarm TUI (sdk_integration_skills.go InjectSkillsContext) prepends the
// <available_skills> XML to the system prompt with NO separator and ranks it
// with an empty query. Swarm's own source flags the missing boundary as a
// known defect; we reproduce it verbatim because the goal is byte parity at
// the model boundary. Fix it upstream first, then here.
const withSkillCatalog = (prompt: string, catalog: string) => {
  const withoutCatalog = prompt
    .replace(/^(?:<available_skills>[\s\S]*?<\/available_skills>\n*)+/, "")
    .replace(/\n*<available_skills>[\s\S]*?<\/available_skills>\n*/g, "\n\n")
    .trim();
  return catalog ? `${catalog}${withoutCatalog}` : withoutCatalog;
};
/**
 * Pi CLI isolation flags mapped onto Swarm's headless isolation
 * (swarm-tui/cmd/swarmos/main.go resolveHeadlessIsolation +
 * sdk_integration_config.go applyContextExclusions):
 *   --system-prompt X    ⇔ --system-prompt X   (explicit base; no Forge/default)
 *   --no-context-files   ⇔ --no-project-memory (drop claudeMd/swarmMd/agentsMd/indexMd)
 *   --no-skills          ⇔ --no-skills         (no <available_skills> block)
 */
export function cliIsolation(argv: readonly string[] = process.argv): { systemPrompt?: string; noContextFiles: boolean; noSkills: boolean } {
  let systemPrompt: string | undefined;
  for (let i = 0; i < argv.length; i++) {
    const arg = argv[i];
    if (arg === "--system-prompt" && i + 1 < argv.length) systemPrompt = argv[i + 1];
    else if (arg.startsWith("--system-prompt=")) systemPrompt = arg.slice("--system-prompt=".length);
  }
  return { systemPrompt, noContextFiles: argv.includes("--no-context-files"), noSkills: argv.includes("--no-skills") };
}

export function registerSwarmPrompt(pi: PromptExtensionAPI): void {
  if (promptRegistrations.has(pi as object)) return;
  promptRegistrations.add(pi as object);
  registerHook(pi, "swarm-prompt", "before_agent_start", (event: PromptExtensionEvent, ctx: PromptExtensionContext) => {
    const cwd = ctx.cwd ?? event.systemPromptOptions?.cwd ?? process.cwd();
    const isolation = cliIsolation();
    const catalogFor = () => (isolation.noSkills ? "" : getSwarmSkillRegistry(pi as any, { cwd }).catalog(""));
    if (isolation.systemPrompt !== undefined) {
      // Swarm keeps an explicit --system-prompt as the base and still applies
      // skills (prepend, no separator) and context injection on top of it.
      const enabled = isolation.noContextFiles ? Object.fromEntries(PROJECT_MEMORY_SOURCES.map((id) => [id, false])) : undefined;
      const base = injectSwarmContext(isolation.systemPrompt, { workDir: cwd, enabled });
      return { systemPrompt: withSkillCatalog(base, catalogFor()) };
    }
    const selected = resolveActiveSystemPrompt(cwd);
    // Pi-native and explicitly custom prompts own their exact system-prompt
    // contents. Leave them untouched; their respective canonical skill loaders
    // remain responsible for making skills available to the model.
    if (selected.kind === "pi") return;
    if (selected.kind === "custom") return { systemPrompt: selected.content };
    const catalog = catalogFor();
    // Forge may already have been assembled by another prompt layer. Keep its
    // content intact and replace any stale/native partial catalogue.
    if (event.systemPrompt.includes(forgeMarker)) {
      const next = withSkillCatalog(event.systemPrompt, catalog);
      return next === event.systemPrompt ? undefined : { systemPrompt: next };
    }
    const assembly = assembleForgePrompt(event.systemPrompt, { cwd, discoverContextFiles: !isolation.noContextFiles, ...event.swarmPrompt });
    return { systemPrompt: withSkillCatalog(assembly.prompt, catalog) };
  });
}
export default function swarmPromptExtension(pi: PromptExtensionAPI): void { registerSwarmPrompt(pi); }
