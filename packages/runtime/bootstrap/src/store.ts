import { execFileSync } from "node:child_process";
import { mkdirSync, readFileSync, renameSync, writeFileSync } from "node:fs";
import { dirname, join, resolve } from "node:path";
import type { BootstrapMode, BootstrapSettings } from "./types.js";

export const SETTINGS_FILE = "bootstrap-settings.json";
const MODES: readonly BootstrapMode[] = ["parallel", "combined", "off"];

function commonDir(cwd: string): string | undefined {
  try {
    return resolve(cwd, execFileSync("git", ["-C", cwd, "rev-parse", "--git-common-dir"], { encoding: "utf8", stdio: ["ignore", "pipe", "ignore"] }).trim());
  } catch { return undefined; }
}

export function settingsPath(cwd: string): string {
  const root = commonDir(cwd);
  return root ? join(root, "swarm-bootstrap", SETTINGS_FILE) : join(resolve(cwd), ".swarm", SETTINGS_FILE);
}

export function readBootstrapSettings(cwd: string): BootstrapSettings {
  try {
    const value: unknown = JSON.parse(readFileSync(settingsPath(cwd), "utf8"));
    if (typeof value === "object" && value !== null && MODES.includes((value as { mode?: BootstrapMode }).mode as BootstrapMode)) {
      const parsed = value as { mode: BootstrapMode; updatedAt?: unknown; model?: unknown };
      return { mode: parsed.mode, version: 1, updatedAt: String(parsed.updatedAt ?? ""), ...(typeof parsed.model === "string" && parsed.model.trim() ? { model: parsed.model.trim() } : {}) };
    }
  } catch { /* Missing or malformed settings use the safe default. */ }
  return { mode: "parallel", version: 1, updatedAt: new Date(0).toISOString() };
}

export function writeBootstrapSettings(cwd: string, mode: BootstrapMode, model: string | undefined = readBootstrapSettings(cwd).model): BootstrapSettings {
  if (!MODES.includes(mode)) throw new Error(`invalid bootstrap mode: ${mode}`);
  const path = settingsPath(cwd);
  const value: BootstrapSettings = { mode, version: 1, updatedAt: new Date().toISOString(), ...(model?.trim() ? { model: model.trim() } : {}) };
  mkdirSync(dirname(path), { recursive: true });
  const temporary = `${path}.${process.pid}.${Math.random().toString(16).slice(2)}.tmp`;
  writeFileSync(temporary, `${JSON.stringify(value)}\n`, { mode: 0o600 });
  renameSync(temporary, path);
  return value;
}

export function resetBootstrapSettings(cwd: string): BootstrapSettings { return writeBootstrapSettings(cwd, "parallel"); }

export function isEligibleNewSession(ctx: unknown): boolean {
  const entries = (ctx as { sessionManager?: { getEntries?: () => readonly unknown[] } } | null)?.sessionManager?.getEntries?.();
  return !entries?.some((entry) => (entry as { type?: unknown } | null)?.type === "message");
}
