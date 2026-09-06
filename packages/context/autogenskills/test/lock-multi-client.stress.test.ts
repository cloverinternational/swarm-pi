import { describe, expect, it } from "vitest";
import { AutoSkillManager } from "../src/index.js";
import { mkdtempSync, readFileSync, readdirSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { spawn } from "node:child_process";
import { fileURLToPath } from "node:url";

const root = fileURLToPath(new URL("..", import.meta.url));
const dist = join(root, "dist", "index.js");
const instructions = "A reusable production procedure. ".repeat(8);

function clientScript() {
  return `
    import { AutoSkillManager } from ${JSON.stringify(dist)};
    const [dir, client, rounds] = process.argv.slice(1);
    const m = new AutoSkillManager({ dir, mode: "manual", lockTimeoutMs: 5000 });
    const instructions = ${JSON.stringify(instructions)};
    let ok = 0, errors = [];
    for (let i = 0; i < Number(rounds); i++) {
      const name = "shared-" + (i % 4);
      try {
        const viewed = m.execute({ action: "view", name });
        m.execute({ action: "patch", name, instructions: instructions + client + ":" + i, expected_revision: viewed.revision });
        ok++;
      } catch (e) { errors.push(String(e)); }
    }
    process.stdout.write(JSON.stringify({ client, ok, errors }));
  `;
}

function runClient(dir: string, client: number, rounds: number): Promise<{ client: string; ok: number; errors: string[] }> {
  return new Promise((resolve, reject) => {
    const p = spawn(process.execPath, ["--input-type=module", "-e", clientScript(), dir, String(client), String(rounds)], { stdio: ["ignore", "pipe", "pipe"] });
    let out = "", err = "";
    p.stdout.on("data", b => out += b);
    p.stderr.on("data", b => err += b);
    p.on("error", reject);
    p.on("close", code => {
      if (code !== 0) reject(new Error(`client ${client} exited ${code}: ${err}`));
      else { try { resolve(JSON.parse(out)); } catch { reject(new Error(`bad client output: ${out} ${err}`)); } }
    });
  });
}

describe("multi-client lock stress", () => {
  it("survives many independent clients contending on shared skills", async () => {
    const dir = mkdtempSync(join(tmpdir(), "autogen-many-clients-"));
    const bootstrap = new AutoSkillManager({ dir, mode: "manual" });
    for (let i = 0; i < 4; i++) bootstrap.execute({ action: "create", name: `shared-${i}`, description: "shared", instructions });
    const clients = 12;
    const results = await Promise.all(Array.from({ length: clients }, (_, i) => runClient(dir, i, 12)));
    const failures = results.flatMap(r => r.errors.map(e => `${r.client}: ${e}`));

    // A real client may lose an optimistic revision race, but must never see
    // lock ownership errors, leaked staging state, or malformed history.
    expect(failures.filter(e => /lock|ownership|stage|history|ENOENT|JSON/i.test(e))).toEqual([]);
    expect(results.reduce((n, r) => n + r.ok, 0), JSON.stringify(results, null, 2)).toBeGreaterThan(0);
    const locks = join(dir, ".history", "locks");
    expect(readdirSync(locks)).toEqual([]);

    for (const name of ["shared-0", "shared-1", "shared-2", "shared-3"]) {
      const head = readFileSync(join(dir, ".history", "heads", name), "utf8").trim();
      expect(head).toMatch(/^[a-f0-9]{64}$/);
      expect(readFileSync(join(dir, name, "SKILL.md"), "utf8")).toContain("shared");
    }
  }, 30000);
});
