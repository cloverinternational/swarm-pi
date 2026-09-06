import { afterEach, describe, expect, it } from "vitest";
import { existsSync, mkdirSync, mkdtempSync, readFileSync, rmSync, utimesSync, writeFileSync } from "node:fs";
import { join } from "node:path";
import { tmpdir } from "node:os";
import { MIGRATION_MARKER, migrateLegacyConfig } from "../../../../.pi/lib/swarm-configmigrate.ts";

const roots: string[] = [];
const home = () => { const p = mkdtempSync(join(tmpdir(), "swarm-migrate-")); roots.push(p); return p; };
afterEach(() => { while (roots.length) rmSync(roots.pop()!, { recursive: true, force: true }); });
const put = (path: string, data: string) => { mkdirSync(join(path, ".."), { recursive: true }); writeFileSync(path, data); };

describe("configmigrate port (migrate.go)", () => {
  it("is a no-op without a legacy root but still creates the config dir", () => {
    const h = home();
    expect(migrateLegacyConfig({ home: h, env: {} })).toEqual({ moved: [], skipped: [], conflicts: [] });
    expect(existsSync(join(h, ".swarm", "config"))).toBe(true);
    expect(existsSync(join(h, ".swarm", "config", MIGRATION_MARKER))).toBe(false);
  });

  it("merges legacy skills/oauth/config, prefers existing destinations, and writes the marker once", () => {
    const h = home();
    put(join(h, ".swarmos", "skills", "dup", "SKILL.md"), "legacy dup");
    put(join(h, ".swarmos", "skills", "only-legacy", "SKILL.md"), "legacy only");
    put(join(h, ".swarmos", "skills", "junk.bak"), "skip me");
    put(join(h, ".swarm", "skills", "dup", "SKILL.md"), "canonical dup");
    put(join(h, ".swarmos", "providers.json"), "{\"root\":1}");
    put(join(h, ".swarmos", "config", "providers.json"), "{\"config\":22222}");
    put(join(h, ".swarmos", "xai_oauth.json"), "{\"token\":{}}");
    put(join(h, ".swarmos", "oauth.json"), "{\"anthropic\":true}");
    put(join(h, ".swarmos", "config", "oauth", "openai.json"), "{}");
    const report = migrateLegacyConfig({ home: h, env: {} });
    expect(readFileSync(join(h, ".swarm", "skills", "dup", "SKILL.md"), "utf8")).toBe("canonical dup");
    expect(readFileSync(join(h, ".swarm", "skills", "only-legacy", "SKILL.md"), "utf8")).toBe("legacy only");
    expect(existsSync(join(h, ".swarm", "skills", "junk.bak"))).toBe(false);
    // pickBest: larger candidate wins.
    expect(readFileSync(join(h, ".swarm", "config", "providers.json"), "utf8")).toBe("{\"config\":22222}");
    expect(existsSync(join(h, ".swarm", "config", "oauth", "xai.json"))).toBe(true);
    expect(existsSync(join(h, ".swarm", "config", "oauth", "anthropic.json"))).toBe(true);
    expect(existsSync(join(h, ".swarm", "config", "oauth", "openai.json"))).toBe(true);
    expect(report.conflicts).toEqual([join(h, ".swarm", "skills", "dup", "SKILL.md")]);
    expect(report.skipped).toEqual([join(h, ".swarmos", "skills", "junk.bak")]);
    expect(readFileSync(join(h, ".swarm", "config", MIGRATION_MARKER), "utf8")).toBe("migrated from .swarmos\n");
    // Marker short-circuits: later legacy additions are ignored.
    put(join(h, ".swarmos", "skills", "late", "SKILL.md"), "late");
    expect(migrateLegacyConfig({ home: h, env: {} })).toEqual({ moved: [], skipped: [], conflicts: [] });
    expect(existsSync(join(h, ".swarm", "skills", "late"))).toBe(false);
  });

  it("honours SWARM_HOME as the canonical root", () => {
    const h = home(); const root = join(h, "custom-root");
    put(join(h, ".swarmos", "skills", "s", "SKILL.md"), "x");
    migrateLegacyConfig({ home: h, env: { SWARM_HOME: root } });
    expect(readFileSync(join(root, "skills", "s", "SKILL.md"), "utf8")).toBe("x");
    expect(existsSync(join(root, "config", MIGRATION_MARKER))).toBe(true);
    expect(existsSync(join(h, ".swarm"))).toBe(false);
  });

  it("breaks size ties by modification time", () => {
    const h = home();
    put(join(h, ".swarmos", "hooks.json"), "aaaa"); put(join(h, ".swarmos", "config", "hooks.json"), "bbbb");
    utimesSync(join(h, ".swarmos", "hooks.json"), new Date(1_700_000_000_000), new Date(1_700_000_000_000));
    utimesSync(join(h, ".swarmos", "config", "hooks.json"), new Date(1_800_000_000_000), new Date(1_800_000_000_000));
    migrateLegacyConfig({ home: h, env: {} });
    expect(readFileSync(join(h, ".swarm", "config", "hooks.json"), "utf8")).toBe("bbbb");
  });
});
