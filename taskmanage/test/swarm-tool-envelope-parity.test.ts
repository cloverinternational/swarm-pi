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
  });
  it("skips the workspace boundary only for approved (headless auto) paths", () => {
    expect(() => readImage("/nonexistent/parity.txt", "/w")).toThrow("path must be within workspace or ~/.swarm/");
    expect(readImage("/nonexistent/parity.txt", "/w", true)[0]).toMatchObject({ type: "text", text: expect.stringMatching(/^ERROR: Read handles image files only/) });
    expect(() => readImage("", "/w", true)).toThrow("cannot resolve path: empty path");
  });
});
