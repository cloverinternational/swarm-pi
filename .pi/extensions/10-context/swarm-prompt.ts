import { createHash } from "node:crypto";
import { execFileSync } from "node:child_process";
import { existsSync, readFileSync, realpathSync, statSync } from "node:fs";
import { isAbsolute, relative, resolve } from "node:path";
import { registerHook } from "../../lib/runtime/hook-state.ts";
import { getSwarmSkillRegistry } from "../../lib/context/swarm-skill-registry.ts";
import type { LoadedSkill } from "../../../packages/context/skills/src/index.ts";
import { autogenMode } from "./autogenskills.ts";
import { BUILTIN_SOURCES, DEFAULT_MAX_EXPLICIT_FILE_BYTES, DEFAULT_MAX_EXPLICIT_FILE_LINES, DEFAULT_MAX_EXPLICIT_FILES_BYTES, buildContextBlock, candidateContextPath, discoverAgentsMdPaths, injectContextBlocks, injectSwarmContext } from "../../lib/context/swarm-context.ts";

import {
  UPSTREAM_SOURCE,
  forgeSwarmSystemPrompt,
  swarmForgeDelegationAddendum,
  swarmForgeSystemPrompt,
} from "../../../packages/context/prompt/src/index.ts";
import { resolveActiveSystemPrompt } from "./system-prompts.ts";
import { loadPromptContextConfig } from "../../lib/context/swarm-prompt-context-config.ts";

export { UPSTREAM_SOURCE, forgeSwarmSystemPrompt, swarmForgeSystemPrompt };

/**
 * Main-agent reporting contract. The delegation addendum's reporting rules
 * apply only to child workers; keep this contract in the main interactive
 * prompt so the root Pi agent receives it directly as well.
 */
export const MAIN_REPORTING_DIRECTIVE = `[REPORTING DIRECTIVE]

For every substantive final response, produce an evidence-based structured report:

1. Outcome — answer the user's question or state exactly what changed.
2. Evidence — list concrete observations supporting the answer, with file paths and line numbers, command results, tool outputs, or URLs where applicable.
3. Analysis — distinguish directly observed facts from interpretation; do not present inference as fact.
4. Verification — state what was checked, what passed or failed, and what remains unverified.
5. Limitations — explicitly report missing evidence, partial inspection, uncertainty, and blocked work.
6. Next steps — include only actionable next steps that follow from the evidence.

Use concise Markdown headings and bullets. Every material claim must have nearby supporting evidence. Never claim completion, success, provider availability, or behavior from an intention, description, compilation result, or agent report alone. If evidence is absent, say "not established" rather than guessing.

[/REPORTING DIRECTIVE]`;

/**
 * The assembled prompt carries no Pi-specific marker (Swarm's prompt has
 * none); an already-assembled prompt is recognised by the runtime workspace
 * block Swarm renders first (settings/workspace_context.go).
 */
const ASSEMBLED_PREFIX = "<system_information>\n<operating_system>";
const HEADLESS_BASE_PROMPT = "You are a helpful AI assistant.";
const assembledPromptKinds = new Map<string, "interactive" | "headless">();
const assembledKind = (prompt: string): "interactive" | "headless" | undefined => {
  const body = prompt.replace(/^(?:<available_skills>[\s\S]*?<\/available_skills>\n*)+/, "").trimStart();
  if (body.startsWith(ASSEMBLED_PREFIX)) return "interactive";
  if (body.startsWith(`${HEADLESS_BASE_PROMPT}\n\n## Skill System\n`)) return "headless";
  return undefined;
};

