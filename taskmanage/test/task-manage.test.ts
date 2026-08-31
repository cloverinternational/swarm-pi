import { describe, expect, it } from "vitest";
import { TaskManager, registerTaskManage, taskManageSchema, type JournalEntry } from "../src/task-manage.js";

const create = (key:string, subject=key) => ({key,op:"create" as const,subject});
describe("TaskManage", () => {
  it("commits sequential prefix and prevents atomic key leakage", () => {
    const m=new TaskManager(); expect(m.execute({operations:[create("a"),{key:"bad",op:"get",taskId:"404"},create("c")]}).status).toBe("partial");
    expect(m.execute({mode:"atomic",operations:[create("leaked"),{key:"x",op:"get",taskId:"404"}]}).status).toBe("failed");
    expect(m.execute({operations:[{key:"q",op:"list"}]}).results[0].data).toMatchObject({pagination:{total:1}});
  });
  it("resolves committed cross-call references and rejects cycles", () => {
    const m=new TaskManager(); m.execute({operations:[create("a"),create("b")]});
    expect(m.execute({operations:[{key:"link",op:"update",taskId:{ref:"b"},addBlockedBy:[{ref:"a"}]}]}).status).toBe("succeeded");
    expect(m.execute({operations:[{key:"cycle",op:"update",taskId:{ref:"a"},addBlockedBy:[{ref:"b"}]}]}).results[0].error?.code).toBe("cycle");
  });
  it("bounds list pages and rehydrates from Pi entries", () => {
    const entries:JournalEntry[]=[]; const m=new TaskManager(e=>entries.push(e)); m.execute({operations:[create("a"),create("b")]});
    const restored=new TaskManager(); restored.rehydrate(entries); const page=restored.execute({operations:[{key:"l",op:"list",limit:1}]});
    expect((page.results[0].data as any).pagination).toMatchObject({total:2,more:true});
  });
  it("publishes exact reference item schemas and rejects malformed references", () => {
    const item = (taskManageSchema.properties.operations as any).items;
    for (const field of ["addBlocks", "addBlockedBy"]) {
      const ref = item.properties[field].items.oneOf[1];
      expect(ref.required).toEqual(["ref"]);
      expect(ref.additionalProperties).toBe(false);
    }
    const m = new TaskManager();
    expect(m.execute({operations:[{key:"x",op:"create",subject:"x",addBlocks:[{ref:"a",extra:true} as any]}]}).results[0].error?.code).toBe("validation_failed");
  });
  it("rejects fields that do not apply to an operation", () => {
    const m = new TaskManager();
    for (const operation of [
      {key:"x",op:"list" as const,addNote:"no"},
      {key:"x",op:"get" as const,status:"completed" as const},
      {key:"x",op:"create" as const,subject:"x",include_audit:true},
    ]) expect(m.execute({operations:[operation as any]}).results[0].error?.code).toBe("validation_failed");
  });
  it("rolls back reverse dependency mutations when sequential update fails", () => {
    const m = new TaskManager();
    m.execute({operations:[create("a"),create("b"),create("c")]});
    const result = m.execute({operations:[{key:"u",op:"update",taskId:"1",addBlocks:["2","missing"]}]});
    expect(result.status).toBe("failed");
    expect((m.snapshot().tasks.find(t=>t.id==="2")!).dependsOn).toEqual([]);
  });
  it("validates create dependencies and parent cycles without leaking tasks", () => {
    const m = new TaskManager();
    expect(m.execute({operations:[create("a"),{key:"bad",op:"create",subject:"bad",addBlockedBy:["missing"]}]}).status).toBe("partial");
    expect(m.snapshot().tasks).toHaveLength(1);
    expect(m.execute({operations:[{key:"child",op:"create",subject:"child",parentTaskId:"1"}]}).status).toBe("succeeded");
    expect(m.execute({operations:[{key:"bad-parent",op:"update",taskId:"1",parentTaskId:"2"}]}).results[0].error?.code).toBe("cycle");
  });
  it("rejects incomplete dependencies before creating an in-progress task", () => {
    const m = new TaskManager();
    m.execute({operations:[create("dependency")]});
    const result = m.execute({operations:[{
      key:"blocked",op:"create",subject:"Blocked",status:"in_progress",addBlockedBy:["1"],
    }]});
    expect(result.results[0].error?.code).toBe("validation_failed");
    expect(m.snapshot().tasks).toHaveLength(1);
    expect(m.snapshot().nextId).toBe(2);
  });
  it("validates dependencies against an update's resulting in-progress state", () => {
    const m = new TaskManager();
    m.execute({operations:[create("dependency"),create("target")]});
    expect(m.execute({operations:[{key:"start",op:"update",taskId:"2",status:"in_progress"}]}).status).toBe("succeeded");

    const omittedStatus = m.execute({operations:[{
      key:"blocked",op:"update",taskId:"2",addBlockedBy:["1"],
    }]});
    expect(omittedStatus.results[0].error?.code).toBe("validation_failed");
    expect(m.snapshot().tasks.find(task => task.id === "2")?.dependsOn).toEqual([]);

    const explicitStatus = m.execute({operations:[{
      key:"pending-target",op:"update",taskId:"1",status:"in_progress",addBlockedBy:["2"],
    }]});
    expect(explicitStatus.results[0].error?.code).toBe("validation_failed");
    expect(m.snapshot().tasks.find(task => task.id === "1")?.status).toBe("pending");
  });
  it("rehydrates the latest state across multiple persisted entries and registers Pi shape", () => {
    const entries: JournalEntry[] = [];
    const m = new TaskManager(e => entries.push(e));
    m.execute({operations:[create("a")]});
    m.execute({operations:[create("b")]});
    const restored = new TaskManager(); restored.rehydrate(entries);
    expect(restored.snapshot().tasks.map(t=>t.subject)).toEqual(["a","b"]);
    const registered: any[] = [];
    registerTaskManage({
      appendEntry: () => {},
      registerTool: tool => registered.push(tool),
      on: () => {},
    });
    expect(registered[0]).toMatchObject({name:"TaskManage", parameters:taskManageSchema});
  });
  it("implements get include_audit without leaking audit by default", () => {
    const m = new TaskManager();
    m.execute({operations:[create("a")]});
    const normal = m.execute({operations:[{key:"g",op:"get",taskId:"1"}]});
    const audited = m.execute({operations:[{key:"ga",op:"get",taskId:"1",include_audit:true}]});
    expect((normal.results[0].data as any).task.audit_events).toBeUndefined();
    expect((normal.results[0].data as any).task.typed_notes).toBeUndefined();
    expect((audited.results[0].data as any).task.audit_events).toHaveLength(1);
  });
  it("makes in_progress the sole focus and clears active for every other status", () => {
    const m = new TaskManager();
    m.execute({operations:[create("a"), create("b")]});
    m.execute({operations:[{key:"a",op:"update",status:"in_progress"}]});
    expect(m.snapshot().tasks.map(t=>t.active)).toEqual([true, false]);
    m.execute({operations:[{key:"b",op:"update",status:"in_progress"}]});
    expect(m.snapshot().tasks.map(t=>t.active)).toEqual([false, true]);
    m.execute({operations:[{key:"b",op:"update",status:"completed"}]});
    expect(m.snapshot().tasks.map(t=>t.active)).toEqual([false, false]);
  });
  it("validates focus transitions and preserves explicit active:false", () => {
    const m = new TaskManager();
    m.execute({operations:[create("a")]});
    expect(m.execute({operations:[{key:"bad",op:"update",taskId:"1",active:true}]}).results[0].error?.code).toBe("validation_failed");
    expect(m.execute({operations:[{key:"start",op:"update",taskId:"1",status:"in_progress",active:false}]}).status).toBe("succeeded");
    expect(m.snapshot().tasks[0]).toMatchObject({status:"in_progress",active:false});
    expect(m.execute({operations:[{key:"focus",op:"update",taskId:"1",active:true}]}).status).toBe("succeeded");
    expect(m.snapshot().tasks[0].active).toBe(true);
    expect(m.execute({operations:[{key:"bad2",op:"update",taskId:"1",status:"completed",active:true}]}).results[0].error?.code).toBe("validation_failed");
  });
  it("includes computed reverse dependencies in GET after rehydration", () => {
    const entries: JournalEntry[] = [];
    const m = new TaskManager(e => entries.push(e));
    m.execute({operations:[create("a"), create("b"), {key:"link",op:"update",taskId:{ref:"b"},addBlockedBy:[{ref:"a"}]}]});
    const restored = new TaskManager();
    restored.rehydrate(entries);
    const result = restored.execute({operations:[{key:"get",op:"get",taskId:"1"}]});
    expect((result.results[0].data as any).task.blocks).toEqual(["2"]);
  });
  it("rejects invalid direct scalar and metadata values before persistence", () => {
    const m = new TaskManager();
    for (const field of ["subject", "description", "activeForm", "owner_id", "active", "include_audit"]) {
      const operation: any = {key:"bad",op: field === "include_audit" ? "get" : "create", subject:"x", [field]: field === "active" || field === "include_audit" ? "yes" : 42};
      if (operation.op === "get") delete operation.subject;
      expect(m.execute({operations:[operation]}).results[0].error?.code, field).toBe("validation_failed");
    }
    const circular: any = {}; circular.self = circular;
    expect(m.execute({operations:[{key:"cycle",op:"create",subject:"x",metadata:circular}]}).results[0].error?.code).toBe("validation_failed");
    expect(m.execute({operations:[{key:"bad-json",op:"create",subject:"x",metadata:{value:NaN}}]}).results[0].error?.code).toBe("validation_failed");
    expect(m.snapshot().tasks).toHaveLength(0);
  });
  it("keeps plain notes, typed notes, and audit history separate", () => {
    const m = new TaskManager();
    m.execute({operations:[create("a")]});
    m.execute({operations:[{key:"a",op:"update",addNote:"plain"}]});
    m.execute({operations:[{key:"a",op:"update",addNote:"typed",noteType:"decision"}]});
    const task = m.snapshot().tasks[0];
    expect(task.notes).toEqual(["plain", "typed"]);
    expect(task.typed_notes).toHaveLength(1);
    const output = m.execute({operations:[{key:"g",op:"get",taskId:"1",include_audit:true}]});
    expect((output.results[0].data as any).task).toMatchObject({
      content:"a", notes:["plain","typed"], typed_notes:[{content:"typed",type:"decision"}],
    });
    expect((output.results[0].data as any).task.audit_events).toHaveLength(3);
  });
  it("enforces the upstream update allowlist and removes deleted refs locally", () => {
    const m = new TaskManager();
    expect(m.execute({operations:[{key:"x",op:"update",taskId:"1",owner_id:"nope"} as any]}).results[0].error?.code).toBe("validation_failed");
    const result = m.execute({operations:[
      create("a"), {key:"gone",op:"update",taskId:{ref:"a"},status:"deleted"},
      {key:"after",op:"get",taskId:{ref:"a"}},
    ]});
    expect(result.results[2].error?.code).toBe("reference_failed");
  });
  it("journals each successful sequential operation", () => {
    const entries: JournalEntry[] = [];
    const m = new TaskManager(e => entries.push(e));
    m.execute({operations:[create("a"), create("b"), {key:"bad",op:"get",taskId:"missing"}]});
    expect(entries).toHaveLength(2);
    expect(entries.map(e => e.data.tasks.map(t=>t.subject))).toEqual([["a"],["a","b"]]);
  });
  it("journals sequential deletion so it stays deleted after rehydration", () => {
    const entries: JournalEntry[] = [];
    const m = new TaskManager(e => entries.push(e));
    m.execute({operations:[create("a"), {key:"gone",op:"update",taskId:{ref:"a"},status:"deleted"}]});
    expect(entries).toHaveLength(2);
    const restored = new TaskManager();
    restored.rehydrate(entries);
    expect(restored.snapshot().tasks).toEqual([]);
  });
  it("does not journal successful read operations", () => {
    const entries: JournalEntry[] = [];
    const m = new TaskManager(e => entries.push(e));
    m.execute({operations:[create("a")]});
    expect(m.execute({operations:[{key:"g",op:"get",taskId:"1"}, {key:"l",op:"list"}]}).status).toBe("succeeded");
    expect(entries).toHaveLength(1);
    m.execute({mode:"atomic",operations:[{key:"g2",op:"get",taskId:"1"}]});
    expect(entries).toHaveLength(1);
  });
  it("does not publish get keys across later mutation or rehydration", () => {
    const entries: JournalEntry[] = [];
    const m = new TaskManager(e => entries.push(e));
    m.execute({operations:[create("a")]});

    expect(m.execute({operations:[{key:"readAlias",op:"get",taskId:"1"}]}).status).toBe("succeeded");
    expect(m.snapshot().keys).not.toHaveProperty("readAlias");

    m.execute({operations:[{key:"mutate",op:"update",taskId:"1",subject:"changed"}]});
    const restored = new TaskManager();
    restored.rehydrate(entries);
    expect(restored.snapshot().keys).not.toHaveProperty("readAlias");
    expect(restored.execute({operations:[{key:"useReadAlias",op:"get",taskId:{ref:"readAlias"}}]}).results[0].error?.code)
      .toBe("reference_failed");
  });
  it("honors create active and matches upstream create deleted behavior", () => {
    const m = new TaskManager();
    m.execute({operations:[create("first")]});
    const active = m.execute({operations:[{key:"active",op:"create",subject:"Active",status:"in_progress",active:true}]});
    expect(active.status).toBe("succeeded");
    expect(m.snapshot().tasks.map(t=>t.active)).toEqual([false, true]);
    expect(m.execute({operations:[{key:"bad",op:"create",subject:"Bad",active:true}]}).results[0].error?.code).toBe("validation_failed");
    const deleted = m.execute({operations:[{key:"deleted",op:"create",subject:"Not deleted",status:"deleted"}]});
    expect(deleted.status).toBe("succeeded");
    expect(m.snapshot().tasks.at(-1)).toMatchObject({subject:"Not deleted",status:"pending",active:false});
  });
  it("stops and skips after cancellation", () => {
    const controller = new AbortController();
    controller.abort();
    const m = new TaskManager();
    const result = m.execute({operations:[create("a"),create("b")]}, controller.signal);
    expect(result.results.map(r=>r.status)).toEqual(["failed","skipped"]);
    expect(m.snapshot().tasks).toHaveLength(0);
  });
  it("matches upstream key, lifecycle, deletion, metadata, inference, and DTO contracts", () => {
    const m = new TaskManager();
    const created = m.execute({operations:[{key:"build",op:"create",subject:"Investigate API",metadata:{keep:1,remove:2}}]});
    expect(created.results[0].data).toEqual({task:{id:"1",subject:"Investigate API",status:"pending",active:false,parent_id:""}});
    expect(m.execute({operations:[{key:"build",op:"update",status:"in_progress",metadata:{added:3,remove:null}}]}).status).toBe("succeeded");
    expect(m.snapshot().tasks[0]).toMatchObject({category:"researching",active:true,metadata:{keep:1,added:3}});
    expect(m.execute({operations:[{key:"build",op:"get"}]}).results[0].status).toBe("succeeded");
    expect(m.execute({operations:[{key:"list",op:"list"}]}).results[0].data).toMatchObject({
      tasks:[{id:"1",subject:"Investigate API",category:"researching"}],
      pagination:{total:1,offset:0,limit:50,more:false},
    });
    m.execute({operations:[{key:"child",op:"create",subject:"Child",parentTaskId:"1"}]});
    const blocked = m.execute({operations:[{key:"build",op:"update",status:"deleted"}]});
    expect(blocked.results[0].error?.code).toBe("validation_failed");
    expect(m.snapshot().tasks).toHaveLength(2);
    const atomic = m.execute({mode:"atomic",operations:[
      {key:"child",op:"update",status:"deleted"},
      {key:"fail",op:"get",taskId:"missing"},
    ]});
    expect(atomic.status).toBe("failed");
    expect(m.snapshot().tasks).toHaveLength(2);
    expect(m.execute({operations:[{key:"finish",op:"update",taskId:"2",status:"deleted"}]}).status).toBe("succeeded");
    expect(m.execute({operations:[{key:"gone",op:"get",taskId:{ref:"child"}}]}).results[0].error?.code).toBe("reference_failed");
  });
});
