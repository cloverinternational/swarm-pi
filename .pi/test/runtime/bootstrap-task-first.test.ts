import {it,expect,vi} from "vitest";
import {mkdtempSync,rmSync} from "node:fs";import {tmpdir} from "node:os";import {join} from "node:path";
vi.mock("@earendil-works/pi-tui",()=>({
 SettingsList:class{},
 matchesKey:(data:string,key:string)=>key==="ctrl+d"&&(data==="\x04"||data.startsWith("ctrl+d:")),
 isKeyRepeat:(data:string)=>data==="ctrl+d:repeat",
 isKeyRelease:(data:string)=>data==="ctrl+d:release",
}));
vi.mock("../../lib/runtime/bootstrap-dispatch.ts",()=>({dispatchBootstrapHandoff:vi.fn(async()=>({content:[{type:"text",text:'{"status":"succeeded","results":[{"status":"succeeded"}]}'}]}))}));
import bootstrap from "../../../extensions/bootstrap/extension.ts";
import {dispatchBootstrapHandoff} from "../../lib/runtime/bootstrap-dispatch.ts";
import {writeBootstrapSettings} from "../../../packages/runtime/bootstrap/src/store.ts";
it("establishes a task even with omitted commitTasks and missing model",async()=>{
 const cwd=mkdtempSync(join(tmpdir(),"bootstrap-seed-"));const tools=new Map<string,any>();
 try{
  (globalThis as any)[Symbol.for("pi-swarm-task-manager")]={snapshot:()=>({tasks:[]})};
  bootstrap({registerCommand(){},on(){},registerTool:(t:any)=>tools.set(t.name,t),getActiveTools:()=>["TaskManage"],appendEntry(){}});
  const result=await tools.get("bootstrap").execute("call",{task:"Inspect Jev behavior"},new AbortController().signal,undefined,{cwd,sessionManager:{getEntries:()=>[]}});
  expect(dispatchBootstrapHandoff).toHaveBeenCalledWith("TaskManage",expect.objectContaining({operations:[expect.objectContaining({op:"create",status:"in_progress",active:true})]}),expect.anything(),expect.anything(),["TaskManage"]);
  expect(result.isError).toBe(true);expect(result.content[0].text).toContain("Task established");
 }finally{delete (globalThis as any)[Symbol.for("pi-swarm-task-manager")];rmSync(cwd,{recursive:true,force:true});}
});

it("toggles only the bootstrap requirement with Ctrl+D and reports it in the footer",async()=>{
 const cwd=mkdtempSync(join(tmpdir(),"bootstrap-toggle-"));
 const handlers=new Map<string,any[]>(),terminalListeners:any[]=[];
 const requestRender=vi.fn(),notify=vi.fn();
 const removers:any[]=[];
 const registerShortcut=vi.fn();
 const pi:any={registerCommand(){},registerTool(){},appendEntry(){},registerShortcut,on:(event:string,handler:any)=>handlers.set(event,[...(handlers.get(event)??[]),handler])};
 const ctx:any={cwd,mode:"headless",sessionManager:{getEntries:()=>[]},ui:{requestRender,notify,onTerminalInput:(listener:any)=>{terminalListeners.push(listener);const remove=vi.fn();removers.push(remove);return remove;}}};
 try{
  writeBootstrapSettings(cwd,"parallel","",true);
  bootstrap(pi);
  for(const start of handlers.get("session_start")??[])await start({},ctx);
  const footer=()=>(globalThis as any)[Symbol.for("pi-swarm-footer-segments")].get("bootstrap-requirement")();
  const before=handlers.get("before_agent_start")![0],gate=handlers.get("tool_call")![0];
  expect(registerShortcut).not.toHaveBeenCalled();
  // The requirement now defaults OFF for every session; the persisted enforce
  // flag only decides what Ctrl+D turns on. Toggle it on for this session:
  expect(footer()).toBeUndefined();
  expect(gate({toolName:"Bash"},ctx)).toBeUndefined();
  expect(before({systemPrompt:"base"},ctx)).toBeUndefined();
  expect(terminalListeners[0]("\x04")).toEqual({consume:true});
  expect(footer()).toBe("bootstrap:on");
  expect(gate({toolName:"Bash"},ctx)?.block).toBe(true);
  expect(before({systemPrompt:"base"},ctx)?.systemPrompt).toContain("Memory enforcement is ON");
  expect(gate({toolName:"TaskManage"},ctx)).toBeUndefined();

  // Re-entering session_start (including reload/session replacement) disposes
  // the old raw listener before installing one replacement.
  for(const start of handlers.get("session_start")??[])await start({},ctx);
  expect(removers[0]).toHaveBeenCalledTimes(1);
  expect(terminalListeners).toHaveLength(2);
  const activeInput=terminalListeners[1];

  // A re-entered session also resets the requirement to its off default.
  expect(footer()).toBeUndefined();
  expect(activeInput("x")).toBeUndefined();
  expect(activeInput("\x04")).toEqual({consume:true});
  expect(footer()).toBe("bootstrap:on");
  expect(gate({toolName:"Bash"},ctx)?.block).toBe(true);
  expect(gate({toolName:"TaskManage"},ctx)).toBeUndefined();
  expect(activeInput("\x04")).toEqual({consume:true});
  expect(footer()).toBeUndefined();
  expect(gate({toolName:"Bash"},ctx)).toBeUndefined();
  expect(before({systemPrompt:"base"},ctx)).toBeUndefined();

  expect(activeInput("ctrl+d:repeat")).toEqual({consume:true});
  expect(activeInput("ctrl+d:release")).toEqual({consume:true});
  expect(footer()).toBeUndefined();
  expect(activeInput("\x04")).toEqual({consume:true});
  expect(footer()).toBe("bootstrap:on");
  expect(gate({toolName:"Bash"},ctx)?.block).toBe(true);
  expect(gate({toolName:"TaskManage"},ctx)).toBeUndefined();
  expect(requestRender).toHaveBeenCalledTimes(6);
  expect(notify.mock.calls.map(call=>call[0])).toEqual(["Bootstrap requirement on","Bootstrap requirement on","Bootstrap requirement off","Bootstrap requirement on"]);
  for(const shutdown of handlers.get("session_shutdown")??[])await shutdown({},ctx);
  expect(removers[1]).toHaveBeenCalledTimes(1);
 }finally{
  (globalThis as any)[Symbol.for("pi-swarm-footer-segments")]?.delete("bootstrap-requirement");
  delete (globalThis as any)[Symbol.for("pi-swarm-bootstrap-requirement-state")];
  rmSync(cwd,{recursive:true,force:true});
 }
});
