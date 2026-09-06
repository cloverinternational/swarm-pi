import { copyFileSync, existsSync, mkdirSync, readFileSync, renameSync, writeFileSync } from "node:fs";
import { dirname, isAbsolute, join, relative, resolve } from "node:path";

export interface PromptProfile { name: string; content: string }
export interface PromptContextConfig {
  version: 1;
  prompts: PromptProfile[];
  activePrompt?: string;
  context?: {
    enabledSources?: Record<string, boolean>;
    files?: string[];
  };
  skills?: { mode: "default" | "allowlist"; names: string[] };
  tools?: { mode: "default" | "allowlist"; names: string[] };
}

export type SelectionConfig = { mode: "default" | "allowlist"; names: string[] };

export const CONFIG_FILE = ".pi/prompt-context.json";
export const LEGACY_PROMPT_FILE = ".pi/system-prompts.json";

const clean = (value: string) => value.replace(/\r\n/g, "\n").trim();
const unique = (values: unknown[]) => [...new Set(values.filter((v): v is string => typeof v === "string" && v.trim() !== "").map(v => v.trim()))];

/** Apply persisted selection without making default mode destructive. */
export function effectiveSelection<T extends string>(available: readonly T[], selection?: SelectionConfig): T[] {
  return selection?.mode === "allowlist" ? available.filter(name => selection.names.includes(name)) : [...available];
}

export function selectionAllows(name: string, selection?: SelectionConfig): boolean {
  return selection?.mode !== "allowlist" || selection.names.includes(name);
}

export function promptContextConfigPath(cwd: string): string { return join(resolve(cwd), CONFIG_FILE); }
export function legacyPromptPath(cwd: string): string { return join(resolve(cwd), LEGACY_PROMPT_FILE); }

export function defaultPromptContextConfig(): PromptContextConfig {
  return { version: 1, prompts: [] };
}

function validProfile(value: unknown): value is PromptProfile {
  return !!value && typeof value === "object" && typeof (value as any).name === "string" && clean((value as any).name) !== "" && typeof (value as any).content === "string" && clean((value as any).content) !== "";
}

function normalize(raw: unknown): PromptContextConfig {
  const value = raw && typeof raw === "object" ? raw as any : {};
  const profiles = Array.isArray(value.prompts) ? value.prompts.filter(validProfile).map(p => ({ name: clean(p.name), content: clean(p.content) })) : [];
  const enabledSources = value.context?.enabledSources && typeof value.context.enabledSources === "object"
    ? Object.fromEntries(Object.entries(value.context.enabledSources).filter(([k, v]) => /^[a-z0-9_]+$/.test(k) && typeof v === "boolean")) as Record<string, boolean>
    : undefined;
  const files = Array.isArray(value.context?.files) ? unique(value.context.files) : undefined;
  const skills = value.skills?.mode === "allowlist" || value.skills?.mode === "default"
    ? { mode: value.skills.mode, names: unique(Array.isArray(value.skills.names) ? value.skills.names : []) } : undefined;
  const tools = value.tools?.mode === "allowlist" || value.tools?.mode === "default"
    ? { mode: value.tools.mode, names: unique(Array.isArray(value.tools.names) ? value.tools.names : []) } : undefined;
  const activePrompt = typeof value.activePrompt === "string" && clean(value.activePrompt) !== "" ? clean(value.activePrompt) : undefined;
  return { version: 1, prompts: profiles, ...(activePrompt ? { activePrompt } : {}), ...(enabledSources || files ? { context: { ...(enabledSources ? { enabledSources } : {}), ...(files ? { files } : {}) } } : {}), ...(skills ? { skills } : {}), ...(tools ? { tools } : {}) };
}

function readJSON(path: string): unknown | undefined {
  try { return JSON.parse(readFileSync(path, "utf8")); } catch { return undefined; }
}

function atomicWrite(path: string, value: unknown): void {
  mkdirSync(dirname(path), { recursive: true });
  const temp = `${path}.tmp-${process.pid}-${Date.now()}`;
  writeFileSync(temp, `${JSON.stringify(value, null, 2)}\n`, "utf8");
  renameSync(temp, path);
}

function legacyConfig(cwd: string): PromptContextConfig | undefined {
  const raw = readJSON(legacyPromptPath(cwd));
  if (!raw || typeof raw !== "object") return undefined;
  const value = raw as any;
  // Legacy stores with an omitted `active` field represented an explicit Pi
  // selection; preserve that historical migration behavior.
  return normalize({ prompts: value.prompts, activePrompt: typeof value.active === "string" ? value.active : "Pi default / current base" });
}

/** Load, migrate, and normalize the workspace configuration without deleting legacy state. */
export function loadPromptContextConfig(cwd: string, onRepair?: (message: string) => void, options: { persistMigration?: boolean } = {}): PromptContextConfig {
  const path = promptContextConfigPath(cwd);
  const hadConfig = existsSync(path);
  const parsed = hadConfig ? readJSON(path) : undefined;
  let config = hadConfig ? normalize(parsed) : defaultPromptContextConfig();
  if (!hadConfig) {
    const legacy = legacyConfig(cwd);
    if (legacy) {
      config = legacy;
      if (options.persistMigration !== false) atomicWrite(path, config);
      onRepair?.("Migrated .pi/system-prompts.json to .pi/prompt-context.json.");
    }
  } else if (parsed === undefined || (parsed as any)?.version !== 1) {
    const backup = `${path}.recovery-${new Date().toISOString().replace(/[:.]/g, "-")}`;
    try { copyFileSync(path, backup); } catch { /* preserve best effort */ }
    atomicWrite(path, config);
    onRepair?.("Repaired invalid .pi/prompt-context.json; valid fields were preserved.");
  }
  // New configuration wins conflicts but imports missing legacy profiles.
  const legacy = legacyConfig(cwd);
  if (legacy) {
    const names = new Set(config.prompts.map(p => p.name));
    const missing = legacy.prompts.filter(p => !names.has(p.name));
    if (missing.length) { config = { ...config, prompts: [...config.prompts, ...missing] }; if (options.persistMigration !== false) atomicWrite(path, config); }
  }
  return config;
}

export function savePromptContextConfig(cwd: string, config: PromptContextConfig): void {
  atomicWrite(promptContextConfigPath(cwd), normalize(config));
}

export function pathInsideWorkspace(cwd: string, candidate: string): string | undefined {
  const root = resolve(cwd);
  const absolute = resolve(root, candidate);
  const rel = relative(root, absolute);
  if (rel === ".." || rel.startsWith(`..${"/"}`) || isAbsolute(rel)) return undefined;
  return absolute;
}
