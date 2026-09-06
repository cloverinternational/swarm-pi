import { connect, type Socket } from "node:net";
import { randomUUID, timingSafeEqual } from "node:crypto";
import type { ControlPlane, CreateJobRequest, Job, AgentBinding, AgentRegistration } from "./control-plane.js";
import type { ID } from "./contracts.js";
import type { ControlTaskInterface, CreateGoalRequest, CreateTaskRequest, CreateRunRequest, Goal, Task, Run } from "./control-task.js";
import type { GoalLoopControlPlane, GoalCreateInput, LoopCreateInput, GoalRecord, LoopRecord } from "./goal-loop.js";

export class DaemonUnavailableError extends Error { readonly code = "unavailable"; constructor(message = "daemon is unavailable", readonly cause?: unknown) { super(message); this.name = "DaemonUnavailableError"; } }
export interface DaemonRpcOptions { socketPath: string; token: string; timeoutMs?: number; }
export class DaemonRpcClient {
  constructor(private readonly options: DaemonRpcOptions) { if (!options.socketPath) throw new Error("daemon socket is required"); if (!options.token) throw new Error("daemon token is required"); }
  /** Safe lifecycle hook for Pi; calls use short-lived sockets. */
  close(): void { /* no persistent connection to close */ }
  async call<T>(method: string, params?: unknown): Promise<T> {
    const request_id=randomUUID(); const line=JSON.stringify({request_id,method,params,auth:this.options.token})+"\n";
    return new Promise<T>((resolve,reject)=>{ let socket:Socket|undefined; let timer:NodeJS.Timeout|undefined; let buf="";
      const fail=(e:unknown)=>{if(timer)clearTimeout(timer);socket?.destroy();reject(new DaemonUnavailableError("daemon is unavailable",e));};
      try { socket=connect(this.options.socketPath); } catch(e){fail(e);return;}
      timer=setTimeout(()=>fail(new Error("RPC timeout")),this.options.timeoutMs??5000);
      socket.once("error",fail); socket.on("data",chunk=>{buf+=chunk.toString();const i=buf.indexOf("\n");if(i<0)return;try{const r=JSON.parse(buf.slice(0,i));if(timer)clearTimeout(timer);socket?.end();if(r.ok)resolve(r.result as T);else {const e=Object.assign(new Error(r.error?.message??"daemon request failed"),{code:r.error?.code??"internal",retryable:r.error?.retryable??false});reject(e);}}catch(e){fail(e);}}); socket.on("connect",()=>socket!.write(line));
    });
  }
}
export function createDaemonControlPlane(client: DaemonRpcClient): ControlPlane {
 return { registerAgent:(r,a)=>client.call<AgentBinding>("agent.register",{...r,actor:a}), heartbeatAgent:(id,a,at)=>client.call("agent.heartbeat",{id,actor:a,at}), stopAgent:(id,a)=>client.call("agent.stop",{id,actor:a}), createJob:(r,a)=>client.call<Job>("job.create",{...r,actor:a}), getJob:id=>client.call("job.get",{id}), cancelJob:(id,a)=>client.call("job.cancel",{id,actor:a}), retryJob:(id,a)=>client.call("job.retry",{id,actor:a}), readEvents:(after,limit)=>client.call("events.subscribe",{after_sequence:after,limit}) };
}
export function createDaemonControlTask(client: DaemonRpcClient): ControlTaskInterface {
 const c=<T>(method:string,p?:unknown)=>client.call<T>(method,p); return { goalCreate:r=>c<Goal>("goal.create",r),goalGet:id=>c("goal.get",{id}),taskCreate:r=>c<Task>("task.create",r),taskGet:id=>c("task.get",{id}),taskStatus:id=>c("task.status",{id}),taskCancel:id=>c("task.cancel",{id}),runCreate:r=>c<Run>("run.create",r),runGet:id=>c("run.get",{id}),runStatus:id=>c("run.status",{id}),runCancel:id=>c("run.cancel",{id}) };
}
export function createDaemonGoalLoop(client: DaemonRpcClient): GoalLoopControlPlane {
 const c=<T>(m:string,p?:unknown)=>client.call<T>(m,p); return {goalCreate:i=>c<GoalRecord>("goal.create",i),goalStatus:id=>c("goal.status",{id}),goalPause:id=>c("goal.pause",{id}),goalResume:id=>c("goal.resume",{id}),goalComplete:id=>c("goal.complete",{id}),loopCreate:i=>c<LoopRecord>("loop.create",i),loopStatus:id=>c("loop.status",{id}),loopPause:id=>c("loop.pause",{id}),loopResume:id=>c("loop.resume",{id}),loopStop:id=>c("loop.stop",{id})};
}
