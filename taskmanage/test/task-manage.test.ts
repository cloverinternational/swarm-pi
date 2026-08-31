import { describe, expect, it } from "vitest";
import { TaskManager, type JournalEntry } from "../src/task-manage.js";

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
});
