import assert from "node:assert/strict";
import test from "node:test";
import { mkdtemp, rm } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join, resolve } from "node:path";
import {
  EXPECTED_PRIMARY_REQUESTS,
  TOOL_SCRIPTS,
  canonicalizeRequest,
  captureParity,
  diffJson,
  expectedPrimaryRequests,
  primarySystemPrompt,
  promptSliceEvidence,
  sharedPromptPrefix,
  summarizeMismatches,
} from "../tools/parity/probe.mjs";

test("canonicalizer replaces generated tool call IDs consistently", () => {
  const request = canonicalizeRequest({
    messages: [
      { role: "assistant", tool_calls: [{ id: "generated-123", function: { name: "bash" } }] },
      { role: "tool", tool_call_id: "generated-123", content: "ok" },
    ],
  });
  assert.equal(request.messages[0].tool_calls[0].id, "<tool-call-1>");
  assert.equal(request.messages[1].tool_call_id, "<tool-call-1>");
});

test("canonicalizer preserves prompt, tool schema, order, and semantic request fields", () => {
  const request = {
    model: "same-model",
    messages: [{ role: "developer", content: "exact prompt\n" }],
    tools: [
      { type: "function", function: { name: "b", parameters: { type: "object" } } },
      { type: "function", function: { name: "a", parameters: { type: "object" } } },
    ],
    reasoning_effort: "high",
  };
  assert.deepEqual(canonicalizeRequest(request), {
    messages: [{ content: "exact prompt\n", role: "developer" }],
    model: "same-model",
    reasoning_effort: "high",
    tools: [
      { function: { name: "b", parameters: { type: "object" } }, type: "function" },
      { function: { name: "a", parameters: { type: "object" } }, type: "function" },
    ],
  });
});

test("JSON diff reports exact pointers and does not hide semantic differences", () => {
  const differences = diffJson(
    { messages: [{ role: "system", content: "one" }], tools: [{ name: "read" }] },
    { messages: [{ role: "developer", content: "two" }], tools: [{ name: "bash" }], temperature: 0 },
  );
  assert.deepEqual(differences, [
    { path: "/messages/0/content", kind: "changed", left: "one", right: "two" },
    { path: "/messages/0/role", kind: "changed", left: "system", right: "developer" },
    { path: "/temperature", kind: "missing-left", right: 0 },
    { path: "/tools/0/name", kind: "changed", left: "read", right: "bash" },
  ]);
  assert.deepEqual(summarizeMismatches(differences), { messages: 2, temperature: 1, tools: 1 });
});

test("mismatch summary skips the request-array index", () => {
  assert.deepEqual(summarizeMismatches([
    { path: "/0/tools/3/function/name", kind: "changed" },
    { path: "/1/messages/4/content", kind: "changed" },
    { path: "/1/tools/0/function/parameters", kind: "changed" },
  ]), { tools: 2, messages: 1 });
});

test("project prompt alignment excludes host-owned context and skill suffixes", () => {
  const prompt = "shared\n\n<context_file path=\"AGENTS.md\">\nlocal\n</context_file>\n\n<available_skills>x</available_skills>";
  const requests = [{
    messages: [
      { role: "developer", content: prompt },
      { role: "user", content: "PARITY_CAPTURE" },
    ],
  }];
  assert.equal(primarySystemPrompt(requests), prompt);
  assert.equal(sharedPromptPrefix(prompt), "shared");
  assert.deepEqual(promptSliceEvidence(`before\n${prompt}\nafter`, prompt), {
    offset: 7,
    present: true,
    exact: true,
  });
  assert.deepEqual(promptSliceEvidence("different", prompt), {
    offset: -1,
    present: false,
    exact: false,
  });
  assert.equal(
    sharedPromptPrefix(`<available_skills>\n  <skill>x</skill>\n</available_skills>\nWhen a user request matches an available skill, invoke the Skill tool before responding.\n\n${prompt}`),
    "shared",
  );
});