export interface PromptExtensionEvent {
  systemPrompt: string;
  prompt?: string;
  systemPromptOptions?: { cwd?: string };
  /** Optional Forge prompt controls supplied by the host/bridge. */
  swarmPrompt?: PromptAssemblyOptions;
}
export interface PromptExtensionContext { cwd?: string; }
export interface PromptProvenance { section: string; origin: string; ref: string; hash: string; bytes: number; }
export interface WorkspaceContext { cwd: string; os: string; shell: string; home: string; extensions: string; }
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
  /**
   * swarm-tui/internal/chat/swarm_flow_guidance.go: the interactive TUI
   * advertises the `swarm-flow` CLI when it is on PATH; headless `swarm -p`
   * never does (a worker must not spawn workers).
   */
  interactive?: boolean;
  /** Use the interactive Forge/delegation prompt without interactive-only tools. */
  headlessForge?: boolean;
  /** Override the PATH lookup (tests). */
  swarmFlowAvailable?: boolean;
  /**
   * Headless base (`swarm -p`): swarm-tui never wires the settings manager
   * in runHeadless, so the agent keeps sdk_integration_provider.go's
   * "You are a helpful AI assistant." plus the autogenskills guidance/index
   * sdk_integration.go appends at startup. Supplied by the extension from
   * the skill registry; tests may pass an explicit list.
   */
  autogenSkills?: LoadedSkill[];
  /** autogenskills CreationMode.IsEnabled() (mode != never). Defaults to the project setting. */
  autogenEnabled?: boolean;
  contextEnabled?: Partial<Record<string, boolean>>;
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

/** runtime.GOOS spelling for process.platform. */
const goos = (platform: NodeJS.Platform) => platform === "win32" ? "windows" : platform;

const MAX_EXTENSIONS = 15;
/**
 * settings/workspace_context.go fetchExtensions: `git ls-files` in cwd (5s
 * budget), per-extension counts sorted by count desc then name, rounded
 * percentages, top 15. Empty when git fails, is not a repo, or lists nothing.
 */
export function fetchWorkspaceExtensions(cwd: string): string {
  let out: string;
  try { out = execFileSync("git", ["ls-files"], { cwd, timeout: 5000, stdio: ["ignore", "pipe", "ignore"], encoding: "utf8" }); }
  catch { return ""; }
  if (out.length === 0) return "";
  const counts = new Map<string, number>();
  let total = 0;
  for (const raw of out.trim().split("\n")) {
    const line = raw.trim();
    if (line === "") continue;
    total++;
    const slash = Math.max(line.lastIndexOf("/"), line.lastIndexOf("\\"));
    const fname = slash >= 0 ? line.slice(slash + 1) : line;
    const dot = fname.lastIndexOf(".");
    const ext = dot > 0 ? fname.slice(dot + 1) : "(no ext)";
    counts.set(ext, (counts.get(ext) ?? 0) + 1);
  }
  if (total === 0) return "";
  let stats = [...counts].map(([ext, count]) => ({ ext, count, percentage: Math.trunc((count * 100) / total + 0.5) }));
  stats.sort((a, b) => b.count - a.count || (a.ext < b.ext ? -1 : a.ext > b.ext ? 1 : 0));
  const totalExtensions = stats.length;
  if (stats.length > MAX_EXTENSIONS) stats = stats.slice(0, MAX_EXTENSIONS);
  let block = `<workspace_extensions command="git ls-files" files="${total}" extensions="${totalExtensions}">\n`;
  for (const s of stats) block += ` - .${s.ext}: ${s.count} files (${s.percentage}%)\n`;
  if (totalExtensions > MAX_EXTENSIONS) block += `(showing top ${MAX_EXTENSIONS} of ${totalExtensions} extensions)\n`;
  return `${block}</workspace_extensions>\n`;
}

export function resolveWorkspace(cwd = process.cwd()): WorkspaceContext {
  const root = resolve(cwd);
  // os.Getwd / os.UserHomeDir / $SHELL with Go's "/bin/sh" fallback.
  return { cwd: root, os: goos(process.platform), shell: process.env.SHELL || "/bin/sh", home: process.env.HOME || "", extensions: fetchWorkspaceExtensions(root) };
}

/** settings/workspace_context.go RenderWorkspaceContext, byte for byte. */
export function renderWorkspaceContext(workspace: WorkspaceContext): string {
  return `<system_information>\n<operating_system>${workspace.os}</operating_system>\n<current_working_directory>${workspace.cwd}</current_working_directory>\n<default_shell>${workspace.shell}</default_shell>\n<home_directory>${workspace.home}</home_directory>\n${workspace.extensions}</system_information>`;
}

