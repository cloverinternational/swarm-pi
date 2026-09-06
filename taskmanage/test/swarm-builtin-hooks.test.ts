import { describe, expect, it } from "vitest";
import { extractShellCommandWords, isBashReadOnly, parseShellCommands } from "../../.pi/lib/swarm-toolclass.ts";
import {
  MetaNudgeBudget, META_NUDGE_BLOCK, SwarmHookPipeline, createSwarmBuiltinPipeline, type HookTask,
} from "../../.pi/lib/swarm-builtin-hooks.ts";
import { resetReminderSequences } from "../../.pi/lib/swarm-annoyance-nudge.ts";

describe("toolclass.go port", () => {
  it("classifies bash commands like IsBashReadOnly (quirks included)", () => {
    expect(isBashReadOnly("printf one")).toBe(true);
    expect(isBashReadOnly("printf x > f")).toBe(true); // '>' is an ordinary word in Go's tokenizer
    expect(isBashReadOnly("printf x > .pw && rm .pw")).toBe(false);
    expect(isBashReadOnly("printf out; printf err >&2; exit 3")).toBe(false); // '&' splits, "2" becomes a command
    expect(isBashReadOnly("git status --short | head -1")).toBe(true);
    expect(isBashReadOnly("git diff --stat | head -1 > .p; rm -f .p")).toBe(false);
    expect(isBashReadOnly("FOO=1 ls -la")).toBe(true);
    expect(isBashReadOnly("go list ./...")).toBe(true);
    expect(isBashReadOnly("go build ./...")).toBe(false);
    expect(isBashReadOnly("cat <<'EOF'\nrm -rf /\nEOF")).toBe(true);
    expect(isBashReadOnly("echo `rm x`")).toBe(true);
    expect(isBashReadOnly("echo 'unterminated")).toBe(false);
    expect(isBashReadOnly("")).toBe(false);
    expect(extractShellCommandWords("/usr/bin/grep x | sort")).toEqual(["grep", "sort"]);
    expect(parseShellCommands("a b;c")[0]).toEqual([["a", "b"], ["c"]]);
  });
});

describe("nudge_budget.go port", () => {
  it("allows one claim per turn window and none before the first user turn", () => {
    const b = new MetaNudgeBudget();
    expect(b.tryClaim("s", META_NUDGE_BLOCK)).toEqual([0, false]);
    b.recordUserTurn("s"); b.recordUserTurn("s"); // headless: after_receive + CheckTurn
    expect(b.tryClaim("s", META_NUDGE_BLOCK)).toEqual([1, true]);
    expect(b.tryClaim("s", META_NUDGE_BLOCK)).toEqual([0, false]);
    for (let i = 0; i < 5; i++) b.recordUserTurn("s");
    expect(b.tryClaim("s", META_NUDGE_BLOCK)).toEqual([2, true]);
    expect(b.tryClaim("", META_NUDGE_BLOCK)).toEqual([1, true]); // unscoped always claims
  });
});

const bash = (command: string, id = `c${Math.random()}`) => ({ toolName: "bash", params: { command }, toolCallId: id });
const taskManage = (operations: any[]) => ({ toolName: "TaskManage", params: { operations }, toolCallId: "t" });
const ok = (call: any) => ({ ...call, failed: false, output: "<result exit_code=\"0\" duration_ms=\"1\" timed_out=\"false\">\n  <stdout><![CDATA[]]></stdout>\n  <stderr><![CDATA[]]></stderr>\n</result>" });

