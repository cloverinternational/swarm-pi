import * as childProcess from "node:child_process";
import { closeSync, existsSync, mkdirSync, openSync, readFileSync, renameSync, rmSync, writeFileSync, lstatSync, chmodSync } from "node:fs";
import { homedir } from "node:os";
import { join, resolve } from "node:path";
import { fileURLToPath } from "node:url";

export type PaseoDeps = {
  spawn?: typeof childProcess.spawn; fetch?: typeof fetch;
  fs?: Pick<typeof import("node:fs"), "mkdirSync" | "readFileSync" | "writeFileSync" | "renameSync" | "rmSync" | "existsSync" | "openSync" | "closeSync" | "lstatSync" | "chmodSync">;
  env?: NodeJS.ProcessEnv; platform?: NodeJS.Platform; root?: string; state?: string; paseoHome?: string;
  now?: () => number; sleep?: (ms: number) => Promise<void>; processKill?: (pid: number, signal?: NodeJS.Signals | number) => void;
  websocket?: (url: string, timeoutMs: number) => Promise<unknown>;
  processCommand?: (pid: number) => string;
};
export type CommandResult = { ok: boolean; code: number | null; output: string; timedOut?: boolean };
export type PaseoResult = { success: boolean; partial?: boolean; error?: string; steps?: Array<{ name: string; ok: boolean; output?: string }>; [key: string]: unknown };
const CAP = 64 * 1024;
const tail = (s: string, n = 4000) => s.length > n ? `…${s.slice(-n)}` : s;
const defaultRoot = () => resolve(fileURLToPath(new URL("../../..", import.meta.url)));
export function mergePaseoConfig<T extends Record<string, unknown>>(current: T, patch: Partial<T>): T { return { ...current, ...patch }; }
export function mergePaseoHost(config: Record<string, unknown>, hostname: string): Record<string, unknown> {
  if (config.daemon !== undefined && (!config.daemon || typeof config.daemon !== "object" || Array.isArray(config.daemon))) throw new Error("Invalid daemon config");
  const daemon = config.daemon && typeof config.daemon === "object" && !Array.isArray(config.daemon)
    ? config.daemon as Record<string, unknown> : {};
  const existing = daemon.hostnames ?? daemon.allowedHosts;
  if (existing === true) throw new Error("refusing to narrow wildcard daemon.hostnames");
  if (existing !== undefined && (!Array.isArray(existing) || existing.some(v => typeof v !== "string"))) throw new Error("Invalid hostname policy");
  const hosts = (existing ?? []) as string[];
  const result = { ...config, daemon: { ...daemon, hostnames: hosts.includes(hostname) ? hosts : [...hosts, hostname] } };
  if (config.hostnames !== undefined) (result as Record<string, unknown>).hostnames = config.hostnames;
  return result;
}

