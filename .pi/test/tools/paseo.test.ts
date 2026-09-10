import { afterEach, describe, expect, it, vi } from "vitest";
import { EventEmitter } from "node:events";
import { createPaseoSetup, mergePaseoConfig, mergePaseoHost } from "../../lib/tools/paseo-setup.ts";

type MemFs = ReturnType<typeof memoryFs>;

function memoryFs(initial: Record<string, string> = {}) {
  const files = new Map(Object.entries(initial));
  const dirs = new Set<string>();
  const writes: string[] = [];
  const mkdirSync = vi.fn((path: string, options?: any) => {
    if (path.endsWith("/start.lock") && dirs.has(path)) throw Object.assign(new Error("exists"), { code: "EEXIST" });
    dirs.add(path);
  });
  const readFileSync = vi.fn((path: string) => {
    if (!files.has(path)) throw Object.assign(new Error("missing"), { code: "ENOENT" });
    return files.get(path)!;
  });
  const fs = {
    mkdirSync, readFileSync,
    writeFileSync: vi.fn((path: string, value: string) => { files.set(path, value); writes.push(path); }),
    renameSync: vi.fn((from: string, to: string) => { files.set(to, files.get(from)!); files.delete(from); }),
    rmSync: vi.fn((path: string) => { files.delete(path); dirs.delete(path); }),
    existsSync: vi.fn((path: string) => files.has(path)),
    openSync: vi.fn(() => 1), closeSync: vi.fn(),
    lstatSync: vi.fn((path: string) => { if (!files.has(path) && !dirs.has(path)) throw Object.assign(new Error("missing"), { code: "ENOENT" }); return { isSymbolicLink: () => false }; }),
    chmodSync: vi.fn(),
  } as any;
  return { fs, files, dirs, writes };
}

function commandHarness(options: { status?: unknown; routes?: unknown; reloadOk?: boolean; serveOk?: boolean } = {}) {
  const status = options.status ?? { BackendState: "Running", Self: { DNSName: "machine.example.ts.net.", TailscaleIPs: ["100.64.0.1"] } };
  let routes = options.routes ?? {};
  const calls: string[][] = [];
  const spawn = vi.fn((_command: string, args: string[]) => {
    calls.push(args);
    const child = new EventEmitter() as any;
    child.stdout = new EventEmitter(); child.stderr = new EventEmitter(); child.kill = vi.fn();
    queueMicrotask(() => {
      let output = "{}"; let code = 0;
      if (args[0] === "status" && args[1] === "--json") output = JSON.stringify(status);
      else if (args[0] === "serve" && args[1] === "status" && args[2] === "--json") output = JSON.stringify(routes);
      else if (args.at(-4) === "daemon" && args.at(-3) === "reload") code = options.reloadOk === false ? 1 : 0;
      else if (args[0] === "serve" && args[1] !== "status") {
        code = options.serveOk === false ? 1 : 0;
        if (code === 0) routes = { TCP: { "443": { HTTPS: true } }, Web: { "machine.example.ts.net:443": { Handlers: { "/": { Proxy: "http://127.0.0.1:6767" } } } } };
      }
      child.stdout.emit("data", output);
      child.emit("close", code);
    });
    return child;
  });
  return { spawn, calls };
}

function setup(fs: MemFs, commands: ReturnType<typeof commandHarness>, extra: Record<string, unknown> = {}) {
  return createPaseoSetup({ root: "/project", state: "/state", paseoHome: "/paseo-home", fs: fs.fs, spawn: commands.spawn as any, fetch: vi.fn(async () => ({ status: 200 })) as any, websocket: vi.fn(async () => true), ...extra });
}

afterEach(() => vi.restoreAllMocks());

