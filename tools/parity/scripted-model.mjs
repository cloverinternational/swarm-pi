#!/usr/bin/env node
// Minimal scripted OpenAI-completions model for ad-hoc hook probing.
// CMDS="cmd1|cmd2" node scripted-model.mjs → writes the bound port to
// $PORT_FILE (default /tmp/pi-swarm-scripted-model.port) and logs the tail of
// every request it receives to stderr. Used by tools/parity/hookprobe.sh.
import { createServer } from "node:http";
import { writeFileSync } from "node:fs";

// Steps are "|"-separated bash commands, or JSON objects {"tool":..,"args":..}
// for other tools (JSON steps may contain "|" only inside JSON strings when
// CMDS_JSON=1, in which case CMDS is a JSON array instead).
const cmds = process.env.CMDS_JSON === "1"
  ? JSON.parse(process.env.CMDS)
  : (process.env.CMDS || "printf x > f && rm f|printf y > g && rm g|touch h && rm h").split("|");
const stepOf = raw => {
  if (raw && typeof raw === "object") return raw;
  if (typeof raw === "string" && raw.startsWith("{")) return JSON.parse(raw);
  return { tool: "bash", args: { command: raw } };
};
const portFile = process.env.PORT_FILE || "/tmp/pi-swarm-scripted-model.port";
const server = createServer(async (req, res) => {
  const chunks = [];
  for await (const c of req) chunks.push(c);
  const body = JSON.parse(Buffer.concat(chunks).toString());
  const n = body.messages.filter(m => m.role === "tool").length;
  const cmd = cmds[n] === undefined ? undefined : stepOf(cmds[n]);
  res.writeHead(200, { "content-type": "text/event-stream" });
  const emit = v => res.write(`data: ${JSON.stringify(v)}\n\n`);
  const chunk = (delta, fin = null) => ({ id: "x", object: "chat.completion.chunk", created: 1, model: body.model, choices: [{ index: 0, delta, finish_reason: fin }] });
  if (cmd && body.tools?.length) {
    emit(chunk({ role: "assistant", tool_calls: [{ index: 0, id: "call_" + n, type: "function", function: { name: cmd.tool ?? "bash", arguments: JSON.stringify(cmd.args ?? {}) } }] }));
    emit(chunk({}, "tool_calls"));
  } else {
    emit(chunk({ role: "assistant", content: "OK" }));
    emit(chunk({}, "stop"));
  }
  res.end("data: [DONE]\n\n");
  const tail = body.messages.slice(-3).map(m => [m.role, (typeof m.content === "string" ? m.content : JSON.stringify(m.content) || "").slice(0, 500)]);
  process.stderr.write(`REQ ${n} ${JSON.stringify(tail)}\n`);
});
server.listen(0, "127.0.0.1", () => writeFileSync(portFile, String(server.address().port)));