test("clean profile captures matched isolated Pi and Swarm request sequences", async () => {
  const output = await mkdtemp(join(tmpdir(), "pi-swarm-parity-test-"));
  try {
    const result = await captureParity({
      profile: "clean",
      workspace: resolve("."),
      output,
    });
    assert.equal(result.profile, "clean");
    assert.equal(result.pi.requests.length, EXPECTED_PRIMARY_REQUESTS);
    assert.equal(result.swarm.requests.length, EXPECTED_PRIMARY_REQUESTS);
    assert.deepEqual(
      result.pi.tools.map(tool => tool.function?.name ?? tool.name),
      ["bash"],
    );
    assert.deepEqual(
      result.swarm.tools.map(tool => tool.function?.name ?? tool.name),
      ["bash"],
    );
    // The goal condition: identical JSON at the model boundary. Timing fields
    // (duration_ms) and random error ids are normalised by the canonicaliser;
    // everything else — system prompt, user turn + capability manifest, tool
    // schema, request options, tool call/result — must match byte for byte.
    assert.equal(result.pi.prompt.sha256, result.swarm.prompt.sha256);
    assert.deepEqual(result.mismatches, [], `pi -p and swarm -p diverged: ${JSON.stringify(result.mismatchCategories)}`);
    // Stronger than the structural diff: the raw request bodies must agree
    // byte for byte (key order included) once run-random ids/timings and Go's
    // \u003c HTML escaping are folded.
    assert.equal(result.wire.identical, true, `wire bytes diverged: ${JSON.stringify(result.wire.requests)}`);
  } finally {
    await rm(output, { recursive: true, force: true });
  }
}, { timeout: 30_000 });

test("project profile is wire-identical with full skills, context, and the 28-tool surface", async () => {
  const output = await mkdtemp(join(tmpdir(), "pi-swarm-parity-test-"));
  try {
    const result = await captureParity({ profile: "project", workspace: resolve("."), output });
    assert.equal(result.pi.requests.length, EXPECTED_PRIMARY_REQUESTS);
    assert.equal(result.swarm.requests.length, EXPECTED_PRIMARY_REQUESTS);
    assert.equal(result.pi.tools.length, 28);
    assert.equal(result.swarm.tools.length, 28);
    assert.equal(result.pi.skills.length, 1);
    assert.equal(result.swarm.skills.length, 1);
    assert.equal(result.pi.prompt.sha256, result.swarm.prompt.sha256);
    assert.deepEqual(result.mismatches, [], `pi -p and swarm -p diverged: ${JSON.stringify(result.mismatchCategories)}`);
    assert.equal(result.wire.identical, true, `wire bytes diverged: ${JSON.stringify(result.wire.requests)}`);
  } finally {
    await rm(output, { recursive: true, force: true });
  }
}, { timeout: 120_000 });

test("unknown capture profiles are rejected before launching either runtime", async () => {
  await assert.rejects(
    captureParity({ profile: "invalid", workspace: resolve(".") }),
    /unknown parity profile: invalid/,
  );
});

// Builtin hook context (task enforcement/maintenance, skill budget + review,
// sleep/stdin blockers, annoyance nudge) and every non-bash tool's result
// envelope + error path must also be byte-identical, not only the 3-request
// base probe. Each scenario is a scripted tool-call sequence in TOOL_SCRIPTS.
for (const scenario of Object.keys(TOOL_SCRIPTS).filter(name => !["default", "skills", "mutations", "mutations2"].includes(name))) {
  test(`project profile is wire-identical for the ${scenario} scenario`, async () => {
    const output = await mkdtemp(join(tmpdir(), "pi-swarm-parity-test-"));
    try {
      const result = await captureParity({ profile: "project", scenario, workspace: resolve("."), output });
      assert.equal(result.pi.requests.length, expectedPrimaryRequests(scenario));
      assert.equal(result.swarm.requests.length, expectedPrimaryRequests(scenario));
      assert.deepEqual(result.mismatches, [], `pi -p and swarm -p diverged: ${JSON.stringify(result.mismatchCategories)}`);
      assert.equal(result.wire.identical, true, `wire bytes diverged: ${JSON.stringify(result.wire.requests.filter(r => !r.identical).slice(0, 2))}`);
    } finally {
      await rm(output, { recursive: true, force: true });
    }
  }, { timeout: 180_000 });
}