/** swarm_flow_guidance.go buildSwarmFlowGuidance. */
export const SWARM_FLOW_GUIDANCE = `<swarm_flow_capability>
You have \`swarm-flow\`, a CLI that runs parallel \`swarm -p\` workers in tmux over DISJOINT file sets.
Reach for it when a task cleanly splits into 2+ INDEPENDENT subtasks that touch NON-overlapping files
(e.g. package A vs package B): it parallelizes the work AND keeps each worker's verbose output out of
your own context. Do NOT use it when subtasks share files (they would corrupt each other) — do those
yourself; and never nest swarm-flow inside a swarm-flow worker.

Workflow:
  swarm-flow init <name>                      # scaffolds .swarmflow/<name>/ (CONTRACT.md + w1.txt w2.txt)
  # edit each wN.txt: line 1 is the ownership header, the rest is a self-contained task prompt
  #   # FILES: exact/path/one.go, exact/path/one_test.go
  # workers do NOT see this conversation — brief each one fully (repo path, files, task, a verify cmd)
  swarm-flow run <name> --workspace "$PWD" --integrate "<build/test cmd>"
  swarm-flow status <name> | logs <name> <wN> | attach <name>

swarm-flow refuses to launch if two workers claim the same file (disjointness is enforced). After it
returns, YOU run the authoritative integration build/test and reconcile any cross-package seams.
</swarm_flow_capability>`;

let swarmFlowPath: string | undefined;
/** swarm_flow_guidance.go swarmFlowAvailable: exec.LookPath("swarm-flow"), resolved once. */
export function swarmFlowAvailable(env: NodeJS.ProcessEnv = process.env): boolean {
  if (swarmFlowPath !== undefined) return swarmFlowPath !== "";
  swarmFlowPath = "";
  for (const dir of (env.PATH ?? "").split(":")) {
    if (dir === "") continue;
    const candidate = resolve(dir, "swarm-flow");
    try { if (existsSync(candidate)) { swarmFlowPath = candidate; break; } } catch { /* unreadable PATH entry */ }
  }
  return swarmFlowPath !== "";
}

function conventionalFiles(root: string, names: string[], max: number): ContextFile[] {
  return names.flatMap((name) => { const path = safeRef(root, name); if (!path || !existsSync(path)) return []; try { const content = readFileSync(path, "utf8"); return [{ path: name, content: content.slice(0, max) }]; } catch { return []; } });
}

/** Go bytewise string order (registry.List: priority desc, name asc). */
const goLess = (a: string, b: string) => Buffer.compare(Buffer.from(a), Buffer.from(b));
const registryOrder = (skills: readonly LoadedSkill[]) => [...skills].sort((a, b) => ((b.priority ?? 0) - (a.priority ?? 0)) || goLess(a.name, b.name));

