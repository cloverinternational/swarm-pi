import { describe, expect, it } from "vitest";
import { AutoSkillManager } from "../src/index.js";
import { existsSync, lstatSync, mkdirSync, mkdtempSync, readdirSync, symlinkSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";

const instructions = "A reusable production procedure. ".repeat(8);
const fresh = () => mkdtempSync(join(tmpdir(), "autogen-lock-fuzz-"));
const lockPath = (dir: string, name = "fuzz-skill") => join(dir, ".history", "locks", `${name}.lock`);
const create = (dir: string, name = "fuzz-skill") => new AutoSkillManager({ dir, mode: "manual", lockTimeoutMs: 30 }).execute({
  action: "create", name, description: "fuzz", instructions,
});

function artifact(dir: string, kind: number) {
  const path = lockPath(dir);
  mkdirSync(join(dir, ".history", "locks"), { recursive: true });
  if (kind === 0) writeFileSync(path, "legacy plain lock");
  else if (kind === 1) { mkdirSync(path); writeFileSync(join(path, "owner.json"), "not-json"); }
  else if (kind === 2) { mkdirSync(path); writeFileSync(join(path, "owner.json"), JSON.stringify({ pid: 99999999, token: "dead", createdAt: new Date(0).toISOString() })); }
  else if (kind === 3) { mkdirSync(path); writeFileSync(join(path, "owner.json"), JSON.stringify({ pid: process.pid, token: "live-but-not-held", createdAt: new Date().toISOString() })); }
  else if (kind === 4) { writeFileSync(path, "x"); }
  else { mkdirSync(path); writeFileSync(join(path, "owner.json"), "{}"); writeFileSync(join(path, "reap"), "someone"); }
}

describe("skill lock adversarial and fuzz coverage", () => {
  it("recovers every non-symlink legacy/corrupt lock shape without hanging", () => {
    for (let seed = 0; seed < 120; seed++) {
      const dir = fresh();
      const shape = seed % 6;
      artifact(dir, shape);
      if (shape === 1 || shape === 3) {
        expect(() => create(dir)).toThrow(/timed out waiting for skill mutation lock/);
      } else if (shape === 5) {
        // A lock with a fresh, malformed owner is treated conservatively as
        // potentially being mid-acquisition; it must time out, not be stolen.
        expect(() => create(dir)).toThrow(/timed out waiting for skill mutation lock/);
      } else {
        expect(() => create(dir)).not.toThrow();
        expect(existsSync(lockPath(dir))).toBe(false);
      }
    }
  });

  it("fails closed for a symlink lock and never touches its target", () => {
    const dir = fresh();
    const target = join(dir, "untouched");
    writeFileSync(target, "keep");
    mkdirSync(join(dir, ".history", "locks"), { recursive: true });
    symlinkSync(target, lockPath(dir));
    expect(() => create(dir)).toThrow(/symlinks are not permitted/);
    expect(lstatSync(target).isFile()).toBe(true);
  });

  it("cleans all lock artifacts after repeated successful mutations", () => {
    const dir = fresh();
    const manager = new AutoSkillManager({ dir, mode: "manual" });
    for (let i = 0; i < 40; i++) {
      manager.execute({ action: "create", name: `skill-${i}`, description: "fuzz", instructions });
    }
    expect(existsSync(join(dir, ".history", "locks")) ?
      readdirSync(join(dir, ".history", "locks")) : []).toHaveLength(0);
  });

  it("does not let a live owner be reaped", () => {
    const dir = fresh();
    artifact(dir, 3);
    const manager = new AutoSkillManager({ dir, mode: "manual", lockTimeoutMs: 20 });
    expect(() => manager.execute({ action: "create", name: "fuzz-skill", description: "fuzz", instructions }))
      .toThrow(/timed out waiting for skill mutation lock/);
  });
});
