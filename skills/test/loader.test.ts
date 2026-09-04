import { describe, expect, it } from "vitest";
import { mkdirSync, mkdtempSync, writeFileSync } from "node:fs";
import { join } from "node:path";
import { tmpdir } from "node:os";
import { SkillLoader } from "../src/index.js";
const skill=(root:string,name:string,body:string)=>{const d=join(root,name);mkdirSync(d,{recursive:true});writeFileSync(join(d,"SKILL.md"),`---\nname: ${name}\ndescription: ${name} skill\n---\n${body}`);return d};
describe("Swarm skill loader",()=>{
 it("uses managed precedence and discovers packages/support files",()=>{const root=mkdtempSync(join(tmpdir(),"skills-")),managed=join(root,"managed"),project=join(root,"project"),projectSkills=join(project,".swarm","skills");skill(managed,"same","managed");skill(projectSkills,"same","project");const d=join(projectSkills,"extra");mkdirSync(join(d,"references"),{recursive:true});writeFileSync(join(d,"SKILL.md"),"---\nname: extra\ndescription: extra\n---\nbody");writeFileSync(join(d,"references","x.txt"),"bytes");const r=new SkillLoader({cwd:project,managedDir:managed,installDir:join(root,"install"),home:root}).load();expect(r.skills.find(s=>s.name==="same")?.instructions).toBe("managed");expect(r.skills.find(s=>s.name==="extra")?.supportFiles.some(x=>x.endsWith("x.txt"))).toBe(true);});
 it("supports closed allowlists",()=>{const root=mkdtempSync(join(tmpdir(),"skills-"));skill(root,"one","a");skill(root,"two","b");const r=new SkillLoader({closed:true,cliPaths:[root],allowedNames:["two"]}).load();expect(r.skills.map(s=>s.name)).toEqual(["two"]);});
});
