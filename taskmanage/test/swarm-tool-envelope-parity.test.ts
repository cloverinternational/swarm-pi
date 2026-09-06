import { describe, expect, it } from "vitest";
import { mkdtempSync, symlinkSync, writeFileSync, mkdirSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { goNow, swarmValidateTaskManageParams } from "../src/swarm-validate.js";
import { bashTruncateOutput, checkAllowedPath, defaultAllowedPaths, resolveWorkdir } from "../../.pi/lib/swarm-bash.ts";
import { PERMISSIVE_PARAMETERS, applySwarmSurface, overlaySwarmToolSchemas } from "../../.pi/lib/swarm-tool-surface.ts";
import { alignProviderPayload } from "../../.pi/lib/swarm-transport-parity.ts";
import { SwarmSkillRegistry } from "../../.pi/lib/swarm-skill-registry.ts";
import { readImage } from "../../.pi/lib/swarm-read-image.ts";
import { swarmMessageShapes, trimWireContent } from "../../.pi/lib/swarm-transport-parity.ts";
import { truncateSnapshotMessage } from "../../.pi/lib/swarm-apply-patch.ts";
import { annoyedPublicTitle, annoyedResultXML, publishAnnoyedIssue } from "../../.pi/lib/swarm-annoyed-publish.ts";
import { goLocalRFC3339 } from "../../schedule/src/tools.js";

describe("OpenAI message shapes (translate.go, stream.go reasoning fallback)", () => {
  it("turns reasoning-only assistant turns into text, drops thinking otherwise, strips tool images, renames unknown-tool errors", () => {
    const known = new Set(["bash"]);
    const shaped = swarmMessageShapes([
      { role: "assistant", content: [{ type: "thinking", thinking: "hmm" }, { type: "toolCall", id: "1", name: "bash", arguments: {} }] },
      { role: "assistant", content: [{ type: "thinking", thinking: "hmm" }, { type: "text", text: "Let me look." }] },
      { role: "toolResult", toolName: "Read", content: [{ type: "image", data: "x", mimeType: "image/png" }, { type: "text", text: "Image file: a.png" }] },
      { role: "toolResult", toolName: "nope", content: [{ type: "text", text: "Tool nope not found" }] },
      { role: "toolResult", toolName: "bash", content: [{ type: "text", text: "Tool bash not found" }] },
    ], known)!;
    expect(shaped[0].content).toEqual([{ type: "text", text: "hmm" }, { type: "toolCall", id: "1", name: "bash", arguments: {} }]);
    expect(shaped[1].content).toEqual([{ type: "text", text: "Let me look." }]);
    expect(shaped[2].content).toEqual([{ type: "text", text: "Image file: a.png" }]);
    expect(shaped[3].content).toEqual([{ type: "text", text: "Error: Tool 'nope' not found" }]);
    expect(shaped[4].content).toEqual([{ type: "text", text: "Tool bash not found" }]);
    expect(swarmMessageShapes([{ role: "user", content: "x" }], known)).toBeUndefined();
  });
  it("trims wire content and substitutes [Response truncated]", () => {
    const trimmed = trimWireContent([{ role: "user", content: " hi\n" }, { role: "assistant", content: "  " }, { role: "assistant", content: "", tool_calls: [{}] }, { role: "tool", content: " keep " }])!;
    expect(trimmed.map(m => m.content)).toEqual(["hi", "[Response truncated]", "", " keep "]);
    expect(trimWireContent([{ role: "user", content: "hi" }])).toBeUndefined();
  });
});

describe("checkpoint.go / schedule / annoyed formats", () => {
  it("byte-truncates snapshot provenance like Go slicing", () => {
    expect(truncateSnapshotMessage("x".repeat(300))).toHaveLength(300);
    expect(truncateSnapshotMessage("x".repeat(301))).toBe("x".repeat(297) + "...");
  });
  it("formats local RFC3339 like time.Format(time.RFC3339)", () => {
    expect(goLocalRFC3339(new Date(2026, 8, 5, 22, 7, 57))).toMatch(/^2026-09-05T22:07:57(Z|[+-]\d\d:\d\d)$/);
  });
  it("renders the annoyed XML result and publishes through gh api with Go's error text", async () => {
    expect(annoyedResultXML("it broke", "https://github.com/o/r/issues/1", "o/r", "low")).toBe('<result status="ok" severity="low">\n  <issue><![CDATA[it broke]]></issue>\n  <publication_url><![CDATA[https://github.com/o/r/issues/1]]></publication_url>\n  <repository><![CDATA[o/r]]></repository>\n</result>');
    expect(annoyedPublicTitle("  many   words ", "high")).toBe("[HIGH] many words");
    const calls: any[] = [];
    const ok = await publishAnnoyedIssue("o/r", "t", "b", async (args, stdin) => { calls.push([args, stdin]); return { code: 0, signal: null, stdout: '{"html_url":"https://github.com/o/r/issues/7"}', stderr: "" }; });
    expect(ok).toBe("https://github.com/o/r/issues/7");
    expect(calls[0]).toEqual([["api", "--method", "POST", "repos/o/r/issues", "--input", "-"], '{"title":"t","body":"b"}']);
    await expect(publishAnnoyedIssue("o/r", "t", "b", async () => ({ code: 4, signal: null, stdout: "", stderr: "To get started with GitHub CLI, please run:  gh auth login\n" }))).rejects.toThrow("annoyed: GitHub API POST repos/o/r/issues: exit status 4: To get started with GitHub CLI, please run:  gh auth login");
    await expect(publishAnnoyedIssue("o/r", "t", "b", async () => ({ code: 0, signal: null, stdout: '{"html_url":"http://x/issues/1"}', stderr: "" }))).rejects.toThrow("annoyed: gh returned invalid issue URL");
  });
});

describe("TaskManage.Validate port (task_manage.go parseTaskOperations)", () => {
  const v = swarmValidateTaskManageParams;
  it("accepts a valid batch and reports Go's exact messages otherwise", () => {
    expect(v({ operations: [{ key: "a", op: "create", subject: "x" }] })).toBeUndefined();
    expect(v({ operations: [{ key: "c", op: "create", subject: "" }] })).toBe('operation "c": op:"create" requires a non-blank "subject". A minimal valid create is {"key":"<your-key>","op":"create","subject":"<short imperative title>"} — "description" is optional and is never required. Retry this operation with "subject" set to a short imperative title.');
    expect(v({ operations: [] })).toBe("operations must contain at least one operation");
    expect(v({ operations: [{}] })).toBe("operation 0: key is required");
    expect(v({ operations: [{ key: "a" }] })).toBe('operation "a": op is required');
    expect(v({ operations: [{ key: "a", op: "nope" }] })).toBe('operation "a": unsupported op "nope"');
    expect(v({ operations: [{ key: "a", op: "get", subject: "x" }] })).toBe('operation "a": field "subject" is not valid for get');
    expect(v({ operations: [{ key: "a", op: "update", taskId: "" }] })).toBe("taskId must not be empty");
    expect(v({ operations: [{ key: "a", op: "update", taskId: { ref: "b", field: "x" } }] })).toBe("taskId reference field must be taskId");
    expect(v({ operations: [{ key: "a", op: "update", addBlocks: [1] }] })).toBe("addBlocks[0] must be a task ID or reference");
    expect(v({ operations: [{ key: "a", op: "list", limit: 0 }] })).toBe('operation "a": limit must be between 1 and 500');
    expect(v({ operations: [{ key: "a", op: "create", subject: "x", category: "bogus" }] })).toBe('operation "a": invalid category "bogus"');
    expect(v({ operations: [{ key: "a", op: "create", subject: "x" }, { key: "a", op: "create", subject: "y" }] })).toMatch(/^duplicate operation key "a": keys must be unique per operation/);
    expect(v({ operations: [{ key: "a", op: "create", subject: "x" }, { key: "a", op: "update", status: "completed" }] })).toBeUndefined();
    expect(v({ operations: [{ key: "a", op: "list" }], mode: "bulk" })).toBe('unsupported mode "bulk"');
    expect(v({ operations: [{ key: "a", op: "list" }], extra: 1 })).toBe('unknown top-level field "extra"');
  });
  it("formats timestamps like Go time.Time (RFC3339Nano, trimmed)", () => {
    expect(goNow()).toMatch(/^\d{4}-\d\d-\d\dT\d\d:\d\d:\d\d(\.\d{1,9})?Z$/);
    expect(goNow()).not.toMatch(/0Z$/);
  });
});

describe("bash path guard + spill file (path_guard.go, bash.go)", () => {
  it("mirrors builtinAllowedPaths and Swarm's not_allowed text", () => {
    expect(defaultAllowedPaths("/w", "/home/u")).toEqual(["/w", "/tmp", "/home/u/.swarmos"]);
    expect(checkAllowedPath("/nonexistent-cwd", ["/w", "/tmp"])).toBe("Path not allowed (not_allowed): /nonexistent-cwd");
    expect(checkAllowedPath("/tmp/anything/deeper", ["/tmp"])).toBeUndefined();
    expect(checkAllowedPath("/tmpfoo", ["/tmp"])).toBe("Path not allowed (not_allowed): /tmpfoo");
    expect(checkAllowedPath("/anything", [])).toBeUndefined();
    const root = mkdtempSync(join(tmpdir(), "pi-guard-"));
    mkdirSync(join(root, "real")); symlinkSync(join(root, "real"), join(root, "link"));
    expect(checkAllowedPath(join(root, "link", "missing", "child"), [join(root, "real")])).toBeUndefined();
    expect(resolveWorkdir("/nonexistent-cwd", "/w", ["/w"])).toEqual({ error: "Path not allowed (not_allowed): /nonexistent-cwd" });
    expect(resolveWorkdir(join(root, "nope"), "/w", [root])).toEqual({ error: `cwd does not exist: ${join(root, "nope")}` });
  });
  it("names the spill file like os.CreateTemp(dir, \"bash-full-*.txt\")", () => {
    const t = bashTruncateOutput("x\n".repeat(2500), mkdtempSync(join(tmpdir(), "pi-spill-")));
    expect(t.truncated).toBe(true);
    expect(t.outputPath).toMatch(/\/swarm-tool-output\/bash-full-\d{1,10}\.txt$/);
  });
});

describe("permissive validator + canonical wire overlay", () => {
  it("registers a permissive schema and restores the fixture on the wire", () => {
    const tool = applySwarmSurface({ name: "Read", description: "", parameters: { type: "object", required: ["file_path"] } });
    expect(tool.parameters).toEqual(PERMISSIVE_PARAMETERS);
    const payload = alignProviderPayload({ model: "m", messages: [], tools: [{ type: "function", function: { name: "Read", description: "", parameters: tool.parameters } }, { type: "function", function: { name: "pi_only", description: "d", parameters: { type: "object" } } }] })!;
    expect(payload.tools[0].function.description).toBe(tool.description);
    expect(payload.tools[0].function.parameters.required).toEqual(["file_path"]);
    expect(payload.tools[1].function.parameters).toEqual({ type: "object" });
    expect(overlaySwarmToolSchemas({ tools: [{ name: "Read", description: tool.description, input_schema: {} }] })!.tools[0].input_schema.required).toEqual(["file_path"]);
    expect(overlaySwarmToolSchemas({ tools: [] })).toBeUndefined();
  });
});

describe("Skill tool rendering (skilltools/skill_tool.go) and FSRead approval", () => {
  it("prefixes the base directory, substitutes args/variables, and uses Go's error text", () => {
    const home = mkdtempSync(join(tmpdir(), "pi-skill-"));
    const dir = join(home, ".swarm", "skills", "demo"); mkdirSync(dir, { recursive: true });
    writeFileSync(join(dir, "SKILL.md"), "---\nname: demo\ndescription: d\n---\n\nHello {{1}} in ${SWARM_SKILL_DIR} session ${SWARM_SESSION_ID}\n\n");
    const hidden = join(home, ".swarm", "skills", "hidden"); mkdirSync(hidden);
    writeFileSync(join(hidden, "SKILL.md"), "---\nname: hidden\ndescription: d\ndisable-model-invocation: true\n---\nbody");
    const registry = new SwarmSkillRegistry({ cwd: home, home, builtinDir: null });
    expect(registry.invoke("demo", "world", "sess-1").text).toBe(`Base directory for this skill: ${dir}\n\nHello world in ${dir} session sess-1`);
    expect(() => registry.invoke("nope")).toThrow('skill "nope" not found in registry');
    expect(() => registry.invoke("hidden")).toThrow('skill "hidden" cannot be used with the Skill tool due to disable-model-invocation');
    // arguments.go: named {{argName}} from frontmatter `arguments` by position over
    // strings.Fields(args), then positional {{N}}; {{arg}} is not special.
    const named = join(home, ".swarm", "skills", "named"); mkdirSync(named);
    writeFileSync(join(named, "SKILL.md"), "---\nname: named\ndescription: d\narguments:\n  - repo\n  - branch\n---\nClone {{repo}}@{{branch}} {{1}}/{{2}}/{{3}} {{arg}} ${SWARM_SESSION_ID}|");
    registry.refresh();
    expect(registry.invoke("named", "a  b\tc").text).toBe(`Base directory for this skill: ${named}\n\nClone a@b a/b/c {{arg}} |`);
    expect(registry.invoke("named", "only").text).toBe(`Base directory for this skill: ${named}\n\nClone only@{{branch}} only/{{2}}/{{3}} {{arg}} |`);
    // Builtins: Path is "builtin:<name>", no base-directory prefix, and the
    // catalogue still lists disable-model-invocation skills (only Skill refuses).
    const withBuiltins = new SwarmSkillRegistry({ cwd: home, home });
    expect(withBuiltins.invoke("loop").text.startsWith("Base directory")).toBe(false);
    expect(withBuiltins.catalog("")).toContain("<name>hidden</name>");
  });
  it("skips the workspace boundary only for approved (headless auto) paths", () => {
    expect(() => readImage("/nonexistent/parity.txt", "/w")).toThrow("path must be within workspace or ~/.swarm/");
    expect(readImage("/nonexistent/parity.txt", "/w", true)[0]).toMatchObject({ type: "text", text: expect.stringMatching(/^ERROR: Read handles image files only/) });
    expect(() => readImage("", "/w", true)).toThrow("cannot resolve path: empty path");
  });
});