// A foreign (non-repo) workspace exercises the discovery paths the repository
// itself never hits: hierarchical AGENTS.md, CLAUDE.md + SWARM.md side by
// side, a dirty git tree, project-level .claude/skills (Swarm discovers it)
// next to .pi/skills (Swarm does not), and enough project skills to overflow
// the <available_skills> budget so ranking/truncation must agree too.
async function makeStressWorkspace(root) {
  const { mkdir, writeFile } = await import("node:fs/promises");
  const { execFileSync } = await import("node:child_process");
  const write = async (path, text) => { await mkdir(join(root, path, ".."), { recursive: true }); await writeFile(join(root, path), text); };
  await mkdir(root, { recursive: true });
  await write("AGENTS.md", "root rules\n");
  await write("CLAUDE.md", "claude rules\n");
  await write("SWARM.md", "swarm rules\n");
  await write("pkg/AGENTS.md", "pkg rules\n");
  await write("pkg/sub/f.txt", "f\n");
  execFileSync("git", ["init", "-q"], { cwd: root });
  execFileSync("git", ["-c", "user.email=p@p", "-c", "user.name=p", "add", "-A"], { cwd: root });
  execFileSync("git", ["-c", "user.email=p@p", "-c", "user.name=p", "commit", "-qm", "init"], { cwd: root });
  await write("dirty.txt", "dirty\n");
  const skill = (name, description, extra = "") => `---\nname: ${name}\ndescription: "${description}"\n${extra}---\nbody of ${name}\n`;
  await write(".claude/skills/claude-side/SKILL.md", skill("claude-side", "A skill from .claude/skills — it's \\\"quoted\\\" & <tagged>"));
  await write(".pi/skills/pi-side/SKILL.md", skill("pi-side", "A skill from .pi/skills"));
  // Exercised by the `skills` scenario (TOOL_SCRIPTS.skills).
  await write(".swarm/skills/args-skill/SKILL.md", `---
name: args-skill
description: "Substitution probe"
arguments:
  - repo
  - branch
---
Clone {{repo}} on {{branch}}; first={{1}} second={{2}} third={{3}} fourth={{4}} zero={{0}}
dir=\${SWARM_SKILL_DIR} session=[\${SWARM_SESSION_ID}] arg={{arg}} repo again {{repo}}
`);
  await write(".swarm/skills/args-skill/references/notes.md", "notes body\n");
  await write(".swarm/skills/hidden-skill/SKILL.md", skill("hidden-skill", "Not model-invocable", "disable-model-invocation: true\n"));
  await write(".swarm/skills/empty-skill/SKILL.md", "---\nname: empty-skill\ndescription: \"No body at all\"\n---\n");
  for (let i = 1; i <= 70; i++) {
    const n = String(i).padStart(2, "0");
    // Apostrophes/quotes (html.EscapeString → &#39;/&#34;) and multibyte runes
    // (byte budget vs UTF-16 length) must render and truncate identically.
    const sentence = `Description ${n} — it's a "fairly" long sentence (café №${n}) that repeats itself to inflate the catalogue size; `;
    await write(`.swarm/skills/stress-skill-${n}/SKILL.md`, skill(`stress-skill-${n}`, sentence.repeat(5), `when_to_use: Use when the user mentions stress case ${n} or <angle> & ampersand\ntags:\n  - stress\n  - case-${n}\n`));
  }
}

