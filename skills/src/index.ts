import { existsSync, readdirSync, readFileSync, realpathSync, statSync, watch, type FSWatcher } from "node:fs";
import { basename, dirname, join, relative, resolve } from "node:path";
import { fileURLToPath } from "node:url";

export type SkillSource = "managed" | "install" | "project" | "user" | "autogen" | "cli" | "builtin";
/**
 * `filePath` is the on-disk SKILL.md. `location` is what the model sees in
 * `<location>`: Swarm renders embedded builtins as `builtin:<name>/SKILL.md`
 * and everything else as the absolute path.
 */
export interface LoadedSkill { name: string; description: string; instructions: string; dir: string; filePath: string; location: string; source: SkillSource; precedence: number; supportFiles: string[]; disableModelInvocation?: boolean; whenToUse?: string; category?: string; tags?: string[]; priority?: number; }
export interface SkillDiagnostic { path: string; message: string; }
export interface SkillLoaderOptions { cwd?: string; home?: string; installDir?: string; managedDir?: string; autogenDir?: string; builtinDir?: string | null; cliPaths?: string[]; closed?: boolean; allowedNames?: string[]; allowedSkills?: string[]; }
export interface SkillLoadResult { skills: LoadedSkill[]; diagnostics: SkillDiagnostic[]; searchPaths: Array<{ path: string; source: SkillSource; precedence: number }>; }

/**
 * Byte-identical mirrors of the skills Swarm embeds in its binary
 * (`swarm-sdk/internal/skills/builtins` plus the programmatic `loop` skill).
 * Registered first at the lowest precedence, exactly like Swarm's
 * `RegisterDefaultSkills(overwrite=false)`: any filesystem skill with the same
 * name replaces the builtin.
 */
export const DEFAULT_BUILTIN_SKILLS_DIR = resolve(dirname(fileURLToPath(import.meta.url)), "..", "builtins");
export const builtinLocation = (name: string) => `builtin:${name}/SKILL.md`;

