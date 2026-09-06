import { describe, expect, it } from "vitest";
import { mkdirSync, mkdtempSync, writeFileSync } from "node:fs";
import { join } from "node:path";
import { tmpdir } from "node:os";
import { generateAvailableSkillsXML, generateRankedAvailableSkillsXML, rankSkillsForContext, SkillLoader } from "../src/index.js";
const skill=(root:string,name:string,body:string)=>{const d=join(root,name);mkdirSync(d,{recursive:true});writeFileSync(join(d,"SKILL.md"),`---\nname: ${name}\ndescription: ${name} skill\n---\n${body}`);return d};
describe("Swarm skill loader",()=>{
 it("applies Swarm last-wins discovery order (project overrides managed) and discovers packages/support files",()=>{const root=mkdtempSync(join(tmpdir(),"skills-")),managed=join(root,"managed"),project=join(root,"project"),projectSkills=join(project,".swarm","skills");skill(managed,"same","managed");skill(projectSkills,"same","project");const d=join(projectSkills,"extra");mkdirSync(join(d,"references"),{recursive:true});writeFileSync(join(d,"SKILL.md"),"---\nname: extra\ndescription: extra\n---\nbody");writeFileSync(join(d,"references","x.txt"),"bytes");const r=new SkillLoader({cwd:project,managedDir:managed,installDir:join(root,"install"),home:root}).load();expect(r.skills.find(s=>s.name==="same")?.instructions).toBe("project");expect(r.skills.find(s=>s.name==="extra")?.supportFiles.some(x=>x.endsWith("x.txt"))).toBe(true);});
 it("supports closed allowlists",()=>{const root=mkdtempSync(join(tmpdir(),"skills-"));skill(root,"one","a");skill(root,"two","b");const r=new SkillLoader({closed:true,cliPaths:[root],allowedNames:["two"]}).load();expect(r.skills.map(s=>s.name)).toEqual(["two"]);});
 it("emits available_skills metadata without instruction bodies",()=>{const root=mkdtempSync(join(tmpdir(),"skills-"));skill(root,"prompt-skill","private instructions");const loaded=new SkillLoader({closed:true,cliPaths:[root]}).load().skills;const xml=generateAvailableSkillsXML(loaded);expect(xml).toContain("<available_skills>");expect(xml).toContain("<name>prompt-skill</name>");expect(xml).toContain("<location>");expect(xml).not.toContain("private instructions");});
 it("ranks request-relevant metadata and bounds prompt exposure",()=>{const root=mkdtempSync(join(tmpdir(),"skills-"));for(let i=0;i<70;i++)skill(root,i===69?"terminal-recording":`skill-${String(i).padStart(2,"0")}`,"private");const loaded=new SkillLoader({closed:true,cliPaths:[root]}).load().skills;const ranked=rankSkillsForContext(loaded,"create a terminal recording");expect(ranked[0].name).toBe("terminal-recording");const xml=generateRankedAvailableSkillsXML(ranked,3,12000);expect((xml.match(/<skill>/g)??[])).toHaveLength(3);expect(xml).toContain("67 additional skill(s) omitted");expect(xml).not.toContain("private");});
 it("mirrors Swarm discovery roots: project .claude/skills yes, .pi/skills no, archive + deep-hidden skipped, nested skills found, last root wins",()=>{
  const root=mkdtempSync(join(tmpdir(),"skills-")),home=join(root,"home"),project=join(root,"project");
  skill(join(project,".claude","skills"),"claude-side","claude");
  skill(join(project,".pi","skills"),"pi-side","pi");
  skill(join(home,".swarm","skills"),"shared","user-copy");
  skill(join(project,".swarm","skills"),"shared","project-copy");
  skill(join(home,".swarm","skills","autogen"),"auto","autogen body");
  skill(join(home,".swarm","skills","autogen","archive"),"retired","archived body");
  // parser.go: hidden directories are skipped only BELOW the root; a hidden
  // entry directly under a search root is still walked.
  skill(join(home,".swarm","skills",".hidden"),"hidden","hidden body");
  skill(join(home,".swarm","skills","outer",".deep-hidden"),"deep-hidden","deep hidden body");
  skill(join(home,".swarm","skills","outer"),"outer","outer body");
  skill(join(home,".swarm","skills","outer","inner"),"inner","inner body");
  skill(join(home,".swarmos","skills"),"legacy-install","install body");
  skill(join(home,".claude","commands"),"cmd","command body");
  const r=new SkillLoader({cwd:project,home,builtinDir:null}).load();
  const names=r.skills.map(s=>s.name);
  expect(names).toEqual(["auto","claude-side","cmd","hidden","inner","legacy-install","outer","shared"]);
  expect(r.skills.find(s=>s.name==="shared")?.instructions).toBe("project-copy");
  expect(r.skills.find(s=>s.name==="auto")?.source).toBe("autogen");
  expect(r.skills.find(s=>s.name==="claude-side")?.source).toBe("project");
  expect(r.skills.find(s=>s.name==="legacy-install")?.source).toBe("install");
  expect(r.searchPaths.map(p=>p.path)).toEqual([
    join(home,".swarmos","skills"), join(home,".swarmos","skills","skills"),
    join(home,".claude","skills"), join(home,".claude","commands"), join(home,".swarm","skills"),
    join(project,".claude","skills"), join(project,".swarm","skills"),
  ]);
 });
});