test("project profile is wire-identical for a foreign workspace with nested context files, dirty git, and overflowing skills", async () => {
  const scratch = await mkdtemp(join(tmpdir(), "pi-swarm-parity-stress-"));
  const workspace = join(scratch, "ws");
  const output = join(scratch, "out");
  try {
    await makeStressWorkspace(workspace);
    const result = await captureParity({ profile: "project", workspace, output });
    assert.equal(result.pi.requests.length, EXPECTED_PRIMARY_REQUESTS);
    assert.equal(result.swarm.requests.length, EXPECTED_PRIMARY_REQUESTS);
    assert.equal(result.pi.tools.length, 28);
    assert.equal(result.swarm.tools.length, 28);
    assert.equal(result.pi.prompt.sha256, result.swarm.prompt.sha256);
    assert.match(result.swarm.prompt.text, /<location>[^<]*\/\.claude\/skills\/claude-side\/SKILL\.md<\/location>/);
    assert.doesNotMatch(result.swarm.prompt.text, /pi-side/);
    assert.match(result.swarm.prompt.text, /additional skill\(s\) omitted/);
    assert.deepEqual(result.mismatches, [], `pi -p and swarm -p diverged: ${JSON.stringify(result.mismatchCategories)}`);
    assert.equal(result.wire.identical, true, `wire bytes diverged: ${JSON.stringify(result.wire.requests.filter(r => !r.identical).slice(0, 2))}`);
  } finally {
    await rm(scratch, { recursive: true, force: true });
  }
}, { timeout: 180_000 });

// The autogen package lifecycle (create/view/patch/write_file/read_file/
// absorb_files/archive/history/undo) runs inside each side's scratch HOME;
// revision ids are per-run (they hash created_at) and are numbered by first
// appearance so reuse still has to agree.
test("project profile is wire-identical for the autogen SkillManage lifecycle in a foreign workspace (mutations scenario)", async () => {
  const scratch = await mkdtemp(join(tmpdir(), "pi-swarm-parity-stress-"));
  const workspace = join(scratch, "ws");
  const output = join(scratch, "out");
  try {
    await makeStressWorkspace(workspace);
    const result = await captureParity({ profile: "project", scenario: "mutations", workspace, output });
    assert.equal(result.pi.requests.length, expectedPrimaryRequests("mutations"));
    assert.equal(result.swarm.requests.length, expectedPrimaryRequests("mutations"));
    assert.deepEqual(result.mismatches, [], `pi -p and swarm -p diverged: ${JSON.stringify(result.mismatchCategories)}`);
    assert.equal(result.wire.identical, true, `wire bytes diverged: ${JSON.stringify(result.wire.requests.filter(r => !r.identical).slice(0, 2))}`);
  } finally {
    await rm(scratch, { recursive: true, force: true });
  }
}, { timeout: 240_000 });

test("project profile is wire-identical for SkillManage edge cases in a foreign workspace (mutations2 scenario)", async () => {
  const scratch = await mkdtemp(join(tmpdir(), "pi-swarm-parity-stress-"));
  const workspace = join(scratch, "ws");
  const output = join(scratch, "out");
  try {
    await makeStressWorkspace(workspace);
    const result = await captureParity({ profile: "project", scenario: "mutations2", workspace, output });
    assert.equal(result.pi.requests.length, expectedPrimaryRequests("mutations2"));
    assert.equal(result.swarm.requests.length, expectedPrimaryRequests("mutations2"));
    assert.deepEqual(result.mismatches, [], `pi -p and swarm -p diverged: ${JSON.stringify(result.mismatchCategories)}`);
    assert.equal(result.wire.identical, true, `wire bytes diverged: ${JSON.stringify(result.wire.requests.filter(r => !r.identical).slice(0, 2))}`);
  } finally {
    await rm(scratch, { recursive: true, force: true });
  }
}, { timeout: 300_000 });

test("project profile is wire-identical for on-disk skill invocation in a foreign workspace (skills scenario)", async () => {
  const scratch = await mkdtemp(join(tmpdir(), "pi-swarm-parity-stress-"));
  const workspace = join(scratch, "ws");
  const output = join(scratch, "out");
  try {
    await makeStressWorkspace(workspace);
    const result = await captureParity({ profile: "project", scenario: "skills", workspace, output });
    assert.equal(result.pi.requests.length, expectedPrimaryRequests("skills"));
    assert.equal(result.swarm.requests.length, expectedPrimaryRequests("skills"));
    assert.deepEqual(result.mismatches, [], `pi -p and swarm -p diverged: ${JSON.stringify(result.mismatchCategories)}`);
    assert.equal(result.wire.identical, true, `wire bytes diverged: ${JSON.stringify(result.wire.requests.filter(r => !r.identical).slice(0, 2))}`);
  } finally {
    await rm(scratch, { recursive: true, force: true });
  }
}, { timeout: 180_000 });
