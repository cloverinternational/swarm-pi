/**
 * Port of swarm-sdk internal/configmigrate (migrate.go): the one-time,
 * idempotent merge of the legacy `~/.swarmos` root into the canonical
 * `~/.swarm` root (honouring SWARM_HOME) that BOTH `swarm -p` (headless
 * main.go) and the TUI run before anything else.
 *
 * It is wire-relevant: `~/.swarmos/skills/**` is copied into
 * `~/.swarm/skills/**` (dest wins), and under Swarm's last-root-wins skill
 * discovery the `~/.swarm` copy is the one whose absolute path reaches the
 * model in `<location>`. OAuth files migrate to `config/oauth/<provider>.json`,
 * which gates x_search / xai_web_search. Everything else is carried too so
 * the on-disk state Pi leaves behind is the state Swarm would leave behind.
 *
 * Semantics mirrored exactly: marker `config/.migrated_from_swarmos` short-
 * circuits; skip names containing ".bak"/".corrupt"/".clobbered" or ending in
 * "_empty"; single files pick the largest (tie: newest) of root/config
 * candidates; existing destinations always win; directories are merged
 * file-by-file; paths.In/Config/SkillsDir/ConversationsDir/VaultDir create
 * their directories (0700) as a side effect even when nothing migrates.
 */
import { existsSync, mkdirSync, readdirSync, readFileSync, renameSync, statSync, writeFileSync } from "node:fs";
import { basename, dirname, join, relative } from "node:path";

export const LEGACY_DIR_NAME = ".swarmos";
export const MIGRATION_MARKER = ".migrated_from_swarmos";

export interface MigrationReport { moved: string[]; skipped: string[]; conflicts: string[] }
export interface MigrateOptions { home?: string; env?: NodeJS.ProcessEnv }

const ensureDir = (dir: string) => { try { mkdirSync(dir, { recursive: true, mode: 0o700 }); } catch { /* paths.ensureDir swallows */ } return dir; };
/** paths.Root(): SWARM_HOME verbatim, else ~/.swarm. */
export const swarmRoot = (home: string, env: NodeJS.ProcessEnv = process.env) => env.SWARM_HOME || join(home, ".swarm");

const shouldSkip = (name: string) => {
  const base = basename(name);
  return base.includes(".bak") || base.includes(".corrupt") || base.includes(".clobbered") || base.endsWith("_empty");
};
const appendUnique = (list: string[], value: string) => { if (!list.includes(value)) list.push(value); };
const statOrNull = (path: string) => { try { return statSync(path); } catch { return null; } };

/** atomicfile.Write: temp file in the same directory + rename, 0600. */
function copyToDest(src: string, dest: string) {
  const data = readFileSync(src);
  mkdirSync(dirname(dest), { recursive: true, mode: 0o700 });
  const temporary = join(dirname(dest), `.${basename(dest)}.${process.pid}.${Date.now()}.tmp`);
  writeFileSync(temporary, data, { mode: 0o600 });
  renameSync(temporary, dest);
}

function moveOne(src: string, dest: string, report: MigrationReport) {
  if (statOrNull(dest)) { appendUnique(report.conflicts, dest); return; }
  try { copyToDest(src, dest); } catch { return; }
  appendUnique(report.moved, dest);
}

function pickBest(candidates: string[], report: MigrationReport): string {
  const valid: Array<{ path: string; size: number; mod: number }> = [];
  for (const path of candidates) {
    if (shouldSkip(path)) { appendUnique(report.skipped, path); continue; }
    const info = statOrNull(path);
    if (!info || info.isDirectory()) continue;
    valid.push({ path, size: info.size, mod: info.mtimeMs });
  }
  if (!valid.length) return "";
  valid.sort((a, b) => b.size - a.size || b.mod - a.mod);
  return valid[0].path;
}

function* walkFiles(root: string): Generator<string> {
  let entries; try { entries = readdirSync(root, { withFileTypes: true }).sort((a, b) => (a.name < b.name ? -1 : a.name > b.name ? 1 : 0)); } catch { return; }
  for (const entry of entries) {
    const path = join(root, entry.name);
    if (entry.isDirectory()) yield* walkFiles(path);
    else yield path;
  }
}

function oauthProvider(name: string, inOAuthDir: boolean): string | undefined {
  if (!name.endsWith(".json")) return undefined;
  if (inOAuthDir) return name.slice(0, -".json".length);
  if (name === "oauth.json") return "anthropic";
  if (name.endsWith("_oauth.json")) return name.slice(0, -"_oauth.json".length);
  return undefined;
}

