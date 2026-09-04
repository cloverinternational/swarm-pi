import { describe, expect, it } from "vitest";
import { mkdir, mkdtemp, symlink, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join, resolve } from "node:path";
import { Policy, PolicyError, runProcess } from "../src/index.js";

const setup = async () => { const root = await mkdtemp(join(tmpdir(), "swarm-policy-")); await mkdir(join(root, "inside")); await writeFile(join(root, "inside", "x"), "ok"); return root; };

describe("deterministic policy", () => {
  it("fails closed for tools, mutation, network, and credentials", async () => {
    const p = new Policy({ workspace: await setup(), allowedTools: ["read"], allowedHosts: ["example.com"], allowedCredentialEnv: ["X_KEY"] });
    expect(() => p.authorize({ tool: "write", operation: "write" })).toThrow("not allowlisted");
    expect(() => p.authorize({ tool: "read", operation: "network", host: "example.com" })).toThrow("network access");
    expect(() => p.authorize({ tool: "read", operation: "credential", credentialEnv: "HOME" })).toThrow("not allowlisted");
    expect(p.audit().every(x => !x.reason.includes("X_KEY"))).toBe(true);
  });
  it("resolves and rejects symlink escapes", async () => {
    const root = await setup(); const outside = await mkdtemp(join(tmpdir(), "outside-")); await writeFile(join(outside, "secret"), "no"); await symlink(join(outside, "secret"), join(root, "inside", "link"));
    const p = new Policy({ workspace: root, allowedTools: ["read"] });
    await expect(p.authorizePath({ tool: "read", operation: "read", path: join(root, "inside", "link") })).rejects.toMatchObject({ code: "path.boundary" });
    await expect(p.authorizePath({ tool: "read", operation: "read", path: join(root, "inside", "x") })).resolves.toBe(resolve(root, "inside/x"));
  });
  it("requires exact approval and bounds process output", async () => {
    const p = new Policy({ workspace: await setup(), allowedTools: ["bash"], maxOutputBytes: 8, timeoutMs: 1000 });
    const req = { tool: "bash", operation: "execute" as const, command: process.execPath };
    expect(() => p.authorize({ ...req, command: "sudo" })).toThrow("approval");
    const result = await runProcess(p, req, ["-e", "process.stdout.write('123456789')"]);
    expect(result.stdout).toBe("12345678"); expect(result.truncated).toBe(true); expect(result.code).toBe(0);
  });
  it("cancels and times out processes", async () => {
    const p = new Policy({ workspace: await setup(), allowedTools: ["bash"], timeoutMs: 30 });
    const timed = await runProcess(p, { tool: "bash", operation: "execute", command: process.execPath }, ["-e", "setTimeout(()=>{},1000)"]);
    expect(timed.timedOut).toBe(true);
    const controller = new AbortController(); const running = runProcess(p, { tool: "bash", operation: "execute", command: process.execPath }, ["-e", "setTimeout(()=>{},1000)"], controller.signal); controller.abort(); const cancelled = await running; expect(cancelled.code).not.toBe(0);
  });
});