/** autogenskills/guidance.go BuildSKILLSGuidance. */
export function buildSkillsGuidance(existingSkillNames: readonly string[]): string {
  let b = "## Skill System\n\n";
  b += "Skills are reusable instruction sets for recurring tasks. They follow the Hermes on-demand model: you see only a compact index (name + description) in the system prompt. Full instructions are loaded on demand by calling SkillManage(action=\"view\", name=\"...\"). Never guess what a skill does from its description alone — always view it.\n\n";
  b += "### When to check skills (BEFORE starting work)\n\n";
  b += "At the start of any task requiring 3 or more distinct steps, call SkillManage(action=\"list\") to check for relevant skills. If the index shows a plausible match, view it immediately and follow its instructions as the primary approach for this task. Do not begin tool calls before checking — skills encode patterns that prevent repeating expensive discovery work.\n\n";
  b += "### Consult multiple skills, not just one\n\n";
  b += "After finding one relevant skill, ask: does this task span more than one domain? If so, check for complementary skills too. Example: a task that requires both fixing Go code and updating a Linear issue should check for skills matching both domains. Read each relevant skill in full before deciding how to proceed. The index description is a hint — not a substitute for the full instructions.\n\n";
  b += "### SkillManage tool actions\n\n";
  b += "- `list`: Discover available skills with name, version, and description. Always check before starting a complex task.\n";
  b += "- `view`: Load the full SKILL.md body. When you view a skill, follow its instructions for the current task. If it needs updating, patch it before continuing.\n";
  b += "- `create`: Last resort for a genuinely new class-level workflow. Never create merely because a task took several tool calls.\n";
  b += "- `patch`: Update a skill immediately when you find it incomplete, outdated, or wrong. Do not wait to be asked. Stale skills cause repeated mistakes.\n\n";
  b += "- `write_file`: Preserve narrow session detail under an existing umbrella in references/, templates/, scripts/, or assets/.\n";
  b += "- `review`: Record that you completed the review and found nothing reusable. This is a valid outcome; never manufacture a skill to satisfy a budget.\n\n";
  b += "### Correct invocation pattern\n\n";
  b += "1. Task starts → call `list` to check for relevant skills.\n";
  b += "2. Relevant skill found → call `view` to read full instructions.\n";
  b += "3. Task spans multiple domains → `view` each relevant skill before proceeding.\n";
  b += "4. Skill is stale or incomplete → `patch` it immediately, then continue.\n";
  b += "5. No existing umbrella fits → create one only if the learning generalizes beyond today's task.\n";
  b += "6. No reusable learning → call `review` with a short reason and continue without mutation.\n\n";
  b += "Avoid: ignoring the index, assuming skill content from the description, relying on a single skill when multiple apply, or reading a skill without following it.\n";
  if (existingSkillNames.length > 0) {
    b += `\n**Current skill index (${existingSkillNames.length} skills):** ${existingSkillNames.join(", ")}\n`;
    b += "Use `view` before creating a new skill to avoid duplicates.\n";
  } else {
    b += "\nNo skills exist yet. Create your first skill after completing a complex multi-step task.\n";
  }
  return b;
}

const MAX_AUTOGEN_INDEX_SKILLS = 60;
const MAX_AUTOGEN_DESC_CHARS = 120;
/** guidance.go truncateSkillDesc: strings.Fields collapse, BYTE-length cap with "…". */
export function truncateSkillDesc(desc: string): string {
  const collapsed = desc.split(/[\s]+/u).filter(Boolean).join(" ").trim();
  const bytes = Buffer.from(collapsed, "utf8");
  if (bytes.length <= MAX_AUTOGEN_DESC_CHARS) return collapsed;
  return `${bytes.subarray(0, MAX_AUTOGEN_DESC_CHARS - 1).toString("utf8").replace(/\uFFFD+$/u, "").trim()}…`;
}

/** autogenskills/guidance.go BuildAutogenSkillIndex over registry.List() order. */
export function buildAutogenSkillIndex(allSkills: readonly LoadedSkill[]): string {
  const autogen = registryOrder(allSkills).filter((skill) => skill.source === "autogen");
  if (autogen.length === 0) return "";
  let b = "## Available Autogen Skills\n";
  b += "Call SkillManage(action=\"view\", name=\"...\") to load full instructions before using any skill.\n";
  let rendered = autogen;
  let omitted = 0;
  if (rendered.length > MAX_AUTOGEN_INDEX_SKILLS) { omitted = rendered.length - MAX_AUTOGEN_INDEX_SKILLS; rendered = rendered.slice(0, MAX_AUTOGEN_INDEX_SKILLS); }
  for (const skill of rendered) b += `- **${skill.name}** (v${skill.version || "?.?.?"}): ${truncateSkillDesc(skill.description)}\n`;
  if (omitted > 0) b += `- _(${omitted} more skill(s) omitted to bound prompt size; call SkillManage(action="list") for the full set)_\n`;
  return b;
}

/**
 * The agent prompt `swarm -p` starts from (sdk_integration.go startup with
 * no settings manager): the provider default, then the autogenskills
 * guidance and index appended with "\n\n" while the service is enabled.
 */