describe("headless builtin hook pipeline", () => {
  it("task-enforcement advises once on the first non-read-only tool without a focused task", () => {
    resetReminderSequences();
    let tasks: HookTask[] = [];
    const p = createSwarmBuiltinPipeline({ session: "conv", tasks: () => tasks });
    p.onUserPrompt();
    expect(p.preTool(bash("printf one"))).toEqual({ context: "" });
    const advised = p.preTool(bash("printf x > .pw && rm .pw"));
    expect(advised.context).toBe('<system-reminder source="task-enforcement-hook" kind="nudge" seq="1">No active task is focused; consider a TaskManage create/update before multi-step work.</system-reminder>');
    expect(SwarmHookPipeline.applyPreContext(advised.context, "OUT")).toBe(`${advised.context}\n\n---\n\nOUT`);
    // budget window consumed for the rest of the single-shot run
    expect(p.preTool(bash("touch h && rm h"))).toEqual({ context: "" });
    expect(p.postTool(ok(bash("touch h && rm h")))).toBe("");
  });
  it("skill review at 6 tool calls, then onboarding budget block at the 6th non-exempt call", () => {
    resetReminderSequences();
    const tasks: HookTask[] = [{ id: "1", subject: "t1", status: "in_progress", active: true, category: "acting" }];
    const p = createSwarmBuiltinPipeline({ session: "conv", tasks: () => tasks });
    p.onUserPrompt();
    const tm = taskManage([{ key: "a", op: "create", subject: "t1", status: "in_progress", active: true }]);
    expect(p.preTool(tm).context).toBe("");
    expect(p.postTool({ ...tm, failed: false, output: "{}" })).toBe("");
    expect(p.postTool({ ...tm, failed: false, output: "{}" })).toBe("");
    const posts: string[] = [];
    for (let i = 0; i < 4; i++) { const c = bash(`printf ${i} > .x && rm .x`); expect(p.preTool(c).context).toBe(""); posts.push(p.postTool(ok(c))); }
    expect(posts.slice(0, 3)).toEqual(["", "", ""]);
    expect(posts[3]).toBe('<system-reminder source="autogenskills" kind="review" seq="1">[SKILL REVIEW] You\'ve made 6 tool calls since the last skill review. Preserve useful learning without creating one-session clutter: first patch a loaded skill, then an existing class-level umbrella, then add a support file. Create a new class-level skill only if none fits. If there is genuinely nothing reusable, call SkillManage(action: "review", review_reason: "nothing reusable to save") so work can continue without manufacturing a skill.</system-reminder>');
    expect(p.flushTurn()).toBe(posts[3]);
    const fifth = bash("printf 5 > .x && rm .x"); expect(p.preTool(fifth).context).toBe(""); p.postTool(ok(fifth));
    const sixth = p.preTool(bash("printf 6 > .x && rm .x"));
    expect(sixth.block!.startsWith("Tool 'bash' blocked by hook: <system-reminder source=\"autogenskills-budget-enforcement\" kind=\"block\" seq=\"1\">[SKILL BUDGET ENFORCEMENT — BLOCKED]\n\nYou have used 5 non-exempt tool calls. The onboarding budget is 5.\n")).toBe(true);
    expect(sixth.block!.endsWith("skill, your budget expands to 90.</system-reminder>")).toBe(true);
    expect(p.preTool(bash("printf 7 > .x && rm .x")).block).toContain('seq="2"');
    expect(p.preTool(taskManage([{ key: "b", op: "update", taskId: "1", status: "completed" }])).context).toBe(""); // exempt
    expect(p.preTool(bash("git status")).context).toBe(""); // read-only exempt, not blocked
  });
  it("bash-only pre hooks and the annoyance nudge join the same pipeline", () => {
    resetReminderSequences();
    const p = createSwarmBuiltinPipeline({
      session: "conv", tasks: () => [],
      extraPre: [{ name: "sleep-blocker", run: e => (e.params.command as string).startsWith("sleep") ? { block: true, message: "Blocked: x" } : {} }],
      extraPost: [{ name: "annoyance-nudge", run: e => e.failed ? { message: '<system-reminder source="annoyance-nudge" kind="nudge" seq="1">[ANNOYANCE REVIEW]</system-reminder>' } : {} }],
    });
    p.onUserPrompt();
    // task-enforcement (95) runs before sleep-blocker (85): it claims the
    // budget, then the block drops its context — exactly what swarm -p does.
    expect(p.preTool(bash("sleep 5")).block).toBe("Tool 'bash' blocked by hook: Blocked: x");
    const failing = bash("exit 3");
    expect(p.preTool(failing).context).toBe("");
    expect(p.postTool({ ...failing, failed: true, output: "Error executing bash: …" })).toContain("[ANNOYANCE REVIEW]");
    expect(p.flushTurn()).toContain("annoyance-nudge");
    expect(p.flushTurn()).toBe("");
  });
});
