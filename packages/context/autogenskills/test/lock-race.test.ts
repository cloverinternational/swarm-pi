import { describe, expect, it } from "vitest";
import { AutoSkillManager } from "../src/index.js";
import { existsSync, mkdirSync, mkdtempSync, readdirSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";

const instructions = "A reusable production procedure. ".repeat(8);
const dir = () => mkdtempSync(join(tmpdir(), "autogen-race-"));

describe("lock race stress", () => {
  it("does not fake reentrancy between managers in one process", () => {
    const root = dir();
    const a = new AutoSkillManager({ dir: root, mode: "manual" });
    const b = new AutoSkillManager({ dir: root, mode: "manual", lockTimeoutMs: 20 });
    // The first manager holds a nested lock through a public mutation. A
    // second manager must never reuse that in-memory token.
    a.execute({ action: "create", name: "a", description: "a", instructions });
    b.execute({ action: "create", name: "b", description: "b", instructions });
    expect(existsSync(join(root, "b"))).toBe(true);
  });

  it("leaves no lock directories after randomized sequential operations", () => {
    const root = dir();
    const m = new AutoSkillManager({ dir: root, mode: "manual" });
    for (let i = 0; i < 250; i++) {
      const name = `s-${i}`;
      m.execute({ action: "create", name, description: "x", instructions });
      const viewed: any = m.execute({ action: "view", name });
      m.execute({ action: "patch", name, instructions: `${instructions}${i}`, expected_revision: viewed.revision });
    }
    const locks = join(root, ".history", "locks");
    expect(readdirSync(locks)).toEqual([]);
  });

  it("does not silently delete unrelated files when lock metadata is damaged", () => {
    const root = dir();
    const lock = join(root, ".history", "locks", "x.lock");
    mkdirSync(lock, { recursive: true });
    // A live PID with a malformed owner is ambiguous and must fail closed.
    writeFileSync(join(lock, "owner.json"), JSON.stringify({ pid: process.pid, token: "unknown" }));
    const m = new AutoSkillManager({ dir: root, mode: "manual", lockTimeoutMs: 20 });
    expect(() => m.execute({ action: "create", name: "x", description: "x", instructions })).toThrow(/timed out/);
    expect(existsSync(lock)).toBe(true);
  });
});
