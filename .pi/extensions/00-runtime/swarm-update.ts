import { spawn } from "node:child_process";

export const DEFAULT_UPDATE_URL = "https://raw.githubusercontent.com/cloverinternational/swarm-pi/main/update-manifest.json";
export const updateUrl = () => process.env.PI_SWARM_UPDATE_URL || DEFAULT_UPDATE_URL;
export const CHECK_INTERVAL_MS = 60 * 60 * 1000;
const REQUEST_TIMEOUT_MS = 5_000;
const CURRENT_VERSION = "0.1.0";

export type UpdateManifest = { schemaVersion: 1; package: string; version: string; releasedAt: string; changelog: string; source: string; updateCommand: string };
type ParsedVersion = { numbers: [number, number, number]; prerelease: string[] };

function parseVersion(value: unknown): ParsedVersion | undefined {
  if (typeof value !== "string") return undefined;
  const match = /^(0|[1-9]\d*)\.(0|[1-9]\d*)\.(0|[1-9]\d*)(?:-([0-9A-Za-z.-]+))?$/.exec(value);
  return match ? { numbers: [Number(match[1]), Number(match[2]), Number(match[3])], prerelease: match[4]?.split(".") ?? [] } : undefined;
}

export function compareVersions(left: string, right: string): number {
  const a = parseVersion(left); const b = parseVersion(right);
  if (!a || !b) throw new Error("versions must use strict semver (x.y.z)");
  for (let i = 0; i < 3; i += 1) if (a.numbers[i] !== b.numbers[i]) return a.numbers[i] > b.numbers[i] ? 1 : -1;
  if (!a.prerelease.length && !b.prerelease.length) return 0;
  if (!a.prerelease.length) return 1; if (!b.prerelease.length) return -1;
  for (let i = 0; i < Math.max(a.prerelease.length, b.prerelease.length); i += 1) {
    const av = a.prerelease[i]; const bv = b.prerelease[i];
    if (av === undefined) return -1; if (bv === undefined) return 1; if (av === bv) continue;
    const an = /^\d+$/.test(av); const bn = /^\d+$/.test(bv);
    if (an && bn) return Number(av) > Number(bv) ? 1 : -1;
    if (an !== bn) return an ? -1 : 1;
    return av > bv ? 1 : -1;
  }
  return 0;
}

export function validateManifest(value: unknown): UpdateManifest | undefined {
  if (!value || typeof value !== "object") return undefined;
  const v = value as Record<string, unknown>;
  if (v.schemaVersion !== 1 || v.package !== "@pi-swarm/integration" || !parseVersion(v.version)) return undefined;
  for (const key of ["releasedAt", "changelog", "source", "updateCommand"]) if (typeof v[key] !== "string" || !v[key]) return undefined;
  return v as UpdateManifest;
}

async function fetchManifest(url: string): Promise<UpdateManifest> {
  const controller = new AbortController(); const timer = setTimeout(() => controller.abort(), REQUEST_TIMEOUT_MS);
  try {
    const response = await fetch(url, { signal: controller.signal, headers: { accept: "application/json" } });
    if (!response.ok) throw new Error(`update manifest HTTP ${response.status}`);
    const manifest = validateManifest(await response.json());
    if (!manifest) throw new Error("update manifest has an invalid schema");
    return manifest;
  } finally { clearTimeout(timer); }
}

function notify(ctx: any, message: string, level: "info" | "warning" | "error" = "info") { ctx?.ui?.notify?.(message, level); }

export default function swarmUpdateExtension(pi: any) {
  let lastCheck = 0; let timer: ReturnType<typeof setInterval> | undefined; let context: any;
  const check = async (force = false, ctx = context) => {
    if (!force && Date.now() - lastCheck < CHECK_INTERVAL_MS) return false;
    lastCheck = Date.now();
    if (process.env.PI_OFFLINE === "1" || process.env.PI_SWARM_SKIP_UPDATE_CHECK === "1") return false;
    try {
      const manifest = await fetchManifest(updateUrl());
      if (compareVersions(manifest.version, CURRENT_VERSION) > 0) { notify(ctx, `Pi-Swarm ${manifest.version} is available (installed ${CURRENT_VERSION}). Changelog: ${manifest.changelog}\nRun /swarm-update install to update.`, "warning"); return true; }
      if (force) notify(ctx, `Pi-Swarm is up to date (${CURRENT_VERSION}).`);
    } catch (error) { if (force) notify(ctx, `Could not check for Pi-Swarm updates: ${error instanceof Error ? error.message : String(error)}`, "error"); }
    return false;
  };
  const install = async (ctx: any) => {
    if (process.env.PI_OFFLINE === "1") { notify(ctx, "Offline mode is enabled; update was not started.", "error"); return; }
    const child = spawn("pi", ["update", "--extensions"], { detached: true, stdio: "ignore" });
    child.once("error", (error) => notify(ctx, `Could not start Pi update: ${error.message}. Run pi update --extensions manually.`, "error")); child.unref();
    notify(ctx, "Pi-Swarm update started. Restart Pi when it finishes.");
  };
  pi.registerCommand?.("swarm-update", { description: "Check for or install Pi-Swarm updates", handler: async (args: string, ctx: any) => args.trim() === "install" ? install(ctx) : check(true, ctx) });
  pi.on?.("session_start", (_event: any, ctx: any) => { context = ctx; if (timer) clearInterval(timer); void check(); timer = setInterval(() => void check(), CHECK_INTERVAL_MS); timer.unref?.(); });
  pi.on?.("session_shutdown", () => { if (timer) clearInterval(timer); timer = undefined; context = undefined; });
  return { checkForUpdate: check };
}
