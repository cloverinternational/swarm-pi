import { describe, expect, it } from "vitest";
import { AutoSkillManager } from "../src/index.js";
import { existsSync, mkdirSync, mkdtempSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";

const instructions = "A reusable production procedure. ".repeat(8);
const fresh = () => mkdtempSync(join(tmpdir(), "autogen-lock-discovery-"));
const lockFor = (dir: string, name: string) => join(dir, ".history", "locks", `${name}.lock`);

function installLock(dir: string, name: string, owner: unknown) {
  const lock = lockFor(dir, name);
  mkdirSync(lock, { recursive: true });
  writeFileSync(join(lock, "owner.json"), typeof owner === "string" ? owner : JSON.stringify(owner));
  return lock;
}

describe("lock discovery stress tests (run before implementation changes)", () => {
  it("does not steal a lock with a live PID but malformed owner metadata", () => {
    const dir = fresh();
    const lock = installLock(dir, "live-corrupt", { pid: process.pid, token: "missing-created-at" });
    const manager = new AutoSkillManager({ dir, mode: "manual", lockTimeoutMs: 1200 });

    expect(() => manager.execute({
      action: "create", name: "live-corrupt", description: "test", instructions,
    })).toThrow(/timed out waiting for skill mutation lock/);
    expect(existsSync(lock)).toBe(true);
  });

  it("does not reap a lock when owner.json is temporarily unreadable", () => {
    const dir = fresh();
    const lock = installLock(dir, "partial-owner", "{");
    const manager = new AutoSkillManager({ dir, mode: "manual", lockTimeoutMs: 1200 });

    expect(() => manager.execute({
      action: "create", name: "partial-owner", description: "test", instructions,
    })).toThrow(/timed out waiting for skill mutation lock/);
    expect(existsSync(lock)).toBe(true);
  });
});
