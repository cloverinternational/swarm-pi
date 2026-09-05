import { existsSync, readFileSync } from "node:fs";
import { join } from "node:path";
import { HookRuntimeCoordinator, type HookDefinition } from "../../taskmanage/src/swarm-hook-runtime.ts";
import { registerHook } from "../hook-state.ts";

interface DiskHook { type?: string; command?: string; prompt?: string; timeout?: number; }
interface Matcher { matcher?: string; hooks?: DiskHook[]; }
const eventMap: Record<string,string> = { PreToolUse:"tool.before_execute", PostToolUse:"tool.after_execute", SessionStart:"agent.started", SessionEnd:"agent.stopped", Stop:"agent.stopped", UserPromptSubmit:"user.prompt_submit", PreCompact:"compact.before" };
const validCommand = (x: unknown): x is string => typeof x === "string" && x.trim().length > 0 && x.length <= 4096;
function read(path: string): any | undefined { if (!existsSync(path)) return undefined; try { return JSON.parse(readFileSync(path,"utf8")); } catch { return undefined; } }
function loadFiles(cwd: string, home: string) {
  const paths=[join(home,".swarm","config","hooks.json"),join(home,".claude","settings.json"),join(cwd,".claude","settings.json"),join(cwd,".claude","settings.local.json")];
  return paths.flatMap(path=>{const d=read(path); if(!d)return []; if(Array.isArray(d.custom_hooks)) return d.custom_hooks.map((h:any)=>({name:h.name,events:h.event_patterns??[],command:h.command,priority:h.priority??50,timeoutMs:parseTimeout(h.timeout),action:h.action??"continue",matcher:h.tool_matcher})); const out:any[]=[]; for(const [event,matchers] of Object.entries(d.hooks??{})){for(const m of (matchers as Matcher[])??[])for(const h of m.hooks??[])if((h.type??"command")==="command"&&validCommand(h.command))out.push({name:`${event}-${out.length+1}`,events:[eventMap[event]??event.toLowerCase()],command:h.command,priority:50,timeoutMs:(h.timeout??60)*1000,action:"block_exit2",matcher:m.matcher});}return out;});
}
function parseTimeout(value: unknown){if(typeof value!=="string")return 60000;const m=value.match(/^(\d+)(ms|s|m)?$/);if(!m)return 60000;return Number(m[1])*(m[2]==="m"?60000:m[2]==="s"?1000:1);}
export function registerDiskHooks(pi: any, options: { cwd?: string; home?: string; defaultTimeoutMs?: number } = {}) {
  const cwd=options.cwd??process.cwd(), home=options.home??process.env.HOME??cwd; const runtime=new HookRuntimeCoordinator({defaultTimeoutMs:options.defaultTimeoutMs??60000,appendAudit:a=>pi.appendEntry?.("pi-swarm-disk-hook-audit",a)});
  for(const h of loadFiles(cwd,home)) runtime.register({name:h.name,priority:h.priority,events:h.events,timeoutMs:h.timeoutMs,failureMode:"block",filter:(event:any)=>!h.matcher||h.matcher==="*"||new RegExp(`^(?:${h.matcher})$`).test(String(event.tool??event.toolName??"")),handle:async event=>{const child=await import("node:child_process");return await new Promise<any>((resolve,reject)=>{const p=child.exec(h.command,{cwd,timeout:h.timeoutMs,maxBuffer:1024*1024},(error:any,stdout:string,stderr:string)=>{if(h.action==="block_on_output"&&stdout) return reject(new Error("disk hook blocked (output)")); if(h.action==="block_exit2"&&error?.code===2)return reject(new Error("disk hook blocked (exit 2)")); if(h.action==="block"&&error)return reject(new Error(`disk hook failed (${error.code??"unknown"})`)); if(error&&h.action!=="continue")return reject(error); resolve({output:[stdout,stderr].filter(Boolean).join("\n").trim()||undefined});}); p.stdin?.on("error",()=>{/* hook exited before consuming the event */}); p.stdin?.end(JSON.stringify(event)); p.on("error",reject);});}});
  const dispatch = async (eventName: string, e: any, c: any, canBlock = false) => {
    const result = await runtime.dispatch(e, eventName, c);
    return canBlock && result.blocked ? { block: true, reason: result.blocked.message ?? `Blocked by ${result.blocked.hook}`, hookOutput: result.hookOutput } : result.hookOutput ? { hookOutput: result.hookOutput } : undefined;
  };
  registerHook(pi, "disk-hooks", "tool_call", (e:any,c:any) => dispatch("tool_call", e, c, true));
  registerHook(pi, "disk-hooks", "tool_result", (e:any,c:any) => dispatch("tool_result", e, c));
  registerHook(pi, "disk-hooks", "session_start", (e:any,c:any) => dispatch("session_start", e, c));
  registerHook(pi, "disk-hooks", "session_shutdown", (e:any,c:any) => dispatch("session_shutdown", e, c));
  registerHook(pi, "disk-hooks", "before_agent_start", (e:any,c:any) => dispatch("user.prompt_submit", e, c));
  registerHook(pi, "disk-hooks", "session_before_compact", (e:any,c:any) => dispatch("compact.before", e, c));
  pi.registerCommand?.("swarm-disk-hooks",{description:"Reload and inspect disk hooks",handler:async(_a:string,ctx:any)=>ctx.ui?.notify?.(`Loaded ${loadFiles(cwd,home).length} disk hooks`,"info")}); return runtime;
}
export default function swarmDiskHooksExtension(pi:any){return registerDiskHooks(pi);}