export function headlessBasePrompt(skills: readonly LoadedSkill[], autogenEnabled: boolean): string {
  let prompt = HEADLESS_BASE_PROMPT;
  if (!autogenEnabled) return prompt;
  const names = registryOrder(skills).filter((skill) => skill.source === "autogen").map((skill) => skill.name);
  const additions = [buildSkillsGuidance(names)];
  const index = buildAutogenSkillIndex(skills);
  if (index) additions.push(index);
  return `${prompt}\n\n${additions.join("\n\n")}`;
}

export const PROJECT_MEMORY_SOURCES = ["project_claude_md", "project_swarm_md", "agents_md", "index_md"] as const;
/**
 * The cached/ephemeral context blocks context_injecting_provider.go adds to
 * EVERY provider request (agent turns and the conversation-metadata call
 * alike), honouring the headless --no-project-memory ⇔ --no-context-files gate.
 */
export function currentContextBlocks(cwd: string, noContextFiles = cliIsolation().noContextFiles): { cached: string; ephemeral: string } {
  const root = resolve(cwd);
  const config = loadPromptContextConfig(root, undefined, { persistMigration: false });
  const enabled = noContextFiles
    ? Object.fromEntries(PROJECT_MEMORY_SOURCES.map((id) => [id, false]))
    : config.context?.enabledSources;
  const context = buildContextBlock({ workDir: root, enabled, files: noContextFiles ? undefined : config.context?.files });
  return { cached: context.cached, ephemeral: context.ephemeral };
}

/**
 * The system prompt as the Swarm agent holds it, before per-request context
 * injection. Interactive TUI (app_init.go applies the settings prompt):
 *   base     = RenderWorkspaceContext() + "\n\n" + SwarmForge content
 *              (settings/system_prompt.go GetActivePromptWithOAuth)
 * Headless `swarm -p` (runHeadless never wires the settings manager):
 *   base     = "You are a helpful AI assistant." + autogenskills guidance/index
 * Then, on both paths:
 *   startup  = InjectContext(base, loadedCtx.FormatAsXML())
 *              (sdk_integration_config.go LoadAndInjectContext; the tagless
 *              block lands in one <swarmos_context> element)
 *   + "\n\n" + swarm-flow guidance   (interactive TUI with swarm-flow on PATH)
 * Every request then runs InjectContextBlocks again
 * (context_injecting_provider.go), which strips the startup block but keeps
 * its separators — so with guidance the model sees four newlines between the
 * Forge text and <swarm_flow_capability>. Reproduced literally.
 */
