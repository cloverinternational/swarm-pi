import { it, expect } from "vitest";
import { mkdtempSync, rmSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { execFileSync } from "node:child_process";
import { rememberShared, searchShared } from "../../lib/state/shared-memory.ts";

it("shares repository notes across worktrees, isolates worktree notes, and exposes explicit global knowledge", () => {
  const root = mkdtempSync(join(tmpdir(), "memory-test-"));
  const old = process.env.PI_SWARM_MEMORY_DIR; process.env.PI_SWARM_MEMORY_DIR = join(root, "store");
  const repo = join(root, "repo"), wt = join(root, "wt"), other = join(root, "other");
  const git = (...args: string[]) => execFileSync("git", args, { stdio: "ignore" });
  try {
    git("init", repo); git("-C", repo, "-c", "user.name=Test", "-c", "user.email=test@example.invalid", "commit", "--allow-empty", "-m", "initial");
    git("-C", repo, "worktree", "add", "-b", "test", wt); git("init", other);
    rememberShared(repo, "repository", "shared fact"); rememberShared(repo, "worktree", "local fact"); rememberShared(repo, "global", "global fact");
    expect(searchShared(wt).map(x => x.text).sort()).toEqual(["global fact", "shared fact"]);
    expect(searchShared(other).map(x => x.text)).toEqual(["global fact"]);
    expect(searchShared(repo)).toHaveLength(3);
    expect(searchShared(repo, "", undefined, 0)).toEqual([]);
  } finally { if (old === undefined) delete process.env.PI_SWARM_MEMORY_DIR; else process.env.PI_SWARM_MEMORY_DIR = old; rmSync(root, { recursive: true, force: true }); }
});
