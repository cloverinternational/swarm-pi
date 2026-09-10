import { spawn, spawnSync } from "node:child_process";
import { openSync, mkdirSync, readFileSync, writeFileSync, rmSync, existsSync } from "node:fs";
import { join, resolve } from "node:path";
import { withDefaultToolRenderer } from "../../../packages/runtime/core/src/tool-renderer.ts";

type UI = { notify?: (message: string, type?: string) => void };
type Pi = {
  getCwd?: () => string;
  on?: (event: string, handler: (...args: any[]) => unknown) => void;
  registerTool?: (tool: unknown) => void;
  registerCommand?: (name: string, spec: { description: string; handler: (args: string, ctx: { ui?: UI }) => Promise<void> }) => void;
};

const DEFAULT_PORT = "6767";

function repoRoot(pi: Pi): string { return resolve(pi.getCwd?.() ?? process.cwd()); }
function paseoDir(pi: Pi): string { return join(repoRoot(pi), "vendor", "paseo"); }
function stateDir(pi: Pi): string { return join(repoRoot(pi), "artifacts", "paseo"); }
function pidFile(pi: Pi): string { return join(stateDir(pi), "daemon.pid"); }
function logFile(pi: Pi): string { return join(stateDir(pi), "daemon.log"); }
function cliEntry(pi: Pi): string { return join(paseoDir(pi), "packages", "cli", "dist", "index.js"); }
function defaultListen(): string {
  if (process.env.PASEO_LISTEN) return process.env.PASEO_LISTEN;
  const result = spawnSync("tailscale", ["ip", "-4"], { encoding: "utf8", timeout: 1500 });
  const ip = result.status === 0 ? result.stdout.trim().split(/\s+/)[0] : "";
  return /^100\.(?:[0-9]{1,3}\.){2}[0-9]{1,3}$/.test(ip) ? `${ip}:${DEFAULT_PORT}` : `127.0.0.1:${DEFAULT_PORT}`;
}

function sh(cwd: string, cmd: string, args: string[], timeoutMs = 15 * 60 * 1000) {
  const result = spawnSync(cmd, args, { cwd, encoding: "utf8", timeout: timeoutMs, maxBuffer: 16 * 1024 * 1024, env: { ...process.env, CI: "1" } });
  const output = `${result.stdout ?? ""}${result.stderr ?? ""}`.trim();
  return { ok: result.status === 0, code: result.status, output: output.length > 4000 ? `…${output.slice(-4000)}` : output };
}

function daemonPid(pi: Pi): number | undefined {
  try {
    const pid = Number(readFileSync(pidFile(pi), "utf8").trim());
    if (Number.isInteger(pid) && pid > 0) { process.kill(pid, 0); return pid; }
  } catch { /* stale or missing */ }
  return undefined;
}

async function health(listen: string): Promise<boolean> {
  try {
    const response = await fetch(`http://${listen}/`, { signal: AbortSignal.timeout(3000) });
    return response.status < 500;
  } catch { return false; }
}

export function paseoStatus(pi: Pi) {
  const dir = paseoDir(pi);
  const cloned = existsSync(join(dir, "package.json"));
  const built = existsSync(cliEntry(pi));
  const rev = cloned ? sh(dir, "git", ["log", "-1", "--format=%h %s"]).output : "";
  return { cloned, built, revision: rev, pid: daemonPid(pi), listen: defaultListen() };
}

export function paseoUpdate(pi: Pi) {
  const dir = paseoDir(pi);
  const steps: Array<[string, ReturnType<typeof sh>]> = [];
  steps.push(["fetch", sh(dir, "git", ["fetch", "--depth", "1", "origin", "main"])]);
  steps.push(["checkout", sh(dir, "git", ["checkout", "--detach", "FETCH_HEAD"])]);
  const failed = steps.find(([, r]) => !r.ok);
  return { success: !failed, steps: steps.map(([name, r]) => ({ name, ok: r.ok, output: r.output })) };
}

export function paseoBuild(pi: Pi) {
  const dir = paseoDir(pi);
  const install = sh(dir, "npm", ["install", "--no-audit", "--no-fund"]);
  if (!install.ok) return { success: false, step: "install", output: install.output };
  const build = sh(dir, "npm", ["run", "build:server"]);
  return { success: build.ok, step: "build:server", output: build.output };
}

export function paseoStart(pi: Pi, listen = defaultListen()) {
  const existing = daemonPid(pi);
  if (existing) return { success: true, alreadyRunning: true, pid: existing, listen };
  if (!existsSync(cliEntry(pi))) return { success: false, error: "paseo is not built; run the paseo tool with action=build first" };
  mkdirSync(stateDir(pi), { recursive: true });
  const fd = openSync(logFile(pi), "a");
  const child = spawn(process.execPath, ["--disable-warning=DEP0040", cliEntry(pi), "daemon", "start", "--foreground"], {
    cwd: paseoDir(pi),
    detached: true,
    stdio: ["ignore", fd, fd],
    env: { ...process.env, PASEO_LISTEN: listen },
  });
  child.unref();
  writeFileSync(pidFile(pi), String(child.pid));
  return { success: true, pid: child.pid, listen, log: logFile(pi) };
}

