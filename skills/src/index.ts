import { existsSync, readdirSync, readFileSync, realpathSync, statSync, watch, type FSWatcher } from "node:fs";
import { basename, dirname, join, relative, resolve } from "node:path";
import { fileURLToPath } from "node:url";
import { fatalValidationError, parseSkillMDContent } from "./skillmd.js";

export type SkillSource = "managed" | "install" | "project" | "user" | "autogen" | "cli" | "builtin";
/**
 * `filePath` is the on-disk SKILL.md. `location` is what the model sees in
 * `<location>`: Swarm renders embedded builtins as `builtin:<name>/SKILL.md`
 * and everything else as the absolute path.
 */
export interface LoadedSkill { name: string; description: string; instructions: string; dir: string; filePath: string; location: string; source: SkillSource; precedence: number; supportFiles: string[]; disableModelInvocation?: boolean; whenToUse?: string; category?: string; tags?: string[]; priority?: number; /** frontmatter `arguments` — names for {{name}} substitution (arguments.go). */ arguments?: string[]; }
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

/** prompt_xml.go escapeXML = html.EscapeString (note &#39; and &#34;, not &quot;). */
const esc = (value: string) => value.replace(/&/g, "&amp;").replace(/'/g, "&#39;").replace(/</g, "&lt;").replace(/>/g, "&gt;").replace(/"/g, "&#34;");
/** Go bytewise string ordering (sort.Strings / `<` on strings). */
const goLess = (a: string, b: string) => Buffer.compare(Buffer.from(a, "utf8"), Buffer.from(b, "utf8"));
/** registry.go classifySource names, as they appear in Skill.Source/LoadedFrom. */
const goSourceName = (source: SkillSource) => source === "managed" ? "policy" : source === "install" || source === "cli" ? "local" : source;
const skillEntryXML = (skill: LoadedSkill, description: string) => {
  const when = skill.whenToUse ? `    <when_to_use>${esc(skill.whenToUse)}</when_to_use>\n` : "";
  return `  <skill>\n    <name>${esc(skill.name)}</name>\n    <description>${esc(description)}</description>\n${when}    <location>${esc(skill.location ?? skill.filePath)}</location>\n  </skill>\n`;
};

/** prompt_xml.go GenerateAvailableSkillsXML (uncapped, full descriptions). */
export function generateAvailableSkillsXML(skills: LoadedSkill[]): string {
  if (!skills.length) return "";
  return `<available_skills>\n${skills.map(skill => skillEntryXML(skill, skill.description)).join("")}</available_skills>`;
}

export const MAX_AVAILABLE_SKILLS = 60;
export const MAX_AVAILABLE_SKILLS_CHARS = 12_000;
export const MAX_PROMPT_DESCRIPTION_RUNES = 240;
export const OMISSION_MARKER_RESERVE = 180;
/** prompt_xml.go truncatePromptDescription (rune-based). */
const promptDescription = (value: string) => {
  const runes = [...value.trim()];
  return runes.length <= MAX_PROMPT_DESCRIPTION_RUNES
    ? runes.join("")
    : `${runes.slice(0, MAX_PROMPT_DESCRIPTION_RUNES - 1).join("")}…`;
};
/** relevance.go relevanceTerms: FieldsFunc(!IsLetter && !IsDigit), >=3 runes, first-seen order. */
const relevanceTerms = (value: string) => [...new Set(
  value.split(/[^\p{L}\p{N}]+/u).filter(term => [...term].length >= 3),
)];
/** relevance.go RankForContext with no active skills (headless has none). */
export function rankSkillsForContext(skills: LoadedSkill[], query: string): LoadedSkill[] {
  const normalized = query.trim().toLowerCase();
  const terms = relevanceTerms(normalized);
  const score = (skill: LoadedSkill) => {
    const name = skill.name.toLowerCase();
    const description = promptDescription(skill.description).toLowerCase();
    const whenToUse = (skill.whenToUse ?? "").toLowerCase();
    const source = goSourceName(skill.source);
    const metadata = [skill.category ?? "", ...(skill.tags ?? []), source, source].join(" ").toLowerCase();
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
  // Registry.List() pre-sorts by priority desc, name asc; the ranking sort is stable.
  const listed = [...skills].sort((a, b) => ((b.priority ?? 0) - (a.priority ?? 0)) || goLess(a.name, b.name));
  return listed.sort((left, right) =>
    score(right) - score(left) ||
    (right.priority ?? 0) - (left.priority ?? 0) ||
    goLess(left.name.toLowerCase(), right.name.toLowerCase()),
  );
}
/** prompt_xml.go GenerateRankedAvailableSkillsXML — the budget is in BYTES. */
export function generateRankedAvailableSkillsXML(
  skills: LoadedSkill[],
  maxSkills = MAX_AVAILABLE_SKILLS,
  maxChars = MAX_AVAILABLE_SKILLS_CHARS,
): string {
  if (!skills.length) return "";
  if (maxSkills <= 0) maxSkills = skills.length;
  if (maxChars <= 0) maxChars = MAX_AVAILABLE_SKILLS_CHARS;
  const closing = "</available_skills>";
  let xml = "<available_skills>\n";
  let bytes = Buffer.byteLength(xml);
  let rendered = 0;
  for (const skill of skills) {
    if (rendered >= maxSkills) continue;
    const entry = skillEntryXML(skill, promptDescription(skill.description));
    const entryBytes = Buffer.byteLength(entry);
    if (rendered > 0 && bytes + entryBytes + closing.length + OMISSION_MARKER_RESERVE > maxChars) break;
    xml += entry; bytes += entryBytes;
    rendered++;
  }
  const omitted = skills.length - rendered;
  if (omitted > 0) xml += `  <!-- ${omitted} additional skill(s) omitted to bound prompt size; use SkillManage(action="list") for on-demand discovery -->\n`;
  return `${xml}${closing}`;
}

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

/**
 * skills.LoadSkill(dir) for one package: parse + fatal validation. Throws with
 * the loader's diagnostic message. Used by autogen refreshSkill, which
 * re-registers a single package without re-discovering every root.
 */
export function loadSkillFromDir(dir: string, source: SkillSource, precedence = Number.MAX_SAFE_INTEGER): LoadedSkill {
  const file = join(resolve(dir), "SKILL.md");
  const raw = readFileSync(file, "utf8");
  const parsed = parseSkillMDContent(raw);
  const m = parsed.metadata; const fatal = fatalValidationError(m); if (fatal) throw new Error(fatal);
  return { name: m.name, description: m.description, instructions: parsed.instructions, dir: resolve(dir), filePath: file, location: source === "builtin" ? builtinLocation(m.name) : file, source, precedence, supportFiles: packageFiles(resolve(dir)), disableModelInvocation: m.disableModelInvocation, whenToUse: m.whenToUse || undefined, category: m.category || undefined, tags: m.tags.length ? m.tags : undefined, priority: m.priority || undefined, arguments: m.arguments.length ? m.arguments : undefined };
}

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
    for(const spec of paths){ for(const file of walk(spec.path)){
      const dir=resolve(file,"..");
      let raw; try{raw=readFileSync(file,"utf8");}catch(e){diagnostics.push({path:file,message:String(e)});continue;}
      // LoadSkillWithValidation: parse like yaml.v3, then the fatal gate.
      let parsed; try{parsed=parseSkillMDContent(raw);}catch(e){diagnostics.push({path:file,message:(e as Error).message});continue;}
      const m=parsed.metadata; const fatal=fatalValidationError(m); if(fatal){diagnostics.push({path:file,message:fatal});continue;}
      const autogenRoot=resolve(process.env.SWARM_HOME || join(this.options.home ?? process.env.HOME ?? cwd,".swarm"),"skills","autogen");
      const source:SkillSource=spec.source!=="managed"&&spec.source!=="builtin"&&spec.source!=="cli"&&(dir===autogenRoot||dir.startsWith(autogenRoot+"/"))?"autogen":spec.source;
      const skill:LoadedSkill={name:m.name,description:m.description,instructions:parsed.instructions,dir,filePath:file,location:spec.source==="builtin"?builtinLocation(m.name):file,source,precedence:spec.precedence,supportFiles:packageFiles(dir),disableModelInvocation:m.disableModelInvocation,whenToUse:m.whenToUse||undefined,category:m.category||undefined,tags:m.tags.length?m.tags:undefined,priority:m.priority||undefined,arguments:m.arguments.length?m.arguments:undefined};
      const prior=selected.get(m.name); if(!prior || skill.precedence>=prior.precedence) selected.set(m.name,skill);
    } }
    let skills=[...selected.values()].sort((a,b)=>a.name.localeCompare(b.name)); const allowed = this.options.allowedNames ?? this.options.allowedSkills; if (allowed) skills=skills.filter(s=>allowed.includes(s.name)); return {skills,diagnostics,searchPaths:paths};
  }
  find(name:string):LoadedSkill|undefined{return this.load().skills.find(s=>s.name===name)}
  watch(onChange:(result:SkillLoadResult)=>void):()=>void { this.closeWatchers(); for(const p of this.paths()){ if(!existsSync(p.path)) continue; try{this.watchers.push(watch(p.path,{recursive:true},()=>onChange(this.load())))}catch{} } return ()=>this.closeWatchers(); }
  closeWatchers(){for(const w of this.watchers)w.close();this.watchers=[];}
}
export { parseSkillMDContent, fatalValidationError, validateName, validateDescription, extractDescriptionFromMarkdown, MAX_DESCRIPTION_LENGTH, MAX_NAME_LENGTH } from "./skillmd.js";