describe("Paseo setup safety", () => {
  it("merges nested config and preserves legacy policy, but rejects invalid and wildcard policies", () => {
    expect(mergePaseoConfig({ keep: { nested: true }, other: 1 }, { keep: { changed: true } })).toEqual({ keep: { changed: true }, other: 1 });
    expect(mergePaseoHost({ keep: true, daemon: { allowedHosts: ["old.ts.net"] } }, "machine.ts.net")).toEqual({ keep: true, daemon: { allowedHosts: ["old.ts.net"], hostnames: ["old.ts.net", "machine.ts.net"] } });
    expect(() => mergePaseoHost({ daemon: { allowedHosts: ["*"] } }, "machine.ts.net")).not.toThrow();
    expect(() => mergePaseoHost({ daemon: { hostnames: true } }, "machine.ts.net")).toThrow();
    expect(() => mergePaseoHost({ daemon: { allowedHosts: ["*"] } as any }, "machine.ts.net")).not.toThrow();
    expect(() => mergePaseoHost({ daemon: { allowedHosts: true } as any }, "machine.ts.net")).toThrow();
  });

  it("fresh setup writes nested config, reloads before Serve, and verifies HTTPS/wss", async () => {
    const fs = memoryFs(); const commands = commandHarness(); const s = setup(fs, commands);
    const result = await s.setup("127.0.0.1:6767", true);
    expect(result, result.error).toMatchObject({ success: true, configChanged: true, routeChanged: true, verified: true });
    const config = JSON.parse(fs.files.get("/paseo-home/config.json")!);
    expect(config.daemon.hostnames).toEqual(["machine.example.ts.net"]);
    const reload = commands.calls.findIndex(a => a.includes("daemon") && a.includes("reload"));
    const serve = commands.calls.findIndex(a => a[0] === "serve" && a.includes("--bg"));
    expect(reload).toBeGreaterThanOrEqual(0);
    expect(serve).toBeGreaterThan(reload);
    expect(commands.calls.some(a => a[0] === "serve" && a.includes("--https=443"))).toBe(true);
  });

  it("repeated correct setup writes no config, does not mutate the route, but still reloads and verifies", async () => {
    const routes = { TCP: { "443": { HTTPS: true } }, Web: { "machine.example.ts.net:443": { Handlers: { "/": { Proxy: "http://127.0.0.1:6767" } } } } };
    const fs = memoryFs({ "/paseo-home/config.json": JSON.stringify({ keep: { x: 1 }, daemon: { hostnames: ["machine.example.ts.net"] } }) });
    const commands = commandHarness({ routes }); const s = setup(fs, commands);
    const result = await s.setup("127.0.0.1:6767", true);
    expect(result).toMatchObject({ success: true, changed: false, configChanged: false, routeChanged: false });
    expect(fs.writes).toEqual([]);
    expect(commands.calls.filter(a => a[0] === "serve" && a[1] !== "status").length).toBe(0);
    expect(commands.calls.some(a => a.includes("daemon") && a.includes("reload"))).toBe(true);
  });

  it("refuses conflicting route or Funnel without writing config or invoking Serve", async () => {
    for (const routes of [{ AllowFunnel: { "machine.example.ts.net": true } }, { TCP: { "443": { HTTPS: false } } }]) {
      const fs = memoryFs(); const commands = commandHarness({ routes }); const s = setup(fs, commands);
      const result = await s.setup("127.0.0.1:6767", true);
      expect(result.success).toBe(false); expect(fs.writes).toEqual([]); expect(commands.calls.some(a => a[0] === "serve" && a[1] !== "status")).toBe(false);
    }
  });

  it("does not write malformed config", async () => {
    const fs = memoryFs({ "/paseo-home/config.json": "{not json" }); const commands = commandHarness();
    const result = await setup(fs, commands).setup("127.0.0.1:6767", true);
    expect(result.success).toBe(false); expect(result.partial).toBe(false); expect(fs.writes).toEqual([]);
  });

  it("uses the real session/status then pong handshake", async () => {
    const sent: any[] = [];
    class Socket {
      onopen?: () => void; onmessage?: (e: { data: string }) => void; onclose?: () => void;
      constructor() { queueMicrotask(() => this.onopen?.()); }
      send(data: string) {
        const message = JSON.parse(data); sent.push(message);
        queueMicrotask(() => this.onmessage?.({ data: JSON.stringify(message.type === "hello"
          ? { type: "session", message: { type: "status", payload: { status: "server_info", serverId: "test-daemon" } } }
          : { type: "pong" }) }));
      }
      close() { this.onclose?.(); }
    }
    vi.stubGlobal("WebSocket", Socket);
    try {
      const s = setup(memoryFs(), commandHarness(), { websocket: undefined });
      expect(await s.health("127.0.0.1:6767")).toBe(true);
      expect(sent[0]).toMatchObject({ type: "hello", clientType: "cli", protocolVersion: 1 });
      expect(sent[0].clientId).toBeTruthy();
      expect(sent[1]).toEqual({ type: "ping" });
    } finally { vi.unstubAllGlobals(); }
  });

  it("reports reload failure as partial and never invokes Serve", async () => {
    const fs = memoryFs(); const commands = commandHarness({ reloadOk: false });
    const result = await setup(fs, commands).setup("127.0.0.1:6767", true);
    expect(result).toMatchObject({ success: false, partial: true, configChanged: true });
    expect(commands.calls.some(a => a[0] === "serve" && a[1] !== "status")).toBe(false);
  });

  it("fails when HTTPS or WebSocket verification fails after Serve", async () => {
    const fs = memoryFs(); const commands = commandHarness();
    const s = setup(fs, commands, { websocket: vi.fn(async (url: string) => url.startsWith("ws://")) });
    const result = await s.setup("127.0.0.1:6767", true);
    expect(result).toMatchObject({ success: false, partial: true, routeAttempted: true });
  });

  it("refuses concurrent setup lock and non-running Tailscale", async () => {
    const locked = memoryFs(); locked.dirs.add("/state/start.lock");
    const lockResult = await setup(locked, commandHarness()).setup("127.0.0.1:6767", true);
    expect(lockResult.success).toBe(false);
    const stopped = memoryFs(); const result = await setup(stopped, commandHarness({ status: { BackendState: "Stopped", Self: { DNSName: "machine.example.ts.net." } } })).setup("127.0.0.1:6767", true);
    expect(result).toMatchObject({ success: false, partial: false });
  });
});