export function assembleForgePrompt(_base: string, options: PromptAssemblyOptions = {}): PromptAssembly {
  const workspace = resolveWorkspace(options.cwd);
  const root = workspace.cwd;
  const max = options.maxContextFileBytes ?? DEFAULT_MAX_EXPLICIT_FILE_BYTES;
  const provenance: PromptProvenance[] = [];
  const sections: string[] = [];
  const add = (section: string, content: string, origin: string, ref = "", raw = false) => { const value = raw ? content : clean(content); if (!value) return; sections.push(value); provenance.push({ section, origin, ref, hash: hash(value), bytes: Buffer.byteLength(value) }); };
  if (options.interactive || options.headlessForge === true) {
    add("workspace", renderWorkspaceContext(workspace), "runtime", "workspace", true);
    // Keep Forge's upstream body and delegation guidance intact, but place the
    // root-agent reporting contract between them. Child-worker reporting rules
    // must not be the only source of structured final-output behavior.
    add("forge", forgeSwarmSystemPrompt, "forge", `${UPSTREAM_SOURCE}#forgeSwarmSystemPrompt`, true);
    add("reporting", MAIN_REPORTING_DIRECTIVE, "runtime", "pi-swarm-main-reporting-directive", true);
    add("delegation", swarmForgeDelegationAddendum, "forge", `${UPSTREAM_SOURCE}#swarmForgeDelegationAddendum`, true);
  } else {
    add("headless", headlessBasePrompt(options.autogenSkills ?? [], options.autogenEnabled ?? autogenMode(root) !== "never"), "runtime", "sdk_integration_provider.go+autogenskills/guidance.go", true);
  }
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
  const enabled = options.discoverContextFiles === false ? Object.fromEntries(PROJECT_MEMORY_SOURCES.map((id) => [id, false])) : options.contextEnabled;
  const context = buildContextBlock({
    workDir: root,
    enabled,
    files: options.contextFiles,
    maxExplicitFileBytes: max,
    maxExplicitFileLines: DEFAULT_MAX_EXPLICIT_FILE_LINES,
    maxExplicitFilesBytes: DEFAULT_MAX_EXPLICIT_FILES_BYTES,
  });
  const contextPaths = [
    ...BUILTIN_SOURCES.filter((source) => (enabled?.[source.id] ?? source.enabled)).flatMap((source) => source.id === "agents_md" ? discoverAgentsMdPaths(root) : [candidateContextPath(source.id, root)]),
    ...(options.contextFiles ?? []).flatMap((path) => {
      const lexical = resolve(root, path);
      const rel = relative(root, lexical);
      if (rel === ".." || rel.startsWith(`..${"/"}`) || isAbsolute(rel)) return [];
      try {
        if (!statSync(lexical).isFile()) return [];
        const realRoot = realpathSync(root);
        const realFile = realpathSync(lexical);
        const realRel = relative(realRoot, realFile);
        return realRel === ".." || realRel.startsWith(`..${"/"}`) || isAbsolute(realRel) ? [] : [realFile];
      } catch { return []; }
    }),
  ].filter((path): path is string => Boolean(path));
  const base = sections.join("\n\n");
  // Startup injection (LoadAndInjectContext): FormatAsXML carries no tags, so
  // SplitContextBlock files everything as ONE ephemeral <swarmos_context>
  // element. It is stripped again per request; only its separators survive.
  const startupText = [context.cached, context.ephemeral].filter((part) => part.trim() !== "").join("\n");
  let agentPrompt = injectContextBlocks(base, "", startupText);
  const guidance = options.interactive && (options.swarmFlowAvailable ?? swarmFlowAvailable());
  if (guidance) {
    agentPrompt = `${agentPrompt}\n\n${SWARM_FLOW_GUIDANCE}`;
    provenance.push({ section: "swarm-flow", origin: "runtime", ref: "swarm_flow_guidance.go", hash: hash(SWARM_FLOW_GUIDANCE), bytes: Buffer.byteLength(SWARM_FLOW_GUIDANCE) });
  }
  // Per-request injection (context_injecting_provider.go applyContext).
  const prompt = injectContextBlocks(agentPrompt, context.cached, context.ephemeral);
  if (context.block) provenance.push({ section: "context", origin: "swarm-context", ref: contextPaths.join(","), hash: hash(context.block), bytes: Buffer.byteLength(context.block) });
  return { prompt, hash: hash(prompt), workspace, provenance, contextFiles: contextPaths };
}

export function comparePromptGolden(actual: PromptAssembly | string, golden: string): { equal: boolean; actualHash: string; goldenHash: string } {
  const actualText = typeof actual === "string" ? actual : actual.prompt;
  return { equal: actualText === golden, actualHash: hash(actualText), goldenHash: hash(golden) };
}

export interface PromptExtensionAPI { on(event: "before_agent_start", handler: (event: PromptExtensionEvent, ctx: PromptExtensionContext & { hasUI?: boolean }) => unknown): void; }
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

import { memorySystemPrompt } from "../../lib/context/memory-ceremony.ts";