export function paseoStop(pi: Pi) {
  const pid = daemonPid(pi);
  if (!pid) { rmSync(pidFile(pi), { force: true }); return { success: true, wasRunning: false }; }
  try { process.kill(-pid, "SIGTERM"); } catch { try { process.kill(pid, "SIGTERM"); } catch { /* gone */ } }
  rmSync(pidFile(pi), { force: true });
  return { success: true, wasRunning: true, pid };
}

export function paseoPair(pi: Pi) {
  return sh(paseoDir(pi), process.execPath, ["--disable-warning=DEP0040", cliEntry(pi), "daemon", "pair", "--relay", "--json"]);
}

function tailLog(pi: Pi, lines = 40): string {
  try { return readFileSync(logFile(pi), "utf8").split("\n").slice(-lines).join("\n"); } catch { return "(no log)"; }
}

const parameters = {
  type: "object",
  properties: {
    action: { type: "string", enum: ["status", "update", "build", "start", "stop", "pair", "logs"], description: "status/update/build/start/stop manage Paseo; pair creates a mobile relay link; logs tails the daemon log." },
    listen: { type: "string", description: `Listen target for start (defaults to the machine's Tailscale IPv4 on port ${DEFAULT_PORT}).` },
    lines: { type: "integer", minimum: 1, maximum: 500, default: 40, description: "Log lines for action=logs." },
  },
  required: ["action"],
};

const text = (value: unknown) => ({ content: [{ type: "text", text: typeof value === "string" ? value : JSON.stringify(value, undefined, 1) }], details: value });

export default function paseoExtension(pi: Pi): void {
  // Paseo is part of every Pi session: start the local daemon before the
  // first prompt, but never block session startup on a stale/broken install.
  pi.on?.("session_start", (_event: unknown, ctx: any) => {
    const result = paseoStart(pi);
    if (!result.success) {
      ctx?.ui?.notify?.(`Paseo auto-start skipped: ${result.error}`, "warning");
      return;
    }
    process.env.PASEO_HOST ||= `http://${result.listen}`;
    ctx?.ui?.notify?.(result.alreadyRunning ? `Paseo connected on ${result.listen}.` : `Paseo started on ${result.listen}.`, "info");
  });

  pi.registerTool?.(withDefaultToolRenderer({
    name: "paseo",
    label: "Paseo daemon",
    description: "Manage the vendored Paseo install (vendor/paseo): check status, update from upstream, build, and start/stop the daemon for remote agent access.",
    parameters,
    async execute(_id: string, input: any) {
      switch (input?.action) {
        case "status": {
          const status = paseoStatus(pi);
          return text({ ...status, healthy: status.pid ? await health(status.listen) : false });
        }
        case "update": return text(paseoUpdate(pi));
        case "build": return text(paseoBuild(pi));
        case "start": return text(paseoStart(pi, input.listen));
        case "stop": return text(paseoStop(pi));
        case "pair": return text(paseoPair(pi));
        case "logs": return text(tailLog(pi, input.lines ?? 40));
        default: return text({ success: false, error: "action must be status, update, build, start, stop, pair, or logs" });
      }
    },
  }));

  pi.registerCommand?.("paseo", {
    description: "Paseo daemon controls: /paseo status|update|build|start|stop|pair|logs",
    handler: async (args, ctx) => {
      const action = args.trim().split(/\s+/, 1)[0] || "status";
      try {
        if (action === "status") {
          const s = paseoStatus(pi);
          ctx.ui?.notify?.(`paseo ${s.revision || "(not cloned)"} · built=${s.built} · daemon=${s.pid ? `pid ${s.pid} on ${s.listen}` : "stopped"}`, "info");
        } else if (action === "update") {
          const r = paseoUpdate(pi);
          ctx.ui?.notify?.(r.success ? "Updated vendor/paseo to latest upstream main. Run /paseo build next." : `Update failed: ${r.steps.find((s) => !s.ok)?.output}`, r.success ? "info" : "error");
        } else if (action === "build") {
          ctx.ui?.notify?.("Building paseo (this can take several minutes)…", "info");
          const r = paseoBuild(pi);
          ctx.ui?.notify?.(r.success ? "Paseo built." : `Build failed at ${r.step}: ${r.output?.slice(-500)}`, r.success ? "info" : "error");
        } else if (action === "start") {
          const r = paseoStart(pi);
          ctx.ui?.notify?.(r.success ? (r.alreadyRunning ? `Already running (pid ${r.pid}).` : `Daemon started (pid ${r.pid}) on ${r.listen}.`) : r.error!, r.success ? "info" : "error");
        } else if (action === "stop") {
          const r = paseoStop(pi);
          ctx.ui?.notify?.(r.wasRunning ? `Stopped daemon (pid ${r.pid}).` : "Daemon was not running.", "info");
        } else if (action === "pair") {
          const r = paseoPair(pi);
          ctx.ui?.notify?.(r.ok ? r.output : `Pairing failed: ${r.output}`, r.ok ? "info" : "error");
        } else if (action === "logs") {
          ctx.ui?.notify?.(tailLog(pi), "info");
        } else {
          throw new Error("usage: /paseo status|update|build|start|stop|logs");
        }
      } catch (error) { ctx.ui?.notify?.(error instanceof Error ? error.message : String(error), "error"); }
    },
  });
}