/** Upstream-compatible progressive-disclosure index; bodies stay on disk. */
export function generateAvailableSkillsXML(skills: LoadedSkill[]): string {
  if (!skills.length) return "";
  const esc = (value: string) => value.replace(/&/g, "&amp;").replace(/</g, "&lt;").replace(/>/g, "&gt;").replace(/\"/g, "&quot;");
  return `<available_skills>\n${skills.map(skill => `  <skill>\n    <name>${esc(skill.name)}</name>\n    <description>${esc(skill.description)}</description>\n    <location>${esc(skill.location ?? skill.filePath)}</location>\n  </skill>`).join("\n")}\n</available_skills>`;
}

export const MAX_AVAILABLE_SKILLS = 60;
export const MAX_AVAILABLE_SKILLS_CHARS = 12_000;
export const MAX_PROMPT_DESCRIPTION_RUNES = 240;
const promptDescription = (value: string) => {
  const runes = [...value.trim()];
  return runes.length <= MAX_PROMPT_DESCRIPTION_RUNES
    ? runes.join("")
    : `${runes.slice(0, MAX_PROMPT_DESCRIPTION_RUNES - 1).join("")}…`;
};
const relevanceTerms = (value: string) => [...new Set(
  value.toLowerCase().split(/[^\p{L}\p{N}]+/u).filter(term => [...term].length >= 3),
)];
export function rankSkillsForContext(skills: LoadedSkill[], query: string): LoadedSkill[] {
  const normalized = query.trim().toLowerCase();
  const terms = relevanceTerms(normalized);
  const score = (skill: LoadedSkill) => {
    const name = skill.name.toLowerCase();
    const description = promptDescription(skill.description).toLowerCase();
    const whenToUse = (skill.whenToUse ?? "").toLowerCase();
    const metadata = [skill.category ?? "", ...(skill.tags ?? []), skill.location ?? skill.filePath, skill.source].join(" ").toLowerCase();
    let value = 0;
    if (normalized && name.includes(normalized)) value += 1000;
    if (normalized && `${description} ${whenToUse}`.includes(normalized)) value += 600;
    for (const term of terms) {
      if (name.includes(term)) value += 80;
      if (description.includes(term)) value += 30;
      if (whenToUse.includes(term)) value += 40;
      if (metadata.includes(term)) value += 10;
    }
    return value;
  };
  return [...skills].sort((left, right) =>
    score(right) - score(left) ||
    (right.priority ?? 0) - (left.priority ?? 0) ||
    left.name.toLowerCase().localeCompare(right.name.toLowerCase()),
  );
}
export function generateRankedAvailableSkillsXML(
  skills: LoadedSkill[],
  maxSkills = MAX_AVAILABLE_SKILLS,
  maxChars = MAX_AVAILABLE_SKILLS_CHARS,
): string {
  if (!skills.length) return "";
  const esc = (value: string) => value.replace(/&/g, "&amp;").replace(/</g, "&lt;").replace(/>/g, "&gt;").replace(/\"/g, "&quot;");
  const closing = "</available_skills>";
  let xml = "<available_skills>\n";
  let rendered = 0;
  for (const skill of skills) {
    if (rendered >= maxSkills) break;
    const when = skill.whenToUse ? `\n    <when_to_use>${esc(skill.whenToUse)}</when_to_use>` : "";
    const entry = `  <skill>\n    <name>${esc(skill.name)}</name>\n    <description>${esc(promptDescription(skill.description))}</description>${when}\n    <location>${esc(skill.location ?? skill.filePath)}</location>\n  </skill>\n`;
    if (rendered > 0 && xml.length + entry.length + closing.length + 160 > maxChars) break;
    xml += entry;
    rendered++;
  }
  const omitted = skills.length - rendered;
  if (omitted > 0) xml += `  <!-- ${omitted} additional skill(s) omitted to bound prompt size; use SkillManage(action="list") for on-demand discovery -->\n`;
  return `${xml}${closing}`;
}

const MAX_NAME = 64, MAX_DESC = 1024;
/**
 * Discovery order mirrors Swarm exactly (later roots overwrite earlier ones,
 * because Registry.DiscoverAll does `r.skills[name] = skill` unconditionally):
 *   builtins (RegisterDefaultSkills, overwrite=false)
 *   $SWARM_MANAGED_SKILLS_DIR            loader.go — prepended, so in practice LOWEST
 *   ~/.swarmos/skills, ~/.swarmos/skills/skills   loader.go installDir (TUI DefaultSkillsDir)
 *   ~/.claude/skills, ~/.claude/commands loader.go
 *   $SWARM_HOME|~/.swarm/skills           paths.SkillsDir() (autogen/ is a subtree)
 *   <cwd>/.claude/skills, <cwd>/.claude/commands, <cwd>/.swarm/skills
 *                                        skills_manager.go AddProjectSearchPaths (only if they exist)
 * Precedence is therefore the ordinal position of the root, not a per-source
 * rank. `source` is only a classification (registry.go classifySource).
 */
const sourceFor = (path: string, roots: Array<{ root: string; source: SkillSource }>): SkillSource => roots.find(r => { const p=resolve(path), root=resolve(r.root); return p===root || p.startsWith(root+"/"); })?.source ?? "user";
const parseFrontmatter = (raw: string): { fields: Record<string,string>; body: string } => {
  const m = raw.match(/^---\r?\n([\s\S]*?)\r?\n---\r?\n?([\s\S]*)$/);
  if (!m) return { fields: {}, body: raw };
  const fields: Record<string,string> = {};
  // Swarm builtin SKILL.md files use YAML block lists (`tags:\n  - a`). Fold
  // list items into the comma-separated form the rest of the loader expects so
  // the same file parses identically on both runtimes.
  let listKey: string | undefined;
  for (const line of m[1].split(/\r?\n/)) {
    const item = line.match(/^\s+-\s*(.*)$/);
    if (item && listKey) { const value = item[1].trim().replace(/^['"]|['"]$/g,""); fields[listKey] = fields[listKey] ? `${fields[listKey]},${value}` : value; continue; }
    if (/^\s/.test(line)) continue; // nested mapping we do not model
    const i=line.indexOf(":"); if (i<=0) { listKey = undefined; continue; }
    const key=line.slice(0,i).trim(), value=line.slice(i+1).trim().replace(/^['"]|['"]$/g,"");
    fields[key]=value; listKey = value === "" ? key : undefined;
  }
  return { fields, body: m[2] };
};
const validName = (name: string) => name.length>0 && name.length<=MAX_NAME && /^[a-z0-9-]+$/.test(name) && !name.startsWith("-") && !name.endsWith("-") && !name.includes("--");
/** parser.go skipDirs — "archive" holds skills the autogen curator retired. */
const SKIP_DIRS = new Set(["node_modules", ".git", ".svn", ".hg", "venv", ".venv", "__pycache__", ".mypy_cache", ".ruff_cache", "dist", "build", ".next", ".nuxt", ".output", "vendor", ".cache", ".tmp", "tmp", "archive"]);
/**
 * parser.go DiscoverSkills: recursive walk that follows directory symlinks,
 * skips tooling/hidden directories (except the root itself), keeps descending
 * below a directory that already holds a SKILL.md, and dedupes SKILL.md files
 * by canonical path. Unresolvable/unreadable directories are skipped silently.
 */
const walk = (rootDir: string, out: string[] = []): string[] => {
  const seenDirs = new Set<string>(), seenPaths = new Set<string>();
  const visit = (dir: string) => {
    let realDir: string; try { realDir = realpathSync(dir); } catch { return; }
    if (seenDirs.has(realDir)) return; seenDirs.add(realDir);
    // os.ReadDir returns entries sorted by filename; Node does not.
    let entries; try { entries = readdirSync(dir, { withFileTypes: true }).sort((a, b) => (a.name < b.name ? -1 : a.name > b.name ? 1 : 0)); } catch { return; }
    for (const e of entries) {
      const name = e.name, fullPath = join(dir, name);
      if (e.isDirectory() || e.isSymbolicLink()) {
        let isDir = false; try { isDir = statSync(fullPath).isDirectory(); } catch { continue; }
        if (!isDir) { if (name === "SKILL.md") addFile(fullPath); continue; }
        if (SKIP_DIRS.has(name)) continue;
        if (dir !== rootDir && name.startsWith(".")) continue;
        visit(fullPath); continue;
      }
      if (name === "SKILL.md") addFile(fullPath);
    }
  };
  const addFile = (fullPath: string) => {
    let canonical = fullPath; try { canonical = realpathSync(fullPath); } catch {}
    if (seenPaths.has(canonical)) return; seenPaths.add(canonical);
    out.push(fullPath);
  };
  visit(rootDir);
  return out;
};
const packageFiles = (dir: string): string[] => { const out:string[]=[]; const roots=["references","templates","scripts","assets"]; for(const root of roots){ const p=join(dir,root); if(existsSync(p)) walkFiles(p,out); } const hooks=join(dir,"hooks.json"); if(existsSync(hooks)) out.push(hooks); return out; };
export function resolveSkillFile(skill: LoadedSkill, filePath: string): string {
  const relativePath = filePath.trim();
  if (!relativePath || relativePath === "SKILL.md") return skill.filePath;
  if (relativePath.includes("\\") || relativePath.split("/").some(part => !part || part === "." || part === "..")) throw new Error("invalid skill file path");
  const candidate = resolve(skill.dir, relativePath);
  const allowed = new Set(skill.supportFiles.map(path => resolve(path)));
  if (!allowed.has(candidate)) throw new Error(`skill file not found: ${filePath}`);
  return candidate;
}
const walkFiles = (root:string,out:string[]) => { let es; try{es=readdirSync(root,{withFileTypes:true});}catch{return;} for(const e of es){const p=join(root,e.name); try{if(e.isDirectory())walkFiles(p,out); else if(e.isFile())out.push(p);}catch{}} };

export class SkillLoader {
  private watchers: FSWatcher[] = [];
  constructor(private readonly options: SkillLoaderOptions = {}) {}
  configure(policy: Pick<SkillLoaderOptions, "closed" | "allowedNames">) {
    this.options.closed = !!policy.closed;
    if (policy.allowedNames) this.options.allowedNames = [...policy.allowedNames];
  }
  paths(): Array<{path:string;source:SkillSource;precedence:number}> {
    const cwd=resolve(this.options.cwd ?? process.cwd()), home=this.options.home ?? process.env.HOME ?? cwd;
    const swarmRoot = process.env.SWARM_HOME || join(home, ".swarm");
    const rows:Array<{path:string;source:SkillSource;precedence:number}> = [];
    // Ordinal precedence: the LAST root to define a name wins (DiscoverAll).
    const add=(path:string,source:SkillSource)=>{ if(path) rows.push({path:resolve(path),source,precedence:rows.length}); };
    if (!this.options.closed) {
      if (this.options.builtinDir !== null) add(this.options.builtinDir ?? DEFAULT_BUILTIN_SKILLS_DIR,"builtin");
      // loader.go NewLoader: managed dir only when set, existing, a directory,
      // and SWARM_DISABLE_POLICY_SKILLS != "1". It is *prepended* to the search
      // paths, which under last-wins discovery makes it the lowest priority.
      const managed = this.options.managedDir ?? (process.env.SWARM_DISABLE_POLICY_SKILLS === "1" ? "" : process.env.SWARM_MANAGED_SKILLS_DIR ?? "");
      if (managed) { let ok = false; try { ok = statSync(managed).isDirectory(); } catch {} if (ok) add(managed, "managed"); }
      const installDir = this.options.installDir ?? join(home, ".swarmos", "skills");
      add(installDir, "install"); add(join(installDir, "skills"), "install");
      add(join(home,".claude","skills"),"user"); add(join(home,".claude","commands"),"user");
      add(join(swarmRoot,"skills"),"user");
      // A non-default autogen dir has no Swarm equivalent; the default one is
      // already covered by the ~/.swarm/skills subtree walk.
      if (this.options.autogenDir && resolve(this.options.autogenDir) !== resolve(swarmRoot, "skills", "autogen")) add(this.options.autogenDir, "autogen");
      // skills_manager.go AddProjectSearchPaths: only roots that exist.
      for (const p of [join(cwd,".claude","skills"), join(cwd,".claude","commands"), join(cwd,".swarm","skills")]) if (existsSync(p)) add(p, "project");
    }
    // Pi-only explicit roots (closed/allowlisted policy); no Swarm counterpart.
    for(const p of this.options.cliPaths ?? []) add(p,"cli");
    return rows.filter((r,i,a)=>a.findIndex(x=>x.path===r.path)===i);
  }
  load(): SkillLoadResult {
    const paths=this.paths(), diagnostics:SkillDiagnostic[]=[]; const selected=new Map<string,LoadedSkill>(); const cwd=resolve(this.options.cwd ?? process.cwd());
    // Swarm ParseSkillMDContent: instructions = strings.TrimSpace(body).
    for(const spec of paths){ for(const file of walk(spec.path)){ const dir=resolve(file,".."); let raw; try{raw=readFileSync(file,"utf8");}catch(e){diagnostics.push({path:file,message:String(e)});continue;} const {fields,body}=parseFrontmatter(raw); const name=fields.name || (dir.split("/").pop() ?? ""); const description=fields.description ?? ""; if(!validName(name)){diagnostics.push({path:file,message:`invalid skill name ${name}`});continue;} if(description.length>MAX_DESC){diagnostics.push({path:file,message:"description exceeds 1024 characters"});continue;} const autogenRoot=resolve(process.env.SWARM_HOME || join(this.options.home ?? process.env.HOME ?? cwd,".swarm"),"skills","autogen"); const source:SkillSource=spec.source!=="managed"&&spec.source!=="builtin"&&spec.source!=="cli"&&(dir===autogenRoot||dir.startsWith(autogenRoot+"/"))?"autogen":spec.source; const skill:LoadedSkill={name,description,instructions:body.trim(),dir,filePath:file,location:spec.source==="builtin"?builtinLocation(name):file,source,precedence:spec.precedence,supportFiles:packageFiles(dir),disableModelInvocation:fields["disable-model-invocation"] === "true",whenToUse:fields.when_to_use || fields["when-to-use"],category:fields.category,tags:fields.tags?.split(",").map(tag=>tag.trim()).filter(Boolean),priority:Number.isFinite(Number(fields.priority))?Number(fields.priority):undefined}; const prior=selected.get(name); if(!prior || skill.precedence>=prior.precedence) selected.set(name,skill); } }
    let skills=[...selected.values()].sort((a,b)=>a.name.localeCompare(b.name)); const allowed = this.options.allowedNames ?? this.options.allowedSkills; if (allowed) skills=skills.filter(s=>allowed.includes(s.name)); return {skills,diagnostics,searchPaths:paths};
  }
  find(name:string):LoadedSkill|undefined{return this.load().skills.find(s=>s.name===name)}
  watch(onChange:(result:SkillLoadResult)=>void):()=>void { this.closeWatchers(); for(const p of this.paths()){ if(!existsSync(p.path)) continue; try{this.watchers.push(watch(p.path,{recursive:true},()=>onChange(this.load())))}catch{} } return ()=>this.closeWatchers(); }
  closeWatchers(){for(const w of this.watchers)w.close();this.watchers=[];}
}
export { parseFrontmatter, validName };
