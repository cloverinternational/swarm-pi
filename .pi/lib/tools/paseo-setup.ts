import * as childProcess from "node:child_process";
import { closeSync, existsSync, mkdirSync, openSync, readFileSync, renameSync, rmSync, writeFileSync, lstatSync, chmodSync } from "node:fs";
import { homedir } from "node:os";
import { join, resolve } from "node:path";
import { fileURLToPath } from "node:url";
import { constants, fstatSync } from "node:fs";
import { request } from "node:http";

export type PaseoDeps = {
  spawn?: typeof childProcess.spawn; fetch?: typeof fetch;
  fs?: Pick<typeof import("node:fs"), "mkdirSync" | "readFileSync" | "writeFileSync" | "renameSync" | "rmSync" | "existsSync" | "openSync" | "closeSync" | "lstatSync" | "chmodSync">;
  env?: NodeJS.ProcessEnv; platform?: NodeJS.Platform; root?: string; state?: string; paseoHome?: string;
  now?: () => number; sleep?: (ms: number) => Promise<void>; processKill?: (pid: number, signal?: NodeJS.Signals | number) => void;
  websocket?: (url: string, timeoutMs: number) => Promise<unknown>;
  processCommand?: (pid: number) => string;
  serveApi?: (method: "GET" | "POST", body?: string, etag?: string) => Promise<{ status: number; body: string; etag?: string }>;
};
export type CommandResult = { ok: boolean; code: number | null; output: string; timedOut?: boolean };
export type PaseoResult = { success: boolean; partial?: boolean; error?: string; steps?: Array<{ name: string; ok: boolean; output?: string }>; [key: string]: unknown };
const CAP = 64 * 1024;
const JSON_CAP = 8 * 1024 * 1024;
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
    let output = "", settled = false, overflow = false; let timer: ReturnType<typeof setTimeout> | undefined;
    const finish = (r: CommandResult) => { if (settled) return; settled = true; if (timer) clearTimeout(timer); resolveResult({ ...r, ok: r.ok && !overflow, output: overflow ? "JSON output exceeds 8 MiB safety limit" : output || r.output }); };
    try {
      const child = d.spawn!(command, args, { cwd, env: { ...d.env, CI: "1" }, stdio: ["ignore", "pipe", "pipe"] });
      const collect = (x: unknown) => { if (overflow) return; output += String(x ?? ""); if (preserve && output.length > JSON_CAP) { overflow = true; output = ""; } else if (!preserve && output.length > CAP) output = output.slice(-CAP); };
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
      try { d.processKill!(pid, 0); } catch (e: any) { if (e?.code === "EPERM") return { pid, listen: raw.listen, unverifiable: true }; if (e?.code === "ESRCH") return; throw e; }
      const argv = d.fs.readFileSync(`/proc/${pid}/cmdline`, "utf8").split("\0").filter(Boolean);
      const expected = [process.execPath, "--disable-warning=DEP0040", cli, "daemon", "start", "--foreground"];
      if (JSON.stringify(argv) !== JSON.stringify(expected)) return;
      const env = d.fs.readFileSync(`/proc/${pid}/environ`, "utf8").split("\0");
      const listen = env.find(x => x.startsWith("PASEO_LISTEN="))?.slice(13);
      if (!listen || (raw.listen && raw.listen !== listen)) return;
      const stat = d.fs.readFileSync(`/proc/${pid}/stat`, "utf8");
      const fields = stat.slice(stat.lastIndexOf(")") + 2).split(/\s+/);
      const started = fields[19];
      if (!started || !/^\d+$/.test(started)) return;
      return { pid, listen, unverifiable: false, started, file };
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
  // Tailscale local API supports ETag/If-Match, unlike a CLI read-then-write.
  // Never fall back to an unconditional mutation if conditional writes fail.
  const serveApi = input.serveApi ?? ((method: "GET" | "POST", body?: string, etag?: string) => new Promise<{ status: number; body: string; etag?: string }>((resolveApi, reject) => {
    const req = request({ socketPath: d.env.TAILSCALE_SOCKET ?? "/var/run/tailscale/tailscaled.sock", path: "/localapi/v0/serve-config", method,
      headers: { Host: "local-tailscaled.sock", "Content-Type": "application/json", ...(etag ? { "If-Match": etag } : {}) } }, res => {
      let text = "";
      res.on("data", chunk => { text += String(chunk); if (text.length > JSON_CAP) req.destroy(Error("Serve JSON exceeds 8 MiB safety limit")); });
      res.on("error", reject);
      res.on("end", () => resolveApi({ status: res.statusCode ?? 0, body: text, etag: typeof res.headers.etag === "string" ? res.headers.etag : undefined }));
    });
    req.setTimeout(10000, () => req.destroy(Error("Tailscale local API timeout")));
    req.on("error", reject); req.end(body);
  }));
  const verifyDirectory = (path: string) => {
    const st = d.fs.lstatSync!(path) as any;
    if (st.isSymbolicLink?.()) throw Error("Refusing symlink config ancestor");
    // Do not write through an untrusted directory.  Test doubles need not
    // provide uid/mode, but real Linux filesystems always do.
    if (typeof st.uid === "number" && typeof st.mode === "number") {
      const uid = typeof process.getuid === "function" ? process.getuid() : -1;
      const writableByOther = (st.mode & 0o022) !== 0 && (st.mode & 0o1000) === 0;
      if (st.uid !== 0 && st.uid !== uid || writableByOther) throw Error("Refusing unsafe config directory");
    }
  };
  const atomicPath = (file: string, value: unknown, expected?: string, anchored = false) => {
    const parent = file.slice(0, file.lastIndexOf("/"));
    for (let path = anchored ? "" : parent; path && path !== "/"; path = path.slice(0, path.lastIndexOf("/"))) {
      try { verifyDirectory(path); }
      catch (e: any) { if (e.code !== "ENOENT") throw e; }
    }
    if (!anchored) {
      d.fs.mkdirSync(parent, { recursive: true, mode: 0o700 });
      verifyDirectory(parent);
    }
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
  const atomic = (file: string, value: unknown, expected?: string) => {
    // Injected filesystems model writes in tests. Real Linux writes are anchored
    // to directory descriptors, so renaming an ancestor cannot redirect them.
    if (input.fs) return atomicPath(file, value, expected);
    if (d.platform !== "linux") throw Error("Descriptor-safe config writes require Linux");
    if (!file.startsWith("/") || file.split("/").includes("..")) throw Error("Unsafe config path");
    const parts = file.split("/").filter(Boolean);
    const name = parts.pop()!;
    const flags = constants.O_RDONLY | constants.O_DIRECTORY | constants.O_NOFOLLOW;
    const descriptors: number[] = [];
    try {
      let fd = openSync("/", flags); descriptors.push(fd);
      for (const part of parts) {
        const anchored = `/proc/self/fd/${fd}/${part}`;
        let next: number;
        try { next = openSync(anchored, flags); }
        catch (e: any) {
          if (e.code !== "ENOENT") throw e;
          try { mkdirSync(anchored, { mode: 0o700 }); } catch (mkdirError: any) { if (mkdirError.code !== "EEXIST") throw mkdirError; }
          next = openSync(anchored, flags);
        }
        descriptors.push(next);
        const st = fstatSync(next);
        const uid = process.getuid!();
        if ((st.uid !== 0 && st.uid !== uid) || ((st.mode & 0o022) !== 0 && (st.mode & 0o1000) === 0)) throw Error("Unsafe config directory owner or mode");
        fd = next;
      }
      atomicPath(`/proc/self/fd/${fd}/${name}`, value, expected, true);
    } finally { for (const fd of descriptors.reverse()) closeSync(fd); }
  };
  const conflicts = (raw: string, hostname: string, listen: string) => {
    const v = JSON.parse(raw);
    if (!v || typeof v !== "object" || Array.isArray(v)) throw Error("Invalid Serve configuration");
    if (Object.keys(v).some(k => !["TCP", "Web", "AllowFunnel"].includes(k))) throw Error("Unsupported Serve configuration");
    if (v.TCP !== undefined && (!v.TCP || typeof v.TCP !== "object" || Array.isArray(v.TCP) || Object.keys(v.TCP).some(k => k !== "443"))) throw Error("Unsupported TCP Serve configuration");
    if (v.Web !== undefined && (!v.Web || typeof v.Web !== "object" || Array.isArray(v.Web) || Object.keys(v.Web).some(k => !/^[-a-z0-9.]+:443$/.test(k)))) throw Error("Unsupported Web Serve configuration");
    const funnel = v.AllowFunnel === true || Object.values(v.AllowFunnel ?? {}).some(x => x === true);
    const tcp = v.TCP?.["443"];
    const handlers = v.Web?.[`${hostname}:443`]?.Handlers;
    const route = handlers?.["/"];
    if (v.Web?.[`${hostname}:443`] && (!handlers || Object.keys(handlers).some(k => k !== "/") || !route || Object.keys(route).some(k => k !== "Proxy"))) throw Error("Unsupported Web Serve configuration");
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
      const snapshot = await serveApi("GET");
      if (snapshot.status !== 200 || !snapshot.etag) throw Error("Conditional Serve updates unavailable; refusing mutation");
      const snapshotConfig = JSON.parse(snapshot.body) ?? {};
      const initial = conflicts(JSON.stringify(snapshotConfig), dnsName, listen);
      if (initial.funnel || initial.root) throw Error("Serve configuration changed; refusing mutation");
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
      if (!initial.same) {
        routeAttempted = true;
        const next = { ...snapshotConfig, TCP: { "443": { HTTPS: true } }, Web: { [`${dnsName}:443`]: { Handlers: { "/": { Proxy: `http://${listen}` } } } } };
        const serve = await serveApi("POST", JSON.stringify(next), snapshot.etag);
        if (serve.status === 412) throw Error("Serve changed concurrently; conditional update rejected without overwrite");
        if (serve.status !== 200) throw Error("Conditional Tailscale Serve update failed; check local permissions");
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
      if (old?.unverifiable) return { success: false, error: "Existing daemon identity cannot be verified; refusing launch" };
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
      let child: ReturnType<typeof childProcess.spawn> | undefined;
      let spawnedPid: number | undefined;
      const reap = async () => {
        if (!spawnedPid) return true;
        try { d.processKill!(-spawnedPid, "SIGTERM"); } catch (e: any) { if (e?.code !== "ESRCH" && e?.code !== "EPERM") { /* try pid below */ } }
        try { d.processKill!(spawnedPid, "SIGTERM"); } catch (e: any) { if (e?.code !== "ESRCH" && e?.code !== "EPERM") return false; }
        const until = d.now!() + 5000;
        while (d.now!() < until) {
          try { d.processKill!(spawnedPid, 0); } catch (e: any) { if (e?.code === "ESRCH") return true; if (e?.code === "EPERM") return false; }
          await d.sleep!(100);
        }
        return false;
      };
      try { child = d.spawn!(process.execPath, ["--disable-warning=DEP0040", cli, "daemon", "start", "--foreground"], { cwd: paseo, detached: true, stdio: ["ignore", fd, fd], env: { ...d.env, PASEO_HOME: paseoHome, PASEO_LISTEN: listen } }); }
      finally { d.fs.closeSync(fd); }
      spawnedPid = child.pid;
      let failed = false;
      child.on("error", () => { failed = true; });
      child.on("exit", () => { failed = true; });
      child.unref();
      if (!child.pid) throw Error("Daemon launch failed");
      try { atomic(pidFile, { pid: child.pid, listen }); }
      catch (e) {
        const cleaned = await reap();
        if (!cleaned) return { success: false, partial: true, error: `Daemon record persistence failed and spawned process cleanup could not be verified: ${e instanceof Error ? e.message : "write failed"}` };
        throw e;
      }
      const deadline = d.now!() + 5000;
      while (!failed && d.now!() < deadline) {
        if (await health(listen) && !failed) return { success: true, pid: child.pid, listen, ready: true, log };
        await d.sleep!(100);
      }
      return { success: false, partial: true, pid: child.pid, listen, ready: false, error: "Daemon did not become ready" };
    } catch (e) { return { success: false, error: e instanceof Error ? e.message : "Daemon launch failed" }; }
    finally { d.fs.rmSync(lock, { recursive: true, force: true }); }
  };
  const waitGone = async (pid: number) => { const until = d.now!() + 5000; while (d.now!() < until) { try { d.processKill!(pid, 0); } catch (e: any) { if (e?.code === "ESRCH") return true; if (e?.code === "EPERM") return false; } await d.sleep!(100); } return false; };
  const stop = async (): Promise<PaseoResult> => {
    if (!acquire()) return { success: false, error: "Another Paseo operation is in progress" };
    try {
      const x = owned() ?? legacyOwned() ?? (() => {
        for (const file of [pidFile, join(d.root!, "artifacts", "paseo", "daemon.pid")]) {
          try { const raw = JSON.parse(d.fs.readFileSync(file, "utf8")); const pid = typeof raw === "number" ? raw : raw.pid; if (Number.isInteger(pid) && pid > 0) { d.processKill!(pid, 0); return { pid, listen: "", unverifiable: true }; } }
          catch (e: any) { if (e?.code === "EPERM") return { pid: JSON.parse(d.fs.readFileSync(file, "utf8")).pid, listen: "", unverifiable: true }; }
        }
        return undefined;
      })();
      if (x?.unverifiable) return { success: false, partial: true, error: "Daemon record could not be verified; record retained" };
      if (!x) { return { success: true, wasRunning: false }; }
      if (!("file" in x) || !("started" in x)) throw Error("Missing process generation; record retained");
      const fresh = record(x.file!);
      if (!fresh || fresh.unverifiable || fresh.pid !== x.pid || fresh.started !== x.started) throw Error("Process identity changed before stop; no signal sent");
      // Signal only the validated supervisor, never a potentially recycled group.
      // Linux kill is not pidfd-based; validation and signal are adjacent sync calls.
      try { d.processKill!(x.pid, "SIGTERM"); } catch (e: any) { if (e?.code !== "ESRCH") throw e; }
      const gone = await waitGone(x.pid);
      if (!gone) return { success: false, partial: true, pid: x.pid, error: "Daemon did not stop; record retained" };
      for (const file of [pidFile, join(d.root!, "artifacts", "paseo", "daemon.pid")]) {
        try {
          const raw = JSON.parse(d.fs.readFileSync(file, "utf8"));
          if ((typeof raw === "number" ? raw : raw.pid) === x.pid) d.fs.rmSync(file, { force: true });
        } catch (e: any) { if (e.code !== "ENOENT") throw e; }
      }
      return { success: true, wasRunning: true, pid: x.pid };
    } catch (e) { return { success: false, error: e instanceof Error ? e.message : "Stop failed; record retained" }; }
    finally { d.fs.rmSync!(lock, { recursive: true, force: true }); }
  };
  const logs = (lines = 40) => { try { return d.fs.readFileSync!(log, "utf8").split("\n").slice(-Math.max(1, Math.min(500, lines))).join("\n"); } catch { return "(no log)"; } };
  const simple = (args: string[], cwd = paseo) => run(cwd, args[0], args.slice(1));
  const lifecycleGuard = () => { const x = owned() ?? legacyOwned(); if (x) throw Error("Paseo daemon is running; stop it before update/build"); for (const file of [pidFile, join(d.root!, "artifacts", "paseo", "daemon.pid")]) { if (!d.fs.existsSync(file)) continue; const raw = JSON.parse(d.fs.readFileSync(file, "utf8")); const pid = typeof raw === "number" ? raw : raw.pid; try { d.processKill!(pid, 0); throw Error("Paseo daemon record is live but unverifiable"); } catch (e: any) { if (e?.code !== "ESRCH") throw e; } } };
  const update = async () => { if (!acquire()) return { success: false, error: "Another Paseo operation is in progress" }; try { lifecycleGuard(); const fetchResult = await run(paseo, "git", ["fetch", "--depth", "1", "origin", "main"]); if (!fetchResult.ok) return { success: false, steps: [{ name: "fetch", ok: false, output: fetchResult.output }] }; const checkout = await run(paseo, "git", ["checkout", "--detach", "FETCH_HEAD"]); return { success: checkout.ok, steps: [{ name: "fetch", ok: true, output: fetchResult.output }, { name: "checkout", ok: checkout.ok, output: checkout.output }] }; } catch (e) { return { success: false, error: e instanceof Error ? e.message : "Update refused" }; } finally { d.fs.rmSync!(lock, { recursive: true, force: true }); } };
  const build = async () => { if (!acquire()) return { success: false, error: "Another Paseo operation is in progress" }; try { lifecycleGuard(); const i = await run(paseo, "npm", ["install", "--no-audit", "--no-fund"]); if (!i.ok) return { success: false, partial: true, step: "install", output: i.output }; const b = await run(paseo, "npm", ["run", "build:server"]); return { success: b.ok, step: "build:server", output: b.output }; } catch (e) { return { success: false, error: e instanceof Error ? e.message : "Build refused" }; } finally { d.fs.rmSync!(lock, { recursive: true, force: true }); } };
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
