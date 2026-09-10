import { EventEmitter } from "node:events";
import { describe, expect, it, vi } from "vitest";
import { createPaseoSetup } from "../../lib/tools/paseo-setup.ts";

function fixture(initial: Record<string, string> = {}) {
  const files = new Map(Object.entries(initial));
  const dirs = new Set<string>();
  const fs: any = {
    mkdirSync: (p: string) => { if (p.endsWith("/start.lock") && dirs.has(p)) throw Object.assign(new Error(), { code: "EEXIST" }); dirs.add(p); },
    readFileSync: (p: string) => { if (!files.has(p)) throw Object.assign(new Error(), { code: "ENOENT" }); return files.get(p); },
    writeFileSync: (p: string, v: string) => { files.set(p, v); },
    renameSync: (a: string, b: string) => { files.set(b, files.get(a)!); files.delete(a); },
    rmSync: (p: string) => { files.delete(p); dirs.delete(p); },
    existsSync: (p: string) => files.has(p), openSync: () => 1, closeSync: () => {}, chmodSync: () => {},
    lstatSync: (p: string) => { if (!files.has(p) && !dirs.has(p)) throw Object.assign(new Error(), { code: "ENOENT" }); return { isSymbolicLink: () => false }; },
  };
  return { fs, files, dirs };
}

const child = (pid: number) => { const c: any = new EventEmitter(); c.pid = pid; c.unref = vi.fn(); c.stdout = new EventEmitter(); c.stderr = new EventEmitter(); c.kill = vi.fn(); return c; };
const base = (fs: any, processKill: any, spawn: any) => createPaseoSetup({ root: "/project", state: "/state", paseoHome: "/home", fs, processKill, spawn, platform: "linux", now: () => Date.now(), sleep: async () => {}, fetch: vi.fn(async () => ({ status: 200 })) as any, websocket: vi.fn(async () => true) });

describe("Paseo lifecycle review", () => {
  it("refuses to signal when a validated PID generation changes", async () => {
    const f = fixture({ "/state/daemon.json": JSON.stringify({ pid: 42, listen: "127.0.0.1:6767" }),
      "/proc/42/cmdline": `${process.execPath}\0--disable-warning=DEP0040\0/project/vendor/paseo/packages/cli/dist/index.js\0daemon\0start\0--foreground\0`,
      "/proc/42/environ": "PASEO_LISTEN=127.0.0.1:6767\0" });
    const original = f.fs.readFileSync;
    let generation = 1;
    f.fs.readFileSync = (path: string) => path.endsWith("/stat")
      ? `42 (node) ${["S", ...Array(18).fill("0"), String(generation++)].join(" ")}` : original(path);
    const kill = vi.fn();
    const result = await base(f.fs, kill, vi.fn()).stop();
    expect(result.success).toBe(false);
    expect(kill.mock.calls.every(call => call[1] === 0)).toBe(true);
    expect(f.files.has("/state/daemon.json")).toBe(true);
  });

  it("refuses update/build with a live record or an occupied operation lock", async () => {
    const f = fixture({ "/state/daemon.json": JSON.stringify({ pid: 42 }) });
    const spawn = vi.fn();
    const s = base(f.fs, vi.fn(), spawn);
    expect((await s.update()).success).toBe(false);
    expect((await s.build()).success).toBe(false);
    f.files.clear(); f.dirs.add("/state/start.lock");
    expect((await s.update()).success).toBe(false);
    expect((await s.build()).success).toBe(false);
    expect(spawn).not.toHaveBeenCalled();
  });

  it("does not checkout after a failed fetch", async () => {
    const f = fixture();
    const spawn = vi.fn(() => {
      const c = child(99);
      queueMicrotask(() => c.emit("close", 1));
      return c;
    });
    const result = await base(f.fs, vi.fn(), spawn).update();
    expect(result.success).toBe(false);
    expect(spawn).toHaveBeenCalledOnce();
    expect(spawn.mock.calls[0]).toBeTruthy();
  });

  it("reaps a spawned child when PID persistence fails", async () => {
    const f = fixture({ "/project/vendor/paseo/packages/cli/dist/index.js": "" }); const kill = vi.fn(() => { throw Object.assign(new Error("gone"), { code: "ESRCH" }); }); const c = child(42);
    f.fs.writeFileSync = () => { throw new Error("disk full"); };
    const s = base(f.fs, kill, vi.fn(() => c));
    const result = await s.start();
    expect(result).toMatchObject({ success: false }); expect(kill).toHaveBeenCalledWith(-42, "SIGTERM");
  });

  it("retains the record when TERM cannot verify process exit", async () => {
    const f = fixture({ "/state/daemon.json": JSON.stringify({ pid: 42, listen: "127.0.0.1:6767" }), "/project/vendor/paseo/packages/cli/dist/index.js": "" });
    const kill = vi.fn((_pid: number, signal?: string | number) => { if (signal === 0) throw Object.assign(new Error(), { code: "EPERM" }); });
    const s = base(f.fs, kill, vi.fn());
    const result = await s.stop();
    expect(result.success).toBe(false); expect(f.files.has("/state/daemon.json")).toBe(true);
  });

  it("stops and removes a validated legacy record", async () => {
    const f = fixture({ "/project/artifacts/paseo/daemon.pid": JSON.stringify({ pid: 42, listen: "127.0.0.1:6767" }), "/proc/42/cmdline": `${process.execPath}\0--disable-warning=DEP0040\0/project/vendor/paseo/packages/cli/dist/index.js\0daemon\0start\0--foreground\0`, "/proc/42/environ": "PASEO_LISTEN=127.0.0.1:6767\0" });
    f.files.set("/proc/42/stat", `42 (node) ${["S", ...Array(18).fill("0"), "12345"].join(" ")}`);
    let gone = false;
    const kill = vi.fn((_pid: number, signal?: string | number) => {
      if (signal === "SIGTERM") gone = true;
      if (signal === 0 && gone) throw Object.assign(new Error(), { code: "ESRCH" });
    });
    const s = base(f.fs, kill, vi.fn());
    const result = await s.stop();
    expect(result).toMatchObject({ success: true, wasRunning: true }); expect(f.files.has("/project/artifacts/paseo/daemon.pid")).toBe(false);
  });
});
