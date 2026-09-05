import { mkdtemp, readFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { connect } from "node:net";
import { describe, expect, it } from "vitest";
import { FileControlPlane, LocalControlPlaneServer, ControlPlaneError, createId } from "../src/index.js";

const jobRequest = (key: string) => ({ request: { prompt: `run ${key}`, target: {} }, idempotencyKey: key });
const dir = async () => mkdtemp(join(tmpdir(), "pi-control-plane-"));

function rpc(path: string, request: unknown): Promise<any> {
  return new Promise((resolve, reject) => { const socket=connect(path); let data=""; socket.on("data", b=>{data+=b.toString(); const i=data.indexOf("\n"); if(i>=0){socket.end();resolve(JSON.parse(data.slice(0,i)));}}); socket.on("error",reject); socket.on("connect",()=>socket.write(JSON.stringify(request)+"\n")); });
}

describe("FileControlPlane", () => {
  it("persists jobs, events, and idempotency across restart", async () => {
    const root=await dir(), path=join(root,"state.json"); const first=new FileControlPlane(path);
    const job=await first.createJob(jobRequest("same"), "test");
    const second=new FileControlPlane(path); expect(await second.createJob({ ...jobRequest("same"), request:{prompt:"different",target:{}} }, "retry")).toEqual(job);
    expect((await second.readEvents()).events.map(e=>e.type)).toEqual(["job.created"]);
    expect(JSON.parse(await readFile(path,"utf8")).schemaVersion).toBe(1);
  });
  it("serializes concurrent idempotent mutation and fences leases", async () => {
    const path=join(await dir(),"state.json"), a=new FileControlPlane(path,{ownerId:"a"}), b=new FileControlPlane(path,{ownerId:"b"});
    const jobs=await Promise.all(Array.from({length:10},()=>a.createJob(jobRequest("once"),"a"))); expect(new Set(jobs.map(j=>j.id)).size).toBe(1);
    const lease=await a.claimJob(); expect(lease).toBeDefined(); expect(await b.claimJob()).toBeUndefined();
    await expect(b.completeLease(lease!,"succeeded")).rejects.toMatchObject({code:"conflict"});
    expect((await a.completeLease(lease!,"succeeded")).state).toBe("succeeded");
  });
  it("recovers expired leases and rejects stale completion", async () => {
    const path=join(await dir(),"state.json"), plane=new FileControlPlane(path,{leaseMs:1}); const job=await plane.createJob(jobRequest("recover"),"test"); const lease=await plane.claimJob();
    await new Promise(r=>setTimeout(r,10)); expect(await plane.recoverExpiredLeases()).toBe(1); expect((await plane.getJob(job.id)).state).toBe("queued");
    await expect(plane.completeLease(lease!,"succeeded")).rejects.toMatchObject<ControlPlaneError>({code:"conflict"});
  });
  it("routes targeted jobs only to the exact owner and cleans cancellation leases", async () => {
    const path=join(await dir(),"state.json"), a=new FileControlPlane(path,{ownerId:"agent:a"}), b=new FileControlPlane(path,{ownerId:"agent:b"});
    const targeted=await a.createJob({ request:{prompt:"private",target:{agentId:"agent:a"}}, idempotencyKey:"targeted" },"client");
    expect(await b.claimJob()).toBeUndefined();
    const lease=await a.claimJob(); expect(lease?.ownerId).toBe("agent:a");
    await a.cancelJob(targeted.id,"operator");
    expect((await a.readEvents()).events.map(e=>e.type)).toContain("job.cancelled");
  });

});

describe("LocalControlPlaneServer", () => {
  it("authenticates requests, rejects malformed JSON, and returns typed errors", async () => {
    const root=await dir(), socket=join(root,"control.sock"), server=new LocalControlPlaneServer(new FileControlPlane(join(root,"state.json")),{socketPath:socket,token:"local-secret"}); await server.listen();
    expect(await rpc(socket,"not json")).toMatchObject({ok:false,error:{code:"invalid_request"}});
    expect(await rpc(socket,{request_id:"1",method:"job.get",auth:"wrong",params:{id:"job:missing"}})).toMatchObject({request_id:"1",ok:false,error:{code:"unauthorized"}});
    expect(await rpc(socket,{request_id:"2",method:"job.get",auth:"local-secret",params:{id:"job:missing"}})).toMatchObject({request_id:"2",ok:false,error:{code:"not_found"}});
    const created=await rpc(socket,{request_id:"3",method:"job.create",auth:"local-secret",params:{...jobRequest("wire"),actor:"client"}}); expect(created.ok).toBe(true); expect(created.result.id).toMatch(/^job:/);
    await server.close();
  });
  it("does not expose prompt data in event replay", async () => {
    const root=await dir(), socket=join(root,"control.sock"), server=new LocalControlPlaneServer(new FileControlPlane(join(root,"state.json")),{socketPath:socket,token:"t"}); await server.listen();
    await rpc(socket,{request_id:"1",method:"job.create",auth:"t",params:{...jobRequest("redact"),actor:"client"}});
    const response=await rpc(socket,{request_id:"2",method:"events.subscribe",auth:"t",params:{}}); expect(JSON.stringify(response)).not.toContain("run redact"); await server.close();
  });
});
