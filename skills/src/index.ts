import { existsSync, readdirSync, readFileSync, statSync, watch, type FSWatcher } from "node:fs";
import { join, relative, resolve } from "node:path";

export type SkillSource = "managed" | "install" | "project" | "user" | "autogen" | "cli";
export interface LoadedSkill { name: string; description: string; instructions: string; dir: string; filePath: string; source: SkillSource; precedence: number; supportFiles: string[]; disableModelInvocation?: boolean; }
export interface SkillDiagnostic { path: string; message: string; }
export interface SkillLoaderOptions { cwd?: string; home?: string; installDir?: string; managedDir?: string; autogenDir?: string; cliPaths?: string[]; closed?: boolean; allowedNames?: string[]; }
export interface SkillLoadResult { skills: LoadedSkill[]; diagnostics: SkillDiagnostic[]; searchPaths: Array<{ path: string; source: SkillSource; precedence: number }>; }

const MAX_NAME = 64, MAX_DESC = 1024;
const precedence: Record<SkillSource, number> = { managed: 600, cli: 500, install: 400, project: 300, user: 200, autogen: 100 };
const sourceFor = (path: string, roots: Array<{ root: string; source: SkillSource }>): SkillSource => roots.find(r => { const p=resolve(path), root=resolve(r.root); return p===root || p.startsWith(root+"/"); })?.source ?? "user";
const parseFrontmatter = (raw: string): { fields: Record<string,string>; body: string } => {
  const m = raw.match(/^---\r?\n([\s\S]*?)\r?\n---\r?\n?([\s\S]*)$/);
  if (!m) return { fields: {}, body: raw };
  const fields: Record<string,string> = {};
  for (const line of m[1].split(/\r?\n/)) { const i=line.indexOf(":"); if (i>0) fields[line.slice(0,i).trim()]=line.slice(i+1).trim().replace(/^['"]|['"]$/g,""); }
  return { fields, body: m[2] };
};
const validName = (name: string) => name.length>0 && name.length<=MAX_NAME && /^[a-z0-9-]+$/.test(name) && !name.startsWith("-") && !name.endsWith("-") && !name.includes("--");
const walk = (root: string, out: string[] = []): string[] => {
  if (!existsSync(root)) return out;
  let entries; try { entries=readdirSync(root,{withFileTypes:true}); } catch { return out; }
  if (entries.some(e=>e.name==="SKILL.md" && (e.isFile() || e.isSymbolicLink()))) { out.push(join(root,"SKILL.md")); return out; }
  for (const e of entries) { if (e.name.startsWith(".") || e.name==="node_modules") continue; const p=join(root,e.name); try { if (e.isDirectory() || (e.isSymbolicLink() && statSync(p).isDirectory())) walk(p,out); } catch {} }
  return out;
};
const packageFiles = (dir: string): string[] => { const out:string[]=[]; const roots=["references","templates","scripts","assets"]; for(const root of roots){ const p=join(dir,root); if(existsSync(p)) walkFiles(p,out); } const hooks=join(dir,"hooks.json"); if(existsSync(hooks)) out.push(hooks); return out; };
const walkFiles = (root:string,out:string[]) => { let es; try{es=readdirSync(root,{withFileTypes:true});}catch{return;} for(const e of es){const p=join(root,e.name); try{if(e.isDirectory())walkFiles(p,out); else if(e.isFile())out.push(p);}catch{}} };

export class SkillLoader {
  private watchers: FSWatcher[] = [];
  constructor(private readonly options: SkillLoaderOptions = {}) {}
  paths(): Array<{path:string;source:SkillSource;precedence:number}> {
    const cwd=resolve(this.options.cwd ?? process.cwd()), home=this.options.home ?? process.env.HOME ?? cwd;
    const rows:Array<{path:string;source:SkillSource;precedence:number}> = [];
    const add=(path:string,source:SkillSource)=>{ if(path) rows.push({path:resolve(path),source,precedence:precedence[source]}); };
    if (!this.options.closed) {
      add(this.options.managedDir ?? process.env.SWARM_MANAGED_SKILLS_DIR ?? "", "managed");
      for(const p of this.options.cliPaths ?? []) add(p,"cli");
      add(this.options.installDir ?? join(home,".swarm","skills"),"install");
      add(join(cwd,".swarm","skills"),"project"); add(join(cwd,".pi","skills"),"project"); add(join(cwd,".agents","skills"),"project");
      add(join(home,".claude","skills"),"user"); add(join(home,".claude","commands"),"user"); add(join(home,".swarm","skills"),"user"); add(join(home,".agents","skills"),"user");
      add(this.options.autogenDir ?? join(home,".swarm","skills","autogen"),"autogen");
    } else for(const p of this.options.cliPaths ?? []) add(p,"cli");
    return rows.filter((r,i,a)=>a.findIndex(x=>x.path===r.path)===i);
  }
  load(): SkillLoadResult {
    const paths=this.paths(), diagnostics:SkillDiagnostic[]=[]; const selected=new Map<string,LoadedSkill>();
    for(const spec of paths){ for(const file of walk(spec.path)){ const dir=resolve(file,".."); let raw; try{raw=readFileSync(file,"utf8");}catch(e){diagnostics.push({path:file,message:String(e)});continue;} const {fields,body}=parseFrontmatter(raw); const name=fields.name || (dir.split("/").pop() ?? ""); const description=fields.description ?? ""; if(!validName(name)){diagnostics.push({path:file,message:`invalid skill name ${name}`});continue;} if(description.length>MAX_DESC){diagnostics.push({path:file,message:"description exceeds 1024 characters"});continue;} const skill:LoadedSkill={name,description,instructions:body.trimEnd(),dir,filePath:file,source:spec.source,precedence:spec.precedence,supportFiles:packageFiles(dir),disableModelInvocation:fields["disable-model-invocation"] === "true"}; const prior=selected.get(name); if(!prior || skill.precedence>=prior.precedence) selected.set(name,skill); } }
    let skills=[...selected.values()].sort((a,b)=>a.name.localeCompare(b.name)); if(this.options.closed && this.options.allowedNames) skills=skills.filter(s=>this.options.allowedNames!.includes(s.name)); return {skills,diagnostics,searchPaths:paths};
  }
  find(name:string):LoadedSkill|undefined{return this.load().skills.find(s=>s.name===name)}
  watch(onChange:(result:SkillLoadResult)=>void):()=>void { this.closeWatchers(); for(const p of this.paths()){ if(!existsSync(p.path)) continue; try{this.watchers.push(watch(p.path,{recursive:true},()=>onChange(this.load())))}catch{} } return ()=>this.closeWatchers(); }
  closeWatchers(){for(const w of this.watchers)w.close();this.watchers=[];}
}
export { parseFrontmatter, validName };