export function createPaseoSetup(input: PaseoDeps = {}) {
  const d = { spawn: input.spawn ?? childProcess.spawn, fetch, fs: { mkdirSync, readFileSync, writeFileSync, renameSync, rmSync, existsSync, openSync, closeSync, lstatSync, chmodSync }, env: process.env, platform: process.platform, root: defaultRoot(), state: join(input.env?.XDG_STATE_HOME || process.env.XDG_STATE_HOME || join(homedir(), ".local", "state"), "pi-swarm", "paseo"), now: Date.now, sleep: (ms: number) => new Promise<void>(r => setTimeout(r, ms)), processKill: process.kill.bind(process), processCommand: (pid: number) => { try { return readFileSync(`/proc/${pid}/cmdline`, "utf8").replaceAll("\0", " "); } catch { return ""; } }, ...input };
  const paseo = join(d.root!, "vendor", "paseo"), paseoHome = input.paseoHome ?? d.env!.PASEO_HOME ?? join(homedir(), ".paseo");
  const cli = join(paseo, "packages", "cli", "dist", "index.js"), pidFile = join(d.state!, "daemon.json"), lock = join(d.state!, "start.lock"), log = join(d.state!, "daemon.log");
  const run = (cwd: string, command: string, args: string[], timeout = 15 * 60_000, preserve = false): Promise<CommandResult> => new Promise(resolveResult => {
    let output = "", settled = false; let timer: ReturnType<typeof setTimeout> | undefined;
    const finish = (r: CommandResult) => { if (settled) return; settled = true; if (timer) clearTimeout(timer); resolveResult({ ...r, output: preserve ? output : tail(output) }); };
    try {
      const child = d.spawn!(command, args, { cwd, env: { ...d.env, CI: "1" }, stdio: ["ignore", "pipe", "pipe"] });
      const collect = (x: unknown) => { output += String(x ?? ""); if (output.length > CAP) output = output.slice(-CAP); };
      child.stdout?.on("data", collect); child.stderr?.on("data", collect);
      child.once("error", e => finish({ ok: false, code: null, output: String(e) }));
      child.once("close", (code, signal) => finish({ ok: code === 0, code: code ?? (signal ? null : 0), output }));
      timer = setTimeout(() => { try { child.kill("SIGKILL"); } catch {} finish({ ok: false, code: null, output, timedOut: true }); }, timeout);
    } catch (e) { finish({ ok: false, code: null, output: String(e) }); }
  });
  const record = (file: string) => {
    try {
      if (d.platform !== "linux") return;
      const raw = JSON.parse(d.fs.readFileSync(file, "utf8"));
      const pid = typeof raw === "number" ? raw : raw.pid;
      if (!Number.isInteger(pid) || pid <= 0) return;
      d.processKill!(pid, 0);
      const argv = d.fs.readFileSync(`/proc/${pid}/cmdline`, "utf8").split("\0").filter(Boolean);
      const expected = [process.execPath, "--disable-warning=DEP0040", cli, "daemon", "start", "--foreground"];
      if (JSON.stringify(argv) !== JSON.stringify(expected)) return;
      const env = d.fs.readFileSync(`/proc/${pid}/environ`, "utf8").split("\0");
      const listen = env.find(x => x.startsWith("PASEO_LISTEN="))?.slice(13);
      if (!listen || (raw.listen && raw.listen !== listen)) return;
      return { pid, listen };
    } catch { return; }
  };
  const owned = () => record(pidFile);
  const legacyOwned = () => record(join(d.root!, "artifacts", "paseo", "daemon.pid"));
  const nativeWebsocket = (url: string, timeoutMs: number) => new Promise<boolean>(resolveHealth => {
    const WS = (globalThis as any).WebSocket; if (typeof WS !== "function") return resolveHealth(false);
    let ws: any, status = false, pong = false, done = false;
    const timer = setTimeout(() => finish(false), timeoutMs); const finish = (ok: boolean) => { if (done) return; done = true; clearTimeout(timer); try { ws?.close(); } catch {} resolveHealth(ok); };
    try { ws = new WS(url); ws.onopen = () => ws.send(JSON.stringify({ type: "hello", clientId: `pi-swarm-${process.pid}-${Date.now()}`, clientType: "cli", protocolVersion: 1 })); ws.onmessage = (e: any) => { try { const envelope = JSON.parse(String(e.data)); const m = envelope.type === "session" ? envelope.message : envelope; if (m?.type === "status" && m.payload?.serverId && !status) { status = true; ws.send(JSON.stringify({ type: "ping" })); } if (envelope.type === "pong" && status) pong = true; if (status && pong) finish(true); } catch {} }; ws.onerror = () => finish(false); ws.onclose = () => finish(false); } catch { finish(false); }
  });
  const health = async (listen: string) => { try { const r = await d.fetch!(`http://${listen}/api/health`, { signal: AbortSignal.timeout(3000), redirect: "error" }); if (r.status !== 200) return false; const result = d.websocket ? await d.websocket(`ws://${listen}/ws`, 3000) : await nativeWebsocket(`ws://${listen}/ws`, 3000); return result === true || (typeof result === "object" && result !== null && (result as any).hello === true && (result as any).status === true && (result as any).ping === true); } catch { return false; } };
  const safeConfig = () => { if (!paseoHome.startsWith("/") || paseoHome.split("/").includes("..")) throw new Error("PASEO_HOME must be an absolute safe path"); try { if (d.fs.lstatSync?.(paseoHome).isSymbolicLink()) throw new Error("refusing symlink PASEO_HOME"); } catch (e: any) { if (e?.code !== "ENOENT") throw e; } return join(paseoHome, "config.json"); };
  const readConfig = (file: string): Record<string, unknown> | undefined => { try { const v = JSON.parse(d.fs.readFileSync!(file, "utf8")); return v && typeof v === "object" && !Array.isArray(v) ? v as Record<string, unknown> : undefined; } catch (e: any) { return e?.code === "ENOENT" ? {} : undefined; } };
  const atomic = (file: string, value: unknown, expected?: string) => {
    const parent = file.slice(0, file.lastIndexOf("/"));
    for (let path = parent; path && path !== "/"; path = path.slice(0, path.lastIndexOf("/"))) {
      try { if (d.fs.lstatSync(path).isSymbolicLink()) throw Error("Refusing symlink config ancestor"); }
      catch (e: any) { if (e.code !== "ENOENT") throw e; }
    }
    d.fs.mkdirSync(parent, { recursive: true, mode: 0o700 });
    const compare = () => {
      try { if (d.fs.lstatSync(file).isSymbolicLink()) throw Error("Refusing symlink config"); }
      catch (e: any) { if (e.code !== "ENOENT") throw e; }
      if (expected === undefined) return;
      let actual = "";
      try { actual = d.fs.readFileSync(file, "utf8"); } catch (e: any) { if (e.code !== "ENOENT") throw e; }
      if (actual !== expected) throw Error("Configuration changed concurrently; refusing overwrite");
    };
    compare();
    const tmp = `${file}.${crypto.randomUUID()}.tmp`;
    let created = false;
    try {
      d.fs.writeFileSync(tmp, `${JSON.stringify(value, null, 2)}\n`, { mode: 0o600, flag: "wx" });
      created = true;
      compare();
      d.fs.renameSync(tmp, file);
    } finally { if (created) d.fs.rmSync(tmp, { force: true }); }
  };
  const conflicts = (raw: string, hostname: string, listen: string) => {
    const v = JSON.parse(raw);
    if (!v || typeof v !== "object" || Array.isArray(v)) throw Error("Invalid Serve configuration");
    if (Object.keys(v).some(k => !["TCP", "Web", "AllowFunnel", "Foreground", "Services"].includes(k))) throw Error("Unsupported Serve configuration");
    if (v.Foreground && Object.keys(v.Foreground).length) throw Error("Foreground Serve configuration requires review");
    const funnel = v.AllowFunnel === true || Object.values(v.AllowFunnel ?? {}).some(x => x === true);
    const tcp = v.TCP?.["443"];
    const handlers = v.Web?.[`${hostname}:443`]?.Handlers;
    const route = handlers?.["/"];
    const same = Boolean(tcp?.HTTPS === true && Object.keys(tcp).length === 1 && route?.Proxy === `http://${listen}` && Object.keys(route).length === 1);
    const other443 = Object.keys(v.Web ?? {}).some(k => k.endsWith(":443") && k !== `${hostname}:443`);
    return { funnel, root: Boolean(other443 || (tcp && !same) || (route && !same)), same };
  };
  const inspect = async () => { const cloned = d.fs.existsSync!(join(paseo, "package.json")), built = d.fs.existsSync!(cli); const revision = cloned || built ? (await run(paseo, "git", ["log", "-1", "--format=%h %s"])).output.trim() : ""; const daemon = owned() ?? legacyOwned(); return { cloned, built, revision, daemon, pid: daemon?.pid, listen: daemon?.listen ?? defaultListen() }; };
  const setupUnlocked = async (requested?: string, apply = false): Promise<PaseoResult> => {
    let configChanged = false, routeAttempted = false;
    try {
      const listen = requested ?? owned()?.listen ?? legacyOwned()?.listen ?? defaultListen();
      const target = new URL(`http://${listen}`);
      if (target.host !== listen || target.username || target.password || !target.port || !/^(127\.0\.0\.1|100\.(?:\d{1,3}\.){2}\d{1,3}|\[::1\])$/.test(target.hostname)) throw Error("Setup requires a local loopback or Tailscale IPv4 listen target");
      const status = await run(d.root!, "tailscale", ["status", "--json"], 10_000, true);
      if (!status.ok) throw Error("Tailscale status unavailable; check installation and login");
      const data = JSON.parse(status.output);
      if (data.BackendState !== "Running") throw Error("Tailscale is not running; login is required");
      const dnsName = String(data.Self?.DNSName ?? "").replace(/\.$/, "").toLowerCase();
      if (dnsName.length > 253 || !dnsName.endsWith(".ts.net") || dnsName.split(".").some(label => !/^[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?$/.test(label))) throw Error("Invalid machine DNSName");
      if (target.hostname.startsWith("100.") && !data.Self?.TailscaleIPs?.includes(target.hostname)) throw Error("Listener is not this machine's Tailscale address");
      const routes = await run(d.root!, "tailscale", ["serve", "status", "--json"], 10_000, true);
      if (!routes.ok) throw Error("Cannot inspect Serve configuration");
      const c = conflicts(routes.output, dnsName, listen);
      if (c.funnel || c.root) throw Error("Conflicting Serve/Funnel route; existing configuration left untouched");
      if (!apply) return { success: true, changed: false, dnsName, listen };
      if (!await health(listen)) throw Error("Direct daemon handshake failed; no configuration changed");
      const file = safeConfig();
      let before: string;
      try { before = d.fs.readFileSync(file, "utf8"); }
      catch (e: any) { if (e.code !== "ENOENT") throw e; before = ""; }
      const current = before === "" ? {} : JSON.parse(before);
      if (!current || typeof current !== "object" || Array.isArray(current)) throw Error("Invalid Paseo config");
      const merged = mergePaseoHost(current, dnsName);
      configChanged = JSON.stringify(current) !== JSON.stringify(merged);
      if (configChanged) atomic(file, merged, before);
      // Reload on repeats too: a previous run may have persisted config then failed.
      const reload = await run(d.root!, process.execPath, ["--disable-warning=DEP0040", cli, "daemon", "reload", "--host", `http://${listen}`], 10_000, true);
      if (!reload.ok) throw Error("Daemon hot reload failed; hostname config retained for retry");
      const check = await run(d.root!, "tailscale", ["serve", "status", "--json"], 10_000, true);
      if (!check.ok || JSON.stringify(JSON.parse(check.output)) !== JSON.stringify(JSON.parse(routes.output))) throw Error("Serve configuration changed concurrently; refusing to overwrite");
      if (!c.same) {
        routeAttempted = true;
        const serve = await run(d.root!, "tailscale", ["serve", "--bg", "--yes", "--https=443", `http://${listen}`], 10_000, true);
        if (!serve.ok) throw Error("Tailscale Serve failed; check local permissions");
      }
      const after = await run(d.root!, "tailscale", ["serve", "status", "--json"], 10_000, true);
      if (!after.ok) throw Error("Serve target verification failed");
      const verifiedRoute = conflicts(after.output, dnsName, listen);
      if (!verifiedRoute.same || verifiedRoute.funnel || verifiedRoute.root) throw Error("Serve target verification failed");
      const https = await d.fetch!(`https://${dnsName}/api/health`, { signal: AbortSignal.timeout(5000), redirect: "error" });
      if (https.status !== 200 || await (d.websocket ?? nativeWebsocket)(`wss://${dnsName}/ws`, 5000) !== true) throw Error("HTTPS WebSocket verification failed");
      return { success: true, changed: configChanged || routeAttempted, configChanged, routeChanged: routeAttempted, dnsName, listen, url: `https://${dnsName}`, verified: true };
    } catch (e) {
      return { success: false, partial: configChanged || routeAttempted, configChanged, routeAttempted, error: e instanceof Error ? e.message : "Setup failed" };
    }
  };
  const acquire = () => { try { d.fs.mkdirSync!(d.state!, { recursive: true }); d.fs.mkdirSync!(lock); return true; } catch { return false; } };
  const start = async (listen = defaultListen()): Promise<PaseoResult> => {
    if (d.platform !== "linux") return { success: false, error: "Paseo ownership checks require Linux" };
    if (!acquire()) return { success: false, error: "Another Paseo operation is in progress; inspect start.lock if interrupted" };
    try {
      const old = owned() ?? legacyOwned();
      if (old) { const ready = await health(old.listen); return { success: ready, alreadyRunning: true, ...old, ready, ...(!ready ? { error: "Existing daemon failed health check; left untouched" } : {}) }; }
      // Never replace a live but unverified PID, including installations using another CLI.
      for (const file of [pidFile, join(d.root!, "artifacts", "paseo", "daemon.pid")]) {
        if (!d.fs.existsSync(file)) continue;
        const raw = JSON.parse(d.fs.readFileSync(file, "utf8"));
        const pid = typeof raw === "number" ? raw : raw.pid;
        if (!Number.isInteger(pid) || pid <= 0) throw Error("Invalid daemon PID record; refusing replacement");
        try { d.processKill!(pid, 0); return { success: false, error: "Live daemon record could not be verified; refusing duplicate launch" }; }
        catch (e: any) { if (e.code !== "ESRCH") throw e; }
      }
      if (!d.fs.existsSync(cli)) return { success: false, error: "Paseo is not built; run build first" };
      const fd = d.fs.openSync(log, "a", 0o600);
      let child: ReturnType<typeof childProcess.spawn>;
      try { child = d.spawn!(process.execPath, ["--disable-warning=DEP0040", cli, "daemon", "start", "--foreground"], { cwd: paseo, detached: true, stdio: ["ignore", fd, fd], env: { ...d.env, PASEO_HOME: paseoHome, PASEO_LISTEN: listen } }); }
      finally { d.fs.closeSync(fd); }
      let failed = false;
      child.on("error", () => { failed = true; });
      child.on("exit", () => { failed = true; });
      child.unref();
      if (!child.pid) throw Error("Daemon launch failed");
      atomic(pidFile, { pid: child.pid, listen });
      const deadline = d.now!() + 5000;
      while (!failed && d.now!() < deadline) {
        if (await health(listen) && !failed) return { success: true, pid: child.pid, listen, ready: true, log };
        await d.sleep!(100);
      }
      return { success: false, partial: true, pid: child.pid, listen, ready: false, error: "Daemon did not become ready" };
    } catch (e) { return { success: false, error: e instanceof Error ? e.message : "Daemon launch failed" }; }
    finally { d.fs.rmSync(lock, { recursive: true, force: true }); }
  };
  const stop = () => { const x = owned(); if (!x) { d.fs.rmSync!(pidFile, { force: true }); return { success: true, wasRunning: false }; } try { d.processKill!(-x.pid, "SIGTERM"); } catch { try { d.processKill!(x.pid, "SIGTERM"); } catch {} } d.fs.rmSync!(pidFile, { force: true }); return { success: true, wasRunning: true, pid: x.pid }; };
  const logs = (lines = 40) => { try { return d.fs.readFileSync!(log, "utf8").split("\n").slice(-Math.max(1, Math.min(500, lines))).join("\n"); } catch { return "(no log)"; } };
  const simple = (args: string[], cwd = paseo) => run(cwd, args[0], args.slice(1));
  const update = async () => { const steps = [["fetch", await run(paseo, "git", ["fetch", "--depth", "1", "origin", "main"])], ["checkout", await run(paseo, "git", ["checkout", "--detach", "FETCH_HEAD"])]] as const; return { success: steps.every(([, r]) => r.ok), steps: steps.map(([name, r]) => ({ name, ok: r.ok, output: r.output })) }; };
  const build = async () => { const i = await run(paseo, "npm", ["install", "--no-audit", "--no-fund"]); if (!i.ok) return { success: false, partial: true, step: "install", output: i.output }; const b = await run(paseo, "npm", ["run", "build:server"]); return { success: b.ok, step: "build:server", output: b.output }; };
  const pair = () => simple([process.execPath, "--disable-warning=DEP0040", cli, "daemon", "pair", "--relay", "--json"]);
  const applySetup = async (listen?: string) => { if (!acquire()) return { success: false, error: "Another Paseo setup is already in progress; if interrupted, inspect start.lock before removing it" }; try { return await setupUnlocked(listen, true); } finally { d.fs.rmSync!(lock, { recursive: true, force: true }); } };
  const setup = (listen?: string, apply = false) => apply ? applySetup(listen) : setupUnlocked(listen, false);
  return { run, inspect, setup, applySetup, start, stop, update, build, pair, health, logs, serve: setup, paths: { paseo, cli, state: d.state!, pidFile, log, paseoHome }, deps: d };
}
export const paseo = createPaseoSetup();
export const defaultListen = () => process.env.PASEO_LISTEN || "127.0.0.1:6767";
const live = () => createPaseoSetup({ spawn: childProcess.spawn });
export const paseoStatus = (_pi?: unknown) => live().inspect();
export const paseoSetup = (_pi?: unknown, listen?: string, apply = false) => live().setup(listen, apply);
export const paseoStart = (_pi?: unknown, listen = defaultListen()) => live().start(listen);
export const paseoStop = (_pi?: unknown) => live().stop();
export const paseoUpdate = (_pi?: unknown) => live().update();
export const paseoBuild = (_pi?: unknown) => live().build();
export const paseoPair = (_pi?: unknown) => live().pair();