/** configmigrate.Migrate. Throws only when the marker cannot be written. */
export function migrateLegacyConfig(options: MigrateOptions = {}): MigrationReport {
  const report: MigrationReport = { moved: [], skipped: [], conflicts: [] };
  const env = options.env ?? process.env;
  const home = options.home ?? env.HOME ?? "";
  const root = swarmRoot(home, env);
  const inRoot = (...parts: string[]) => { const p = join(root, ...parts); ensureDir(dirname(p)); return p; };
  const marker = inRoot("config", MIGRATION_MARKER);
  if (statOrNull(marker)) return report;
  if (!home) return report;
  const src = join(home, LEGACY_DIR_NAME);
  const srcInfo = statOrNull(src);
  if (!srcInfo || !srcInfo.isDirectory()) return report;

  // collectSkips
  for (const path of walkFiles(src)) if (shouldSkip(path)) report.skipped.push(path);

  // migrateSingleFiles (paths.Config() and paths.In() ensure directories)
  const config = () => ensureDir(join(root, "config"));
  const pair = (name: string) => [join(src, name), join(src, "config", name)];
  const concerns: Array<[string, string[]]> = [
    [join(config(), "providers.json"), pair("providers.json")],
    [join(config(), "credentials.json"), pair("credentials.json")],
    [join(config(), "hooks.json"), pair("hooks.json")],
    [join(config(), "mcp_servers.json"), pair("mcp_servers.json")],
    [join(config(), "agent_profiles.json"), pair("agent_profiles.json")],
    [join(config(), "config.yaml"), pair("config.yaml")],
    [join(config(), "system_prompts.yaml"), pair("system_prompts.yaml")],
    [join(config(), "custom_agents.yaml"), pair("custom_agents.yaml")],
    [join(ensureDir(root), "a2a-registry.sqlite"), pair("a2a-registry.sqlite")],
    [inRoot("tui_accounts.json"), [join(src, "tui_accounts.json")]],
    [inRoot("cloud.json"), [join(src, "cloud.json")]],
    [inRoot("cloud_tokens.json"), [join(src, "cloud_tokens.json")]],
  ];
  for (const [dest, candidates] of concerns) {
    const best = pickBest(candidates, report);
    if (best) moveOne(best, dest, report);
  }

  // migrateOAuth
  for (const dir of [src, join(src, "config"), join(src, "config", "oauth")]) {
    let entries; try { entries = readdirSync(dir, { withFileTypes: true }); } catch { continue; }
    const inOAuthDir = basename(dir) === "oauth";
    for (const entry of entries) {
      if (entry.isDirectory()) continue;
      const full = join(dir, entry.name);
      if (shouldSkip(entry.name)) { appendUnique(report.skipped, full); continue; }
      const provider = oauthProvider(entry.name, inOAuthDir);
      if (provider === undefined) continue;
      moveOne(full, join(ensureDir(join(config(), "oauth")), `${provider}.json`), report);
    }
  }

  // migrateDirs (SkillsDir/ConversationsDir/VaultDir ensure; paths.In ensures the parent)
  const dirs: Array<[string, string]> = [
    [join(src, "skills"), ensureDir(join(root, "skills"))],
    [join(src, "conversations"), ensureDir(join(root, "conversations"))],
    [join(src, "vault"), ensureDir(join(root, "vault"))],
    [join(src, "swarms"), inRoot("swarms")],
    [join(src, "deepwiki"), inRoot("deepwiki")],
    [join(src, "findings"), inRoot("findings")],
    [join(src, "analytics-spool"), inRoot("analytics-spool")],
  ];
  for (const [from, to] of dirs) {
    const info = statOrNull(from);
    if (!info || !info.isDirectory()) continue;
    for (const path of walkFiles(from)) {
      if (shouldSkip(path)) { appendUnique(report.skipped, path); continue; }
      moveOne(path, join(to, relative(from, path)), report);
    }
  }

  const temporary = `${marker}.${process.pid}.${Date.now()}.tmp`;
  writeFileSync(temporary, `migrated from ${LEGACY_DIR_NAME}\n`, { mode: 0o600 });
  renameSync(temporary, marker);
  return report;
}

const RAN = Symbol.for("pi-swarm-configmigrate-ran");
/** configmigrate.Run: best-effort, once per process, never throws. */
export function runLegacyConfigMigration(options: MigrateOptions = {}): MigrationReport | undefined {
  const flag = globalThis as typeof globalThis & { [RAN]?: boolean };
  if (flag[RAN]) return undefined;
  flag[RAN] = true;
  try { return migrateLegacyConfig(options); } catch { return undefined; }
}

export const hasLegacyRoot = (home = process.env.HOME ?? "") => !!home && existsSync(join(home, LEGACY_DIR_NAME));