export function registerSwarmPrompt(pi: PromptExtensionAPI): void {
  if (promptRegistrations.has(pi as object)) return;
  promptRegistrations.add(pi as object);
  registerHook(pi, "swarm-prompt", "before_agent_start", (event: PromptExtensionEvent, ctx: PromptExtensionContext) => {
    const cwd = ctx.cwd ?? event.systemPromptOptions?.cwd ?? process.cwd();
    const assemble = () => {
    const interactive = (ctx as { hasUI?: boolean }).hasUI === true;
    const isolation = cliIsolation();
    const config = loadPromptContextConfig(cwd, undefined, { persistMigration: false });
    const selectedSkills = config.skills?.mode === "allowlist" ? config.skills.names : undefined;
    const catalogFor = () => (isolation.noSkills ? "" : getSwarmSkillRegistry(pi as any, { cwd }).catalog("", selectedSkills));
    if (isolation.systemPrompt !== undefined) {
      // Swarm keeps an explicit --system-prompt as the base and still applies
      // skills (prepend, no separator) and context injection on top of it.
      const enabled = isolation.noContextFiles ? Object.fromEntries(PROJECT_MEMORY_SOURCES.map((id) => [id, false])) : undefined;
      const base = injectSwarmContext(isolation.systemPrompt, { workDir: cwd, enabled, files: config.context?.files });
      return { systemPrompt: withSkillCatalog(base, catalogFor()) };
    }
    const selected = resolveActiveSystemPrompt(cwd);
    const catalog = catalogFor();
    const knownKind = assembledPromptKinds.get(hash(event.systemPrompt));
    if (knownKind === (interactive ? "interactive" : "headless")) return undefined;
    const forgePrompt = event.systemPrompt.includes("# Delegation (the Task tool)") && event.systemPrompt.includes("<system_information>");
    // The final prompt is passed back through memory/context assembly, so its
    // hash is not necessarily the intermediate hash recorded below.  Use the
    // mode marker as the stable idempotence check; a headless Forge prompt
    // must still be upgraded when the real session is interactive.
    const promptModeMatches = forgePrompt && interactive === event.systemPrompt.includes("<swarm_flow_capability>");
    if (promptModeMatches) return undefined;
    const selectedBase = selected.kind === "pi" ? event.systemPrompt : selected.kind === "custom" ? selected.content : undefined;
    if (selectedBase !== undefined) {
      const enabled = isolation.noContextFiles ? Object.fromEntries(PROJECT_MEMORY_SOURCES.map((id) => [id, false])) : config.context?.enabledSources;
      const base = injectSwarmContext(selectedBase, { workDir: cwd, enabled, files: isolation.noContextFiles ? undefined : config.context?.files });
      return { systemPrompt: withSkillCatalog(base, catalog) };
    }
    // Already assembled by another prompt layer: keep its content intact.
    // Re-running catalog assembly here is not idempotent because the skill
    // registry can change between lifecycle passes; more importantly, it can
    // turn an interactive Forge prompt into a different prompt after the
    // first successful assembly. The first assembly is the authoritative
    // system prompt for this turn.
    const alreadyAssembled = assembledKind(event.systemPrompt);
    if (alreadyAssembled && ((interactive && alreadyAssembled === "interactive") || (!interactive && alreadyAssembled === "headless")) && !forgePrompt) {
      return undefined;
    }
    const assembly = assembleForgePrompt(event.systemPrompt, { cwd, interactive, headlessForge: !interactive, discoverContextFiles: !isolation.noContextFiles, contextEnabled: config.context?.enabledSources, contextFiles: config.context?.files, autogenSkills: interactive ? [] : getSwarmSkillRegistry(pi as any, { cwd }).list(), ...event.swarmPrompt });
    const systemPrompt = withSkillCatalog(assembly.prompt, catalog);
    assembledPromptKinds.set(hash(systemPrompt), interactive ? "interactive" : "headless");
    return { systemPrompt };
    };
    const assembled = assemble();
    const base = assembled?.systemPrompt ?? event.systemPrompt;
    const final = memorySystemPrompt(base, cwd);
    if (assembled) assembledPromptKinds.set(hash(final), (ctx as { hasUI?: boolean }).hasUI === true ? "interactive" : "headless");
    return assembled || final !== base ? { systemPrompt: final } : undefined;
  });
}
export default function swarmPromptExtension(pi: PromptExtensionAPI): void { registerSwarmPrompt(pi); }
