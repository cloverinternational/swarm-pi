// Swarm Mobile PWA — vanilla ES module, same-origin, SSE + POST /rpc.
// Coded to CONTRACT.md. All URLs are RELATIVE (served by the Go gateway).
"use strict";

/* ------------------------------------------------------------------ *
 * State
 * ------------------------------------------------------------------ */
// Resolve the gateway token: a ?token= in the URL wins and is REMEMBERED (so an
// installed PWA — start_url="." with no query — keeps working against a
// token-secured gateway across relaunches); otherwise fall back to the last
// remembered token. Best-effort: private-mode / disabled storage just no-ops.
const initToken = (() => {
  let t = "";
  try {
    t = new URLSearchParams(location.search).get("token") || "";
    if (t) localStorage.setItem("swarm.token", t);
    else t = localStorage.getItem("swarm.token") || "";
  } catch (_) {
    /* storage unavailable — fall back to the URL value only */
    t = t || new URLSearchParams(location.search).get("token") || "";
  }
  return t;
})();

const state = {
  token: initToken,
  peer: "",            // selected peer handle
  provider: "",
  model: "",
  mode: "",
  activeConvID: "",
  totalTokens: 0,
  streaming: false,
  es: null,            // EventSource
  backoff: 1000,       // reconnect backoff (ms)
  curBubble: null,     // in-progress assistant bubble element
  rpcId: 0,
};

/* ------------------------------------------------------------------ *
 * DOM helpers
 * ------------------------------------------------------------------ */
const $ = (id) => document.getElementById(id);
const peerView = $("peer-view");
const sessionView = $("session-view");
const peerList = $("peer-list");
const chat = $("chat");

function show(view) {
  peerView.classList.toggle("hidden", view !== "peer");
  sessionView.classList.toggle("hidden", view !== "session");
}

function el(tag, cls, text) {
  const e = document.createElement(tag);
  if (cls) e.className = cls;
  if (text != null) e.textContent = text;
  return e;
}

// escapeHtml neutralises every HTML metacharacter. ALL model/tool text passes
// through this before any innerHTML use, so nothing an LLM (or a tool result)
// emits can inject markup — only the tags renderRich itself produces reach the
// DOM.
function escapeHtml(s) {
  return String(s == null ? "" : s)
    .replace(/&/g, "&amp;")
    .replace(/</g, "&lt;")
    .replace(/>/g, "&gt;");
}

// renderRich turns a small, safe subset of markdown into HTML: fenced code
// blocks (```lang … ```), inline `code`, and **bold**. Everything else stays
// literal (the bubble's white-space:pre-wrap already preserves newlines, so no
// <br> juggling). Code fences are extracted FIRST so their contents are never
// touched by the inline rules, and every segment is escaped before any tag we
// emit is added. Kept tiny on purpose — the PWA stays dependency-free.
// mdCells splits a markdown table row into trimmed cell strings, dropping the
// optional leading/trailing pipes.
function mdCells(line) {
  return line
    .trim()
    .replace(/^\|/, "")
    .replace(/\|$/, "")
    .split("|")
    .map((c) => c.trim());
}

function renderRich(src) {
  const blocks = [];
  let s = String(src == null ? "" : src).replace(
    /```(\w*)\r?\n?([\s\S]*?)```/g,
    (_, _lang, code) => {
      blocks.push(
        '<pre class="code"><code>' +
          escapeHtml(code.replace(/\r?\n$/, "")) +
          "</code></pre>"
      );
      return "B" + (blocks.length - 1) + "";
    }
  );
  s = escapeHtml(s);
  // GFM tables: a header row (has a pipe), a |---| delimiter row (the
  // discriminator — prose never has one), then 0+ body rows. Cells are already
  // escaped; inline code/bold is applied per cell. Wrapped in .md-table for
  // horizontal scroll on a narrow phone. Runs before the global inline rules.
  s = s.replace(
    /(?:^|\n)([^\n]*\|[^\n]*)\n(\|? *:?-+:? *(?:\| *:?-+:? *)*\|?)\n((?:[^\n]*\|[^\n]*(?:\n|$))*)/g,
    (_, header, _delim, bodyRaw) => {
      const inl = (t) =>
        t
          .replace(/`([^`\n]+)`/g, (_, c) => "<code>" + c + "</code>")
          .replace(/\*\*([^*\n]+)\*\*/g, (_, c) => "<strong>" + c + "</strong>");
      const th = mdCells(header).map((c) => "<th>" + inl(c) + "</th>").join("");
      const rows = bodyRaw
        .replace(/\n+$/, "")
        .split("\n")
        .filter((l) => l.indexOf("|") >= 0)
        .map((l) => "<tr>" + mdCells(l).map((c) => "<td>" + inl(c) + "</td>").join("") + "</tr>")
        .join("");
      return (
        '\n<div class="md-table"><table><thead><tr>' +
        th +
        "</tr></thead><tbody>" +
        rows +
        "</tbody></table></div>\n"
      );
    }
  );
  s = s.replace(/`([^`\n]+)`/g, (_, c) => "<code>" + c + "</code>");
  s = s.replace(/\*\*([^*\n]+)\*\*/g, (_, c) => "<strong>" + c + "</strong>");
  // Headings: a line starting with 1-6 "#" then a space becomes a block heading
  // (display:block carries its own line break + margins — emitting a raw \n too
  // doubled the gap under pre-wrap).
  s = s.replace(
    /(?:^|\n)#{1,6}[ \t]+([^\n]+)/g,
    (_, txt) => '<div class="md-h">' + txt + "</div>"
  );
  // Blockquotes: a run of lines starting with ">" (already escaped to "&gt;" by
  // escapeHtml). Strip the marker and wrap the run in one <blockquote>; pre-wrap
  // keeps the internal line breaks. Inline code/bold within already applied.
  s = s.replace(/(?:^|\n)((?:&gt; ?.*(?:\n|$))+)/g, (_, run) => {
    const inner = run
      .replace(/\n+$/, "")
      .split(/\n/)
      .map((l) => l.replace(/^&gt; ?/, ""))
      .join("\n");
    return "\n<blockquote>" + inner + "</blockquote>\n";
  });
  // Numbered lists: runs of lines starting with "<n>. " -> <ol><li>. Runs FIRST
  // so "1. x" lines are consumed here, not by the bullet rule. (CSS .bubble ol
  // margins provide the spacing, matching bullets.)
  s = s.replace(/(?:^|\n)((?:[ \t]*\d+\. .*(?:\n|$))+)/g, (_, run) => {
    const items = run
      .replace(/\n+$/, "")
      .split(/\n/)
      .map((l) => l.replace(/^[ \t]*\d+\. /, ""))
      .map((t) => "<li>" + t + "</li>")
      .join("");
    return "<ol>" + items + "</ol>";
  });
  // Bullet lists: runs of lines starting with "- " or "* ". Consume the
  // surrounding newlines into the <ul> so the bubble's pre-wrap doesn't add
  // blank gaps around the list.
  s = s.replace(/(?:^|\n)((?:[ \t]*[-*] .*(?:\n|$))+)/g, (_, run) => {
    const items = run
      .replace(/\n+$/, "")
      .split(/\n/)
      .map((l) => l.replace(/^[ \t]*[-*] /, ""))
      .map((t) => "<li>" + t + "</li>")
      .join("");
    return "<ul>" + items + "</ul>";
  });
  // Spacing housekeeping. Under pre-wrap every leftover source newline renders,
  // but the block elements we just emitted (headings, lists, tables, quotes)
  // ALSO break the line and carry their own margins — so a newline abutting one
  // showed as a doubled blank gap (the classic "weird spacing" on the phone).
  // Drop newlines that touch block tags, and collapse 3+ blank lines in prose.
  s = s.replace(/\n{3,}/g, "\n\n");
  s = s.replace(/\n+(<(?:div|ul|ol|blockquote)\b)/g, "$1");
  s = s.replace(/(<\/(?:div|ul|ol|blockquote)>)\n+/g, "$1");
  s = s.replace(/B(\d+)/g, (_, i) => blocks[+i]);
  // Same newline-drop for restored code fences (they were placeholders while
  // the rules above ran, so the block-tag pass couldn't see them).
  s = s.replace(/\n+(<pre class="code")/g, "$1");
  s = s.replace(/(<\/pre>)\n+/g, "$1");
  return s;
}

// setBubbleRich renders assistant text as the safe markdown subset. Used for the
// FINAL assistant message and stored assistant messages; streaming deltas stay
// plain text (textContent) until the authoritative final message replaces them.
function setBubbleRich(bubble, text) {
  bubble.innerHTML = renderRich(text);
}

/* ------------------------------------------------------------------ *
 * Query string builder (peer + token)
 * ------------------------------------------------------------------ */
function qs(extra) {
  const p = new URLSearchParams();
  if (state.peer) p.set("peer", state.peer);
  if (state.token) p.set("token", state.token);
  if (extra) for (const [k, v] of Object.entries(extra)) p.set(k, v);
  const s = p.toString();
  return s ? "?" + s : "";
}

/* ------------------------------------------------------------------ *
 * RPC helper — POST relative "rpc"
 * ------------------------------------------------------------------ */
// RPC_TIMEOUT bounds a single request. All mobile RPCs return quickly (the agent
// TURN runs decoupled server-side and streams over /sse, so even sendMessage
// returns fast) — so a request still pending after this long means a hung/black-
// holed peer or gateway, and we'd rather fail cleanly than leave the UI awaiting
// forever. Generous so a merely-slow LAN never trips it.
const RPC_TIMEOUT = 25000;
async function rpc(method, params = {}) {
  const ctl = new AbortController();
  const timer = setTimeout(() => ctl.abort(), RPC_TIMEOUT);
  let res;
  try {
    res = await fetch("rpc" + qs(), {
      method: "POST",
      headers: {
        "Content-Type": "application/json",
        ...(state.token ? { Authorization: "Bearer " + state.token } : {}),
      },
      body: JSON.stringify({ jsonrpc: "2.0", id: ++state.rpcId, method, params }),
      signal: ctl.signal,
    });
  } catch (e) {
    if (e && e.name === "AbortError") {
      throw new Error(`rpc ${method}: timed out`);
    }
    throw e;
  } finally {
    clearTimeout(timer);
  }
  if (res.status === 502) {
    // The gateway reports the attached peer is offline — it left the swarm, or
    // (common) restarted with a NEW handle so our stored one is now dead.
    // Silently retrying a dead handle never recovers, so bounce back to the peer
    // picker where the user can re-select the peer's current handle.
    let msg = "peer offline";
    try {
      const j = await res.json();
      if (j && j.error && j.error.message) msg = j.error.message;
    } catch (_) {
      /* body not JSON — keep default */
    }
    peerGone(msg);
    const err = new Error(`rpc ${method}: ${msg}`);
    err.peerOffline = true; // callers short-circuit: we already bounced + notified
    throw err;
  }
  if (res.status === 503) {
    // The peer is REACHABLE but has no usable session right now — the Swarm
    // Desktop peer's serve bridge returns 503 "no active session" when the
    // desktop app itself isn't connected. A phone user who picked that peer with
    // the desktop closed would otherwise see a cryptic "HTTP 503". Tell them what
    // to do and bounce to the picker (reuse the peerGone recovery + short-circuit).
    let msg = "no active session";
    try {
      const j = await res.json();
      if (j && j.error) msg = typeof j.error === "string" ? j.error : j.error.message || msg;
    } catch (_) {
      /* keep default */
    }
    peerGone(
      /no active session/i.test(msg)
        ? "That peer's app isn't connected"
        : msg
    );
    const err = new Error(`rpc ${method}: ${msg}`);
    err.peerOffline = true;
    throw err;
  }
  if (!res.ok) throw new Error(`rpc ${method}: HTTP ${res.status}`);
  const json = await res.json();
  if (json.error) throw new Error(json.error.message || `rpc ${method} error`);
  return json.result;
}

// peerGone bounces the UI back to the peer picker when the attached peer is no
// longer reachable. Debounced: several in-flight RPCs may all 502 at once.
let peerGoneAt = 0;
function peerGone(msg) {
  const now = Date.now();
  if (now - peerGoneAt < 1500) return;
  peerGoneAt = now;
  notice((msg || "Peer offline") + " — pick a peer to reconnect");
  backToPeers();
}

/* ------------------------------------------------------------------ *
 * Toast + notice
 * ------------------------------------------------------------------ */
let toastTimer = null;
function toast(msg, isErr) {
  const t = $("toast");
  t.textContent = msg;
  t.classList.toggle("err", !!isErr);
  t.classList.remove("hidden");
  clearTimeout(toastTimer);
  toastTimer = setTimeout(() => t.classList.add("hidden"), 3500);
}

let noticeTimer = null;
function notice(msg) {
  const n = $("notice-bar");
  n.textContent = msg;
  n.classList.remove("hidden");
  clearTimeout(noticeTimer);
  noticeTimer = setTimeout(() => n.classList.add("hidden"), 4000);
}

/* ------------------------------------------------------------------ *
 * Peer picker
 * ------------------------------------------------------------------ */
// A cheap signature of the peer set so the background poll can skip re-rendering
// (and its flicker) when nothing changed.
function peerSig(peers) {
  return peers
    .map((p) => [p.handle || p.name, p.status, p.model, p.type].join("|"))
    .join(",");
}

// splitRemoteHandle splits a LAN-gossip handle ("host~handle") into its
// machine and bare-handle parts. Local peers return machine "".
function splitRemoteHandle(handle) {
  const i = String(handle || "").indexOf("~");
  if (i <= 0) return { machine: "", bare: handle || "" };
  return { machine: handle.slice(0, i), bare: handle.slice(i + 1) };
}

function renderPeers(peers) {
  peerList.innerHTML = "";
  if (!peers.length) {
    const empty = el("div", "empty");
    empty.appendChild(el("h3", null, "No peers found"));
    empty.appendChild(el("div", null, "Start a swarm daemon/TUI on the LAN, then tap ⟳."));
    peerList.appendChild(empty);
    return;
  }
  // Local (this machine) peers first, then remote machines grouped together —
  // the phone is usually driving the box it scanned, with the rest of the LAN
  // one flick away.
  const sorted = peers.slice().sort((a, b) => {
    const ra = a.type === "remote" ? 1 : 0;
    const rb = b.type === "remote" ? 1 : 0;
    if (ra !== rb) return ra - rb;
    return String(a.handle || a.name).localeCompare(String(b.handle || b.name));
  });
  let lastPeer = "";
  try { lastPeer = localStorage.getItem("swarm.lastPeer") || ""; } catch (_) {}
  let lastGroup = null;
  for (const p of sorted) {
    const handle = p.handle || p.name || "";
    const { machine, bare } = splitRemoteHandle(handle);
    const group = machine || "this machine";
    if (group !== lastGroup && sorted.some((x) => x.type === "remote")) {
      // Only draw section headers when the list actually spans machines.
      peerList.appendChild(el("div", "peer-group", machine ? "on " + machine : "this machine"));
      lastGroup = group;
    }
    const card = el("div", "peer-card");
    card.appendChild(el("div", "pname", p.name && p.name !== handle ? p.name : bare || "(unnamed)"));
    const meta = el("div", "pmeta");
    if (p.type) meta.appendChild(el("span", "badge", p.type === "remote" ? "remote · " + machine : p.type));
    if (p.status) meta.appendChild(el("span", "badge status-" + String(p.status).toLowerCase(), p.status));
    if (handle && handle === lastPeer) meta.appendChild(el("span", "badge last", "last used"));
    if (p.model) meta.appendChild(el("span", null, p.model));
    card.appendChild(meta);
    // When several peers share a display name (two "Swarm Desktop" instances),
    // the unique handle is the only way to tell them apart — show it.
    if (p.name && p.name !== handle && bare && bare !== p.name) {
      card.appendChild(el("div", "pmeta", bare));
    }
    if (p.workspace) card.appendChild(el("div", "pmeta", p.workspace));
    card.addEventListener("click", () => selectPeer(handle, card));
    peerList.appendChild(card);
  }
}

async function loadPeers() {
  peerList.innerHTML = "";
  peerList.appendChild(el("div", "empty", "Loading peers…"));
  let data;
  try {
    const res = await fetch("api/peers" + qs(), {
      headers: state.token ? { Authorization: "Bearer " + state.token } : {},
    });
    if (!res.ok) throw new Error("HTTP " + res.status);
    data = await res.json();
  } catch (e) {
    peerList.innerHTML = "";
    const empty = el("div", "empty");
    empty.appendChild(el("h3", null, "Can't reach gateway"));
    empty.appendChild(el("div", null, String(e.message || e)));
    peerList.appendChild(empty);
    return;
  }
  const peers = (data && data.peers) || [];
  state.peerSig = peerSig(peers);
  renderPeers(peers);
}

// Silent background refresh WHILE the peer picker is visible, so a peer coming
// online / going away / changing handle appears without a manual ⟳ tap. Only
// re-renders when the peer set actually changed (no flicker), self-guards to the
// picker view, and ignores transient fetch errors (keeps the last good list).
async function pollPeers() {
  if (peerView.classList.contains("hidden")) return;
  let data;
  try {
    const res = await fetch("api/peers" + qs(), {
      headers: state.token ? { Authorization: "Bearer " + state.token } : {},
    });
    if (!res.ok) return;
    data = await res.json();
  } catch (_) {
    return;
  }
  if (peerView.classList.contains("hidden")) return; // may have navigated during await
  const peers = (data && data.peers) || [];
  const sig = peerSig(peers);
  if (sig !== state.peerSig) {
    state.peerSig = sig;
    renderPeers(peers);
  }
}

async function selectPeer(handle, card) {
  if (!handle || state.connecting) return;
  state.connecting = true;
  if (card) card.classList.add("connecting");
  state.peer = handle;
  try {
    try {
      await fetch("api/select" + qs(), {
        method: "POST",
        headers: {
          "Content-Type": "application/json",
          ...(state.token ? { Authorization: "Bearer " + state.token } : {}),
        },
        body: JSON.stringify({ handle }),
      });
    } catch (e) {
      // Selection is also honored per-request via ?peer=, so continue anyway.
      console.warn("api/select failed (continuing via ?peer=)", e);
    }
    // Probe the peer BEFORE switching views. Tapping a dead peer used to jump
    // to an empty chat, fail, and bounce back — jarring. Now the card shows
    // "connecting…" and a failure stays on the picker with a clear toast.
    let snap = null;
    try {
      snap = await rpc("client.snapshot", {});
    } catch (e) {
      state.peer = "";
      toast("Can't attach: " + (e.peerOffline ? "peer is offline" : e.message), true);
      loadPeers();
      return;
    }
    try { localStorage.setItem("swarm.lastPeer", handle); } catch (_) {}
    applySnapshot(snap);
    show("session");
    renderHeader();
    chat.innerHTML = "";
    await loadMessages();
    await recoverPendingApproval();
    openStream();
  } finally {
    state.connecting = false;
    if (card) card.classList.remove("connecting");
  }
}

/* ------------------------------------------------------------------ *
 * Session header
 * ------------------------------------------------------------------ */
// Compact token count ("48.2k") — the raw number ate the narrow header.
function fmtTok(n) {
  n = n || 0;
  if (n >= 1e6) return (n / 1e6).toFixed(1) + "M";
  if (n >= 1000) return (n / 1000).toFixed(1) + "k";
  return String(n);
}

function renderHeader() {
  // Line 1 is WHO we're attached to — on a phone with several peers this is
  // the one fact that was missing from the header. Line 2: mode · model
  // (tappable) · compact token/context meter. Provider rides as a tooltip.
  const { machine, bare } = splitRemoteHandle(state.peer);
  const peerEl = $("s-peer");
  peerEl.textContent = bare ? bare + (machine ? " @ " + machine : "") : state.provider || "—";
  peerEl.title = state.provider || "";
  $("s-model").textContent = state.model || "—";
  $("s-mode").textContent = state.mode || "mode";
  const tokEl = $("s-tokens");
  let label = fmtTok(state.totalTokens) + " tok";
  if (state.pctUsed != null) label += " · " + Math.round(state.pctUsed) + "% ctx";
  tokEl.textContent = label;
  // Warn as the context fills toward auto-compaction (~80%+).
  tokEl.classList.toggle("ctx-warn", state.pctUsed != null && state.pctUsed >= 80);
}

async function loadSession() {
  try {
    const snap = await rpc("client.snapshot", {});
    applySnapshot(snap);
  } catch (e) {
    if (e.peerOffline) return; // peerGone already bounced us to the picker
    toast("snapshot failed: " + e.message, true);
  }
  await loadMessages();
  await recoverPendingApproval();
}

// If a tool approval fired BEFORE we attached (or during an SSE reconnect gap),
// we missed the approval_requested event and the turn is now blocked with no
// prompt on screen — a stuck session with no recourse. On attach, ask the peer
// for any approval still awaiting a decision and surface it so the user can
// resolve it. The RPC only returns callIDs (the reason/permissions live in the
// missed event), so we show a generic prompt; Approve/Deny still unblock the
// turn by callID. Best-effort: older peers may not implement it.
async function recoverPendingApproval() {
  if (pendingApproval) return; // a live one is already showing
  let ids = [];
  try {
    const pa = await rpc("client.pendingApprovals", {});
    ids = (pa && pa.pending) || [];
  } catch (_) {
    return;
  }
  if (ids.length) {
    showApproval({
      callID: ids[0],
      reason: "A tool is awaiting your approval (details were sent before you connected).",
    });
  }
}

function applySnapshot(snap) {
  if (!snap) return;
  state.provider = snap.Provider ?? state.provider;
  state.model = snap.Model ?? state.model;
  state.mode = snap.OperatingMode ?? state.mode;
  state.activeConvID = snap.ActiveConvID ?? state.activeConvID;
  if (snap.TotalTokens != null) state.totalTokens = snap.TotalTokens;
  if (snap.IsStreaming != null) setStreaming(snap.IsStreaming);
  renderHeader();
}

/* ------------------------------------------------------------------ *
 * Messages / bubbles
 * ------------------------------------------------------------------ */
function atBottom() {
  return chat.scrollHeight - chat.scrollTop - chat.clientHeight < 80;
}
function scrollToBottom(force) {
  if (force || atBottom()) chat.scrollTop = chat.scrollHeight;
}

async function loadMessages() {
  let data;
  try {
    data = await rpc("client.getMessages", { convID: "" });
  } catch (e) {
    // The PWA always asks for the ACTIVE conversation (empty convID). A
    // freshly-started / just-attached session — or any peer still running an
    // older daemon build that lacks the server-side degrade — reports that as
    // "conversation not found" when it simply has no stored transcript yet.
    // That is a normal empty state, not a failure: render "no messages yet" so
    // the user can just start typing, instead of a scary red error. Only a
    // genuinely unexpected failure still toasts; either way the chat stays
    // usable. This keeps the mobile app working against ANY peer regardless of
    // its binary version.
    if (e.peerOffline) return; // peerGone already bounced us to the picker
    chat.innerHTML = "";
    state.curBubble = null;
    if (!/not\s*found/i.test((e && e.message) || "")) {
      toast("getMessages failed: " + e.message, true);
    }
    chat.appendChild(el("div", "sys-notice", "No messages yet — say hello."));
    return;
  }
  chat.innerHTML = "";
  state.curBubble = null;
  state.toolChips = {}; // drop refs to the cleared chips before rebuilding
  state.thinkChip = null; // ditto for the thinking chip (its node is gone now)
  state.subBar = null; // and the sub-agent status line
  const msgs = (data && data.messages) || [];
  if (data && data.convID) state.activeConvID = data.convID;
  if (!msgs.length) {
    chat.appendChild(el("div", "sys-notice", "No messages yet — say hello."));
    return;
  }
  for (const m of msgs) renderMessage(m);
  scrollToBottom(true);
}

// Render a stored message. Roles: user / assistant / tool / system.
function renderMessage(m) {
  const role = String(m.Role || m.role || "assistant").toLowerCase();
  const content = m.Content ?? m.content ?? "";
  if (role === "user") {
    addBubble("user", content);
  } else if (role === "tool") {
    // A tool RESULT message carries tool_results[{call_id,name,output,error}].
    // Attach each to the chip the assistant's tool_call already created (by id),
    // so a resumed transcript shows call ARGS + RESULT together — exactly like
    // the live stream. Fall back to a standalone named chip if no call chip
    // exists (so we never regress to a nameless "tool" chip with no output).
    const results = m.tool_results || m.toolResults || m.ToolResults || [];
    if (results.length) {
      for (const r of results) {
        const id = r.call_id || r.callID || r.callId || r.id || r.ID;
        if (!id || !(state.toolChips && state.toolChips[id])) {
          addToolChip(r.name || r.Name || "tool", "", id);
        }
        addToolResult(id, r.output ?? r.Output, r.error ?? r.Error);
      }
    } else if (content) {
      addToolChip(m.Name || m.name || "tool", content);
    }
  } else if (role === "system") {
    chat.appendChild(el("div", "sys-notice", content));
  } else {
    // Assistant. Prefer ordered_blocks — the authoritative interleaved order of
    // content/tool_call blocks — so a message that says "let me check" THEN calls
    // a tool (as anthropic models do) renders text-then-chip, not reordered.
    // Each block renders in sequence; empty content blocks make no bubble. Fall
    // back to tool_calls-then-content when ordered_blocks is absent.
    const blocks = m.ordered_blocks || m.orderedBlocks || m.OrderedBlocks;
    if (Array.isArray(blocks) && blocks.length) {
      for (const blk of blocks) {
        const bt = String(blk.type || blk.Type || "").toLowerCase();
        if (bt === "tool_call") {
          const tc = blk.tool_call || blk.toolCall || blk.ToolCall || {};
          const args = tc.parameters ?? tc.Parameters ?? tc.arguments ?? tc.input ?? "";
          addToolChip(tc.name || tc.Name || "tool", args, tc.id || tc.ID);
        } else {
          const txt = blk.content ?? blk.Content ?? blk.text ?? "";
          if (txt && txt.trim()) setBubbleRich(addBubble("assistant", ""), txt);
        }
      }
    } else {
      const calls = m.tool_calls || m.toolCalls || m.ToolCalls || [];
      for (const c of calls) {
        const args = c.parameters ?? c.Parameters ?? c.arguments ?? c.input ?? "";
        addToolChip(c.name || c.Name || "tool", args, c.id || c.ID);
      }
      if (content && content.trim()) {
        setBubbleRich(addBubble("assistant", ""), content);
      }
    }
  }
}

function addBubble(kind, text) {
  const b = el("div", "bubble " + kind, text || "");
  chat.appendChild(b);
  scrollToBottom();
  return b;
}

// toolHint pulls the single most meaningful argument out of a tool call — the
// command a Bash runs, the file a Read reads, the query a search searches — so
// the COLLAPSED summary line already tells the story and a phone user can
// follow the agent's work without expanding every chip.
const HINT_KEYS = [
  "command", "file_path", "filePath", "path", "pattern", "query", "url",
  "description", "prompt", "message", "title", "name",
];
function toolHint(args) {
  let o = args;
  if (typeof o === "string") {
    const s = o.trim();
    if (!s) return "";
    if (s[0] === "{") {
      try { o = JSON.parse(s); } catch (_) { return s.slice(0, 80); }
    } else {
      return s.replace(/\s+/g, " ").slice(0, 80);
    }
  }
  if (!o || typeof o !== "object") return "";
  for (const k of HINT_KEYS) {
    const v = o[k];
    if (typeof v === "string" && v.trim()) {
      return v.trim().replace(/\s+/g, " ").slice(0, 80);
    }
  }
  return "";
}

// addToolChip renders a collapsed tool-call chip: status dot (spinning while
// the call runs live), tool name, one-line arg hint. Expanding shows the full
// args (omitted entirely when there are none — an empty <pre> used to leave a
// weird padded gap under the summary). `pending` is set only for LIVE calls;
// stored transcripts attach their result immediately so no spinner is shown.
function addToolChip(name, body, id, pending) {
  const d = document.createElement("details");
  d.className = "tool-chip";
  const sum = document.createElement("summary");
  sum.appendChild(el("span", "tc-status" + (pending ? " pending" : ""), pending ? "●" : ""));
  sum.appendChild(el("span", "tc-name", name));
  const hint = toolHint(body);
  if (hint) sum.appendChild(el("span", "tc-hint", hint));
  d.appendChild(sum);
  const argText =
    typeof body === "string" ? body : body == null ? "" : JSON.stringify(body, null, 2);
  if (argText && argText !== "{}") d.appendChild(el("pre", "tool-args", argText));
  chat.appendChild(d);
  keepCursorLast();
  if (id) {
    if (!state.toolChips) state.toolChips = {};
    state.toolChips[id] = d;
  }
  scrollToBottom();
  return d;
}

// errText extracts a human-readable string from a tool_result Error, which may
// be a plain string OR a structured error object with a top-level .Message.
// Backend errors append internal trace breadcrumbs like "(error_id=err_ab12)"
// — noise for a phone user staring at a failed tool. Strip them (and collapse
// any doubled whitespace left behind) so the chip shows just the real message.
function cleanErr(s) {
  return String(s)
    .replace(/\s*\(error_id=[^)]*\)/g, "")
    .replace(/[ \t]{2,}/g, " ")
    .trim();
}
function errText(e) {
  if (e == null) return "";
  if (typeof e === "string") return cleanErr(e);
  if (typeof e === "object") {
    if (typeof e.Message === "string" && e.Message) return cleanErr(e.Message);
    if (typeof e.message === "string" && e.message) return cleanErr(e.message);
    try {
      return JSON.stringify(e);
    } catch (_) {
      return "error";
    }
  }
  return cleanErr(e);
}

// Append a tool's RESULT (output or error) to its chip, correlated by call ID.
// The chip stays collapsed by default; output is truncated so a large result
// (e.g. a file dump) can't bloat the DOM. Nothing to attach if the call chip
// wasn't rendered (e.g. reconnected mid-turn).
function addToolResult(id, output, error) {
  const d = id && state.toolChips ? state.toolChips[id] : null;
  if (!d) return;
  // Settle the summary status dot: spinner (live) / blank (stored) → ✓ or ✗.
  const st = d.querySelector(".tc-status");
  if (st) {
    const failed = errText(error) !== "";
    st.textContent = failed ? "✗" : "✓";
    st.className = "tc-status " + (failed ? "err" : "ok");
  }
  // The tool_result Error is a STRUCTURED error object (nested Cause chain), not
  // a string — String(error) yields "[object Object]". Pull the human-readable
  // .Message; fall back to the Output string (which also carries the error text)
  // or a JSON dump so we never show "[object Object]".
  const errStr = errText(error);
  const isErr = errStr !== "";
  let text = isErr
    ? errStr || String(output == null ? "" : output)
    : String(output == null ? "" : output);
  const MAX = 2000;
  if (text.length > MAX) {
    text = text.slice(0, MAX) + "\n… (" + (text.length - MAX) + " more chars)";
  }
  const pre = el(
    "pre",
    "tool-result" + (isErr ? " err" : ""),
    (isErr ? "✗ error\n" : "→ result\n") + text
  );
  d.appendChild(pre);
  scrollToBottom();
}

/* ------------------------------------------------------------------ *
 * Streaming state
 * ------------------------------------------------------------------ */
function setStreaming(on) {
  state.streaming = on;
  // During a turn the send button becomes a STOP control (client.cancel) so a
  // long/runaway turn can be halted on mobile. Previously it was merely
  // disabled, leaving the user with no way to stop.
  const btn = $("send");
  if (btn) {
    btn.disabled = false;
    btn.textContent = on ? "■" : "➤";
    btn.classList.toggle("stop", on);
    btn.setAttribute("aria-label", on ? "Stop" : "Send");
  }
  if (!on && state.curBubble) {
    state.curBubble.classList.remove("streaming");
    if (!state.curBubble.textContent) state.curBubble.remove();
    state.curBubble = null;
  }
  // Turn ended: drop the thinking-chip reference so the NEXT turn starts a fresh
  // chip (the finished one stays in the transcript as a collapsed record).
  if (!on) state.thinkChip = null;
  // Turn ended: any chip still spinning never got its result event (missed
  // during a reconnect gap, or an ID-less call) — stop the animation so the
  // transcript doesn't show phantom "still running" work forever.
  if (!on) {
    chat.querySelectorAll(".tc-status.pending").forEach((s) => {
      s.textContent = "";
      s.className = "tc-status";
    });
  }
  // Turn ended: remove the transient sub-agent status line (live indicator only).
  if (!on && state.subBar) {
    state.subBar.remove();
    state.subBar = null;
  }
}

// cancelTurn asks the daemon to abort the in-flight turn (the daemon-side
// context is cancelled; the stream ends). Best-effort: the UI drops back out of
// streaming state regardless so the composer is usable again.
async function cancelTurn() {
  try {
    await rpc("client.cancel", {});
  } catch (e) {
    if (!e.peerOffline) toast("stop failed: " + e.message, true);
  }
  setStreaming(false);
}

function ensureAssistantBubble() {
  if (!state.curBubble) {
    state.curBubble = addBubble("assistant", "");
    state.curBubble.classList.add("streaming");
  }
  return state.curBubble;
}

// keepCursorLast moves a still-EMPTY streaming bubble (the blinking-cursor
// placeholder stream_start creates) below whatever was just appended. Without
// this, thinking/tool chips that arrive before the first text delta render
// BELOW the cursor, and the reply then streams in ABOVE the agent's activity —
// the transcript read out of order.
function keepCursorLast() {
  const b = state.curBubble;
  if (b && !b.textContent && chat.lastElementChild !== b) chat.appendChild(b);
}

// Extended-thinking (reasoning) is streamed as its own "thinking" agent update.
// It must NEVER go into the answer bubble — render it in a dim, collapsed chip
// (one per turn) so it's available for the curious but out of the way. Returns
// the <pre> the caller writes reasoning text into.
function ensureThinkingChip() {
  if (state.thinkChip) return state.thinkChip;
  const d = document.createElement("details");
  d.className = "think-chip";
  d.appendChild(el("summary", null, "💭 thinking"));
  const pre = el("pre", "think-body", "");
  d.appendChild(pre);
  chat.appendChild(d);
  keepCursorLast();
  state.thinkChip = pre;
  return pre;
}

// A one-line "sub-agent working" status shown while a delegated sub-agent runs.
// It's transient (removed at turn end by setStreaming(false)) — a live indicator,
// not a transcript record.
function ensureSubAgentBar() {
  if (state.subBar && state.subBar.isConnected) return state.subBar;
  const bar = el("div", "subagent-bar", "");
  chat.appendChild(bar);
  keepCursorLast();
  state.subBar = bar;
  return bar;
}
// Derive a short human hint from a sub-agent update's NESTED update (its actual
// work). Falls back to a generic "working…" so an unexpected shape is harmless.
function subAgentActivity(u) {
  const inner = u.Update ?? u.update ?? {};
  const tool = pickToolName(inner);
  if (tool) return "using " + tool;
  const txt = pickText(inner);
  if (txt) return txt.replace(/\s+/g, " ").trim().slice(0, 60);
  return "working…";
}

/* ------------------------------------------------------------------ *
 * SSE — defensive envelope handling (shapes vary)
 * ------------------------------------------------------------------ */
function pickText(u) {
  if (u == null) return "";
  if (typeof u === "string") return u;
  // Try common delta fields (varied casing per CONTRACT note).
  return u.Content ?? u.Delta ?? u.Text ?? u.content ?? u.delta ?? u.text ?? "";
}
function pickToolName(u) {
  if (!u || typeof u !== "object") return "";
  return u.ToolName ?? u.Name ?? u.toolName ?? u.name ?? "";
}
function pickToolArgs(u) {
  if (!u || typeof u !== "object") return undefined;
  // The SDK's ToolCallUpdate marshals the tool's arguments under `Parameters`
  // (no json tag → Go field name); check that FIRST. Without it the tool chip
  // rendered with an empty body — you couldn't see what the tool did (e.g. the
  // file path a Read read). The other names are defensive for varied shapes.
  return (
    u.Parameters ??
    u.parameters ??
    u.Args ??
    u.Input ??
    u.args ??
    u.input ??
    u.Arguments ??
    u.arguments
  );
}

function handleAgent(env) {
  const agent = env.agent || env.Agent || {};
  const update = agent.update ?? agent.Update ?? agent;
  const type = agent.type ?? agent.Type ?? "";

  // Token accounting updates drive the header counter; they carry no text.
  if (type === "token_count" || type === "turn_usage") {
    const inTok = update.InputTokens ?? update.inputTokens ?? 0;
    const outTok = update.OutputTokens ?? update.outputTokens ?? 0;
    state.totalTokens = update.TotalTokens ?? inTok + outTok;
    // PctUsed (0-100) is how full the context window is; surfacing it lets a
    // phone user see when they're approaching auto-compaction. Only token_count
    // carries it — turn_usage does not, so guard on != null.
    if (update.PctUsed != null) state.pctUsed = update.PctUsed;
    renderHeader();
    return;
  }
  // Provider fallback/retry status — informational only, never text.
  if (type === "fallback") return;
  // Extended thinking / reasoning. MUST be handled before the generic delta
  // branch below — otherwise pickText() would splice the reasoning straight into
  // the answer bubble and corrupt the reply. Route it to its own dim chip.
  if (type === "thinking") {
    const t = pickText(update);
    if (t) {
      const pre = ensureThinkingChip();
      const append = update.Append ?? update.append;
      if (append === false) pre.textContent = t;
      else pre.textContent += t;
      scrollToBottom();
    }
    return;
  }
  // Sub-agent activity (a delegated Subagent tool run). The sub-agent's real
  // work is NESTED in update.Update — the main answer bubble stays empty while
  // it runs, which looked frozen. Show a transient live-status line so the user
  // knows work is happening. Never touches the answer bubble.
  if (type === "sub_agent_update") {
    const name = update.AgentName ?? update.agentName ?? "sub-agent";
    ensureSubAgentBar().textContent = "🤖 " + name + " · " + subAgentActivity(update);
    scrollToBottom();
    return;
  }
  // The final assistant message is authoritative: REPLACE whatever the delta
  // stream built (appending it doubled every reply), then end the bubble.
  if (type === "assistant_message") {
    const b = ensureAssistantBubble();
    setBubbleRich(b, pickText(update));
    b.classList.remove("streaming");
    state.curBubble = null;
    scrollToBottom();
    return;
  }

  // A tool's RESULT (output/error) arrives as its own event, correlated to the
  // call by ID. Attach it to the call's chip so expanding shows call + result.
  if (type === "tool_result") {
    addToolResult(update.ID ?? update.id, update.Output ?? update.output, update.Error ?? update.error);
    return;
  }
  // Only real tool CALLS get a chip. The agent also streams many
  // hook_execution events (task-enforcement, skills-budget, plan-mode, …) that
  // carry a ToolName but are internal machinery — rendering them produced a
  // pile of empty "tool · X" chips (8+ per single tool call). Skip anything with
  // a HookName / of type hook_execution.
  const isHook = type === "hook_execution" || !!(update.HookName || update.hookName);
  const toolName = pickToolName(update);
  if (toolName && !isHook) {
    const args = pickToolArgs(update);
    // pending=true: this is a LIVE call — show the running dot until its
    // tool_result event settles it to ✓/✗.
    addToolChip(toolName, args !== undefined ? args : "", update.ID ?? update.id, true);
    return;
  }
  if (isHook) return; // internal hook lifecycle event — nothing to render
  const delta = pickText(update);
  if (delta) {
    const b = ensureAssistantBubble();
    // Append:false means the update carries the full accumulated text —
    // replace, don't concatenate (concatenating doubles the reply).
    const append = update.Append ?? update.append;
    if (append === false) b.textContent = delta;
    else b.textContent += delta;
    scrollToBottom();
    return;
  }
  // Unknown shape — never crash, just log for debugging.
  console.debug("SSE agent update: unknown shape", update);
}

function handleEnvelope(kind, env) {
  // Multiplexed peers (one client driving SEVERAL conversations at once — e.g. a
  // remote engine) interleave all conversations on one /sse stream, tagging each
  // event with its originating conversation (source.convID). Drop events for a
  // DIFFERENT conversation than the one on screen so another conversation's
  // stream can't bleed into this transcript (wrong bubble, spurious cursor, an
  // approval prompt for some other chat). Two escape hatches keep it safe:
  //   - events with no convID (single-conversation peers) always pass, and
  //   - conversation-navigation events (conv_switched/created/updated) always
  //     pass, since those are how we learn the active conversation changed.
  const evConv = env && env.source && (env.source.convID || env.source.ConvID);
  const navKind =
    kind === "conv_switched" || kind === "conv_created" || kind === "conv_updated";
  if (evConv && state.activeConvID && evConv !== state.activeConvID && !navKind) {
    return;
  }
  switch (kind) {
    case "stream_start":
      setStreaming(true);
      ensureAssistantBubble();
      break;
    case "stream_end":
      setStreaming(false);
      // A compaction that fired mid-turn deferred its reload to avoid wiping the
      // live bubble — do it now that the turn is done.
      if (state.reloadAfterStream) {
        state.reloadAfterStream = false;
        loadMessages();
      }
      break;
    case "agent":
      handleAgent(env);
      break;
    case "mode_changed":
      state.mode = (env.payload && (env.payload.mode || env.payload.Mode)) || state.mode;
      renderHeader();
      break;
    case "model_changed":
      if (env.payload) {
        state.model = env.payload.model || env.payload.Model || state.model;
        state.provider = env.payload.provider || env.payload.Provider || state.provider;
      }
      renderHeader();
      break;
    case "conv_switched":
    case "conv_created":
      // The ACTIVE conversation actually changed — reload its transcript.
      loadMessages();
      break;
    case "conv_updated":
      // Metadata-only (the naming agent set a title — no new messages). Must
      // NOT loadMessages(): that clears chat + re-fetches, which would wipe a
      // live streaming bubble if the title lands mid-turn, and flicker
      // otherwise. Just refresh the conversations list if it happens to be open.
      if (!$("convos-sheet").classList.contains("hidden")) fetchConvos();
      break;
    case "approval_requested":
      showApproval(env.payload || {});
      break;
    case "compaction": {
      // Compaction REPLACES the conversation's messages with a compact summary
      // (client/extensions.go), so our on-screen transcript is now stale. Reload
      // it so what's shown matches the real (compacted) conversation — deferring
      // to stream_end if a turn is mid-flight so we don't wipe the live bubble.
      const p = env.payload || {};
      const saved = (p.beforeTokens ?? p.BeforeTokens ?? 0) - (p.afterTokens ?? p.AfterTokens ?? 0);
      notice(saved > 0 ? `Context compacted — saved ~${saved} tokens` : "Context compacted");
      if (state.streaming) state.reloadAfterStream = true;
      else loadMessages();
      break;
    }
    case "error": {
      const msg = (env.payload && (env.payload.message || env.payload.Message)) || "error";
      toast(msg, true);
      break;
    }
    default:
      // peer_joined, task events, etc. — optional; ignore.
      break;
  }
}

function openStream() {
  closeStream();
  let es;
  try {
    es = new EventSource("sse" + qs());
  } catch (e) {
    console.warn("EventSource failed", e);
    return;
  }
  state.es = es;

  const kinds = [
    "stream_start", "stream_end", "agent", "mode_changed", "model_changed",
    "conv_switched", "conv_created", "conv_updated", "approval_requested",
    "compaction", "error",
  ];
  const onEvent = (kind) => (ev) => {
    let env = {};
    try { env = ev.data ? JSON.parse(ev.data) : {}; } catch (_) { /* ignore */ }
    try { handleEnvelope(kind, env); } catch (e) { console.error("handle", kind, e); }
  };
  for (const k of kinds) es.addEventListener(k, onEvent(k));
  // Fallback: unnamed events carry {kind,...} in data.
  es.onmessage = (ev) => {
    let env = {};
    try { env = ev.data ? JSON.parse(ev.data) : {}; } catch (_) { return; }
    const kind = env.kind || env.Kind;
    if (kind) { try { handleEnvelope(kind, env); } catch (e) { console.error(e); } }
  };

  es.onopen = () => {
    state.backoff = 1000;
    // If this open follows a DROP (reconnect), re-sync: events may have been
    // missed during the gap — most importantly a stream_end, which would leave
    // the UI stuck showing "streaming" (stop button + cursor) forever. Re-running
    // loadSession re-fetches the snapshot (whose IsStreaming resets the button)
    // and the transcript, so the view matches reality after a blip. The initial
    // open (right after attach's loadSession) skips this — nothing to re-sync.
    if (state.streamDropped) {
      state.streamDropped = false;
      loadSession();
    }
  };
  es.onerror = () => {
    // EventSource auto-reconnects; we add our own backoff so a permanently
    // dead peer doesn't hammer the gateway. Mark that we dropped so the next
    // successful open re-syncs state.
    state.streamDropped = true;
    closeStream();
    const wait = state.backoff;
    state.backoff = Math.min(state.backoff * 2, 15000);
    setTimeout(() => { if (!sessionView.classList.contains("hidden")) openStream(); }, wait);
  };
}

function closeStream() {
  if (state.es) { try { state.es.close(); } catch (_) {} state.es = null; }
}

/* ------------------------------------------------------------------ *
 * Approval sheet
 * ------------------------------------------------------------------ */
let pendingApproval = null;
function showApproval(payload) {
  pendingApproval = payload || {};
  const callID = payload.callID || payload.CallID || payload.id || payload.ID || "";
  pendingApproval._callID = callID;
  // Render a READABLE prompt (reason + requested permissions) rather than raw
  // JSON — a phone user needs to understand what they're approving at a glance.
  // Permissions are plain strings ("file_write", "bash_execute"); prettify them.
  // Unknown shapes fall back to JSON so nothing is ever hidden. textContent
  // keeps it XSS-safe.
  const reason = payload.reason || payload.Reason || "";
  const perms = payload.permissions || payload.Permissions || [];
  const lines = [];
  if (reason) lines.push(reason);
  if (Array.isArray(perms) && perms.length) {
    if (lines.length) lines.push("");
    lines.push("Permissions requested:");
    for (const p of perms) {
      lines.push(
        "  • " + (typeof p === "string" ? p.replace(/_/g, " ") : JSON.stringify(p))
      );
    }
  }
  if (!lines.length) lines.push(JSON.stringify(payload, null, 2));
  $("approval-body").textContent = lines.join("\n");
  $("approval-sheet").classList.remove("hidden");
}
async function respondApproval(allow) {
  const p = pendingApproval;
  $("approval-sheet").classList.add("hidden");
  pendingApproval = null;
  if (!p) return;
  try {
    await rpc("client.respondApproval", { callID: p._callID, allow });
  } catch (e) {
    if (!e.peerOffline) toast("approval failed: " + e.message, true);
  }
}

/* ------------------------------------------------------------------ *
 * Composer
 * ------------------------------------------------------------------ */
async function sendMessage() {
  const input = $("input");
  const text = input.value.trim();
  if (!text) return;
  if (state.streaming) {
    // Enter while a turn is running used to no-op SILENTLY — the message just
    // didn't send and nothing said why. Tell the user what's happening.
    notice("Still responding — tap ■ to stop, then send");
    return;
  }
  input.value = "";
  autoGrow();
  const sentBubble = addBubble("user", text);
  scrollToBottom(true);
  // Enter streaming state NOW (optimistically) so the guard above blocks a rapid
  // second send. Otherwise, in the window before the stream_start event arrives,
  // state.streaming is still false and a double-send reaches the backend while
  // the first turn is still executing — which fails with "agent is in state
  // executing, expected idle" ("SendMessage failed"). stream_start is
  // idempotent; stream_end / the catch below clear it.
  setStreaming(true);
  try {
    // NOTE: sendMessage uses key `convId` (getMessages uses `convID`).
    const res = await rpc("client.sendMessage", { convId: state.activeConvID, message: text });
    // On a fresh peer we sent convId:"" and the backend created one — adopt it
    // immediately so the multiplexed-stream convID filter engages for this turn's
    // events (and subsequent sends target the right conversation).
    if (res && (res.convId || res.convID) && !state.activeConvID) {
      state.activeConvID = res.convId || res.convID;
    }
    // Response streams back via SSE.
  } catch (e) {
    // Roll back the optimistic bubble AND restore the typed text into the
    // composer — a transient failure must never eat the user's message (it
    // used to vanish: bubble stayed, input was already cleared, send failed).
    sentBubble.remove();
    input.value = text;
    autoGrow();
    if (!e.peerOffline) toast("send failed: " + e.message, true);
    setStreaming(false);
  }
}

function autoGrow() {
  const t = $("input");
  t.style.height = "auto";
  t.style.height = Math.min(t.scrollHeight, 140) + "px";
}

/* ------------------------------------------------------------------ *
 * Mode / model controls
 * ------------------------------------------------------------------ */
const MODES = ["plan", "act", "auto"];
async function cycleMode() {
  const cur = MODES.indexOf((state.mode || "").toLowerCase());
  const next = MODES[(cur + 1) % MODES.length];
  try {
    await rpc("client.setMode", { mode: next });
    state.mode = next;
    renderHeader();
  } catch (e) {
    if (!e.peerOffline) toast("setMode failed: " + e.message, true);
  }
}
// New chat: create a fresh conversation on the attached peer and switch to it.
// The mobile app is otherwise pinned to the peer's ACTIVE conversation, so
// without this a phone user could never start over. createConversation returns
// {id}; switchConversation makes it the active one (so getMessages "" and the
// composer target it). We reload directly AND the backend echoes conv_switched
// over SSE (which also calls loadMessages) — the double load is harmless.
async function newChat() {
  if (state.streaming) {
    toast("Finish or stop the current turn first");
    return;
  }
  try {
    const res = await rpc("client.createConversation", {});
    const id = res && res.id;
    if (id) {
      await rpc("client.switchConversation", { id });
      state.activeConvID = id;
    }
    chat.innerHTML = "";
    state.curBubble = null;
    state.toolChips = {};
    state.pctUsed = null; // fresh conversation: context meter resets
    await loadMessages();
    $("input").focus();
    toast("New chat started");
  } catch (e) {
    if (!e.peerOffline) toast("new chat failed: " + e.message, true);
  }
}
// Conversations sheet: list the peer's conversations and switch/resume any of
// them. Complements newChat() — together they give the phone a full
// create/list/resume story instead of being pinned to the active conversation.
function openConvos() {
  $("convos-sheet").classList.remove("hidden");
  fetchConvos();
}
function closeConvos() {
  $("convos-sheet").classList.add("hidden");
}
// Only ever fetch/render the most-recent CONVOS_LIMIT conversations. A busy peer
// can accumulate THOUSANDS (a live Swarm Desktop backend had 9726) — pulling and
// rendering all of them would transfer megabytes and freeze the phone with tens
// of thousands of DOM rows. The server honors {limit} and returns newest-first.
const CONVOS_LIMIT = 50;
async function fetchConvos() {
  const list = $("convos-list");
  list.textContent = "Loading…";
  let data;
  try {
    data = await rpc("client.listConversations", { limit: CONVOS_LIMIT });
  } catch (e) {
    if (e.peerOffline) return; // peerGone already bounced us to the picker
    list.textContent = "Error: " + e.message;
    return;
  }
  const convos = (data && data.conversations) || [];
  list.innerHTML = "";
  if (!convos.length) {
    list.appendChild(el("div", "empty", "No conversations yet."));
    return;
  }
  for (const c of convos) {
    const row = el("div", "convo-row");
    if (c.id && c.id === state.activeConvID) row.classList.add("active");
    // The body (title + meta) switches to the conversation on tap; the trash
    // button deletes (with a two-tap confirm). title/preview are user-authored
    // text — el() uses textContent (XSS-safe).
    const body = el("div", "convo-body");
    body.appendChild(el("div", "convo-title", c.title || c.preview || "(empty chat)"));
    const bits = [];
    if (c.messageCount != null) bits.push(c.messageCount + " msg");
    if (c.workspacePath) bits.push(baseName(c.workspacePath));
    if (c.updatedAt) bits.push(relTime(c.updatedAt));
    body.appendChild(el("div", "convo-meta", bits.join(" · ")));
    body.addEventListener("click", () => switchTo(c.id));
    row.appendChild(body);

    const del = el("button", "convo-del", "🗑");
    del.setAttribute("aria-label", "Delete conversation");
    del.addEventListener("click", (e) => {
      e.stopPropagation();
      if (del.dataset.confirm === "1") {
        deleteConvo(c.id);
        return;
      }
      // Arm this button (two-tap confirm — no blocking prompt); disarm others.
      list.querySelectorAll(".convo-del.confirm").forEach((b) => {
        b.classList.remove("confirm");
        b.textContent = "🗑";
        b.dataset.confirm = "";
      });
      del.dataset.confirm = "1";
      del.textContent = "Delete?";
      del.classList.add("confirm");
      setTimeout(() => {
        if (del.dataset.confirm === "1") {
          del.dataset.confirm = "";
          del.textContent = "🗑";
          del.classList.remove("confirm");
        }
      }, 3000);
    });
    row.appendChild(del);
    list.appendChild(row);
  }
  // If we got a full page, there are (likely many) older ones not shown.
  if (convos.length >= CONVOS_LIMIT) {
    list.appendChild(el("div", "convos-more", CONVOS_LIMIT + " most recent shown"));
  }
}
// Delete a conversation (from the convos sheet, after a two-tap confirm). If it
// was the active one, reset the session to a clean empty state so we don't keep
// pointing at a deleted conversation. Then refresh the list.
async function deleteConvo(id) {
  if (!id) return;
  try {
    await rpc("client.deleteConversation", { id });
  } catch (e) {
    if (!e.peerOffline) toast("delete failed: " + e.message, true);
    return;
  }
  if (id === state.activeConvID) {
    // We deleted the ACTIVE conversation. The daemon keeps a STALE pointer to it
    // (snapshot still reports the deleted id as active, and a send to it is
    // silently dropped). Create + switch to a FRESH conversation so the peer's
    // active conversation is valid again — otherwise a reconnect would re-adopt
    // the dead id from snapshot and the next message would be lost.
    let fresh = "";
    try {
      const res = await rpc("client.createConversation", {});
      fresh = (res && res.id) || "";
      if (fresh) await rpc("client.switchConversation", { id: fresh });
    } catch (_) {
      /* best effort — fall back to empty active id */
    }
    state.activeConvID = fresh;
    chat.innerHTML = "";
    state.curBubble = null;
    state.toolChips = {};
    state.thinkChip = null;
    state.pctUsed = null;
    chat.appendChild(el("div", "sys-notice", "No messages yet — say hello."));
  }
  toast("Conversation deleted");
  fetchConvos();
}
async function switchTo(id) {
  if (!id) return;
  if (state.streaming) {
    toast("Finish or stop the current turn first");
    return;
  }
  try {
    await rpc("client.switchConversation", { id });
    state.activeConvID = id;
    chat.innerHTML = "";
    state.curBubble = null;
    state.toolChips = {};
    state.pctUsed = null; // fresh conversation: context meter resets
    closeConvos();
    await loadMessages();
  } catch (e) {
    if (!e.peerOffline) toast("switch failed: " + e.message, true);
  }
}
// Last path segment of a workspace path, for a compact conversation subtitle.
function baseName(p) {
  if (!p) return "";
  const parts = String(p).replace(/\/+$/, "").split("/");
  return parts[parts.length - 1] || p;
}
// Compact relative time ("5m ago") for conversation timestamps.
function relTime(iso) {
  const t = new Date(iso).getTime();
  if (isNaN(t)) return "";
  const s = Math.floor((Date.now() - t) / 1000);
  if (s < 60) return "just now";
  if (s < 3600) return Math.floor(s / 60) + "m ago";
  if (s < 86400) return Math.floor(s / 3600) + "h ago";
  return Math.floor(s / 86400) + "d ago";
}
// In-page model picker. A native prompt() blocks the webview (janky on phones,
// and it deadlocks headless automation), so use the same bottom-sheet pattern
// as approvals: prefill the current model, Set applies via client.setModel.
function changeModel() {
  const input = $("model-input");
  input.value = state.model || "";
  $("model-sheet").classList.remove("hidden");
  setTimeout(() => { input.focus(); input.select(); }, 0);
}
async function applyModel() {
  const model = $("model-input").value.trim();
  $("model-sheet").classList.add("hidden");
  if (!model || model === state.model) return;
  try {
    await rpc("client.setModel", { model });
    state.model = model;
    renderHeader();
  } catch (e) {
    if (!e.peerOffline) toast("setModel failed: " + e.message, true);
  }
}

/* ------------------------------------------------------------------ *
 * Daemon logs panel
 * ------------------------------------------------------------------ */
async function fetchLogs() {
  const body = $("logs-body");
  body.textContent = "Loading…";
  try {
    const data = await rpc("daemon.getLogs", { n: 500 });
    const lines = (data && data.lines) || [];
    body.textContent = lines.length
      ? lines.join("\n")
      : "(no log lines captured yet)";
    // Scroll to bottom so latest lines are visible.
    body.scrollTop = body.scrollHeight;
  } catch (e) {
    body.textContent = "Error: " + e.message;
  }
}

function openLogs() {
  $("logs-sheet").classList.remove("hidden");
  fetchLogs();
}

function closeLogs() {
  $("logs-sheet").classList.add("hidden");
}

/* ------------------------------------------------------------------ *
 * Wiring
 * ------------------------------------------------------------------ */
function backToPeers() {
  closeStream();
  setStreaming(false);
  state.curBubble = null;
  show("peer");
  loadPeers();
}

function init() {
  $("refresh-peers").addEventListener("click", loadPeers);
  $("back-btn").addEventListener("click", backToPeers);
  $("refresh-msgs").addEventListener("click", loadMessages);
  $("convos-btn").addEventListener("click", openConvos);
  $("convos-close").addEventListener("click", closeConvos);
  $("convos-new").addEventListener("click", () => { closeConvos(); newChat(); });
  $("logs-btn").addEventListener("click", openLogs);
  $("logs-close").addEventListener("click", closeLogs);
  $("logs-refresh").addEventListener("click", fetchLogs);
  $("s-mode").addEventListener("click", cycleMode);
  $("s-model").addEventListener("click", changeModel);
  $("approve-allow").addEventListener("click", () => respondApproval(true));
  $("approve-deny").addEventListener("click", () => respondApproval(false));
  $("model-set").addEventListener("click", applyModel);
  $("model-cancel").addEventListener("click", () => $("model-sheet").classList.add("hidden"));
  $("model-input").addEventListener("keydown", (e) => {
    if (e.key === "Enter") { e.preventDefault(); applyModel(); }
    else if (e.key === "Escape") { $("model-sheet").classList.add("hidden"); }
  });

  $("composer").addEventListener("submit", (e) => {
    e.preventDefault();
    if (state.streaming) cancelTurn();
    else sendMessage();
  });
  const input = $("input");
  input.addEventListener("input", autoGrow);
  input.addEventListener("keydown", (e) => {
    if (e.key === "Enter" && !e.shiftKey) { e.preventDefault(); sendMessage(); }
  });
  // When the on-screen keyboard opens the visual viewport shrinks and the
  // newest messages end up hidden BEHIND the keyboard — keep the chat pinned
  // to the bottom while the composer is focused.
  if (window.visualViewport) {
    window.visualViewport.addEventListener("resize", () => {
      if (!sessionView.classList.contains("hidden") && document.activeElement === input) {
        scrollToBottom(true);
      }
    });
  }

  show("peer");
  loadPeers();
  registerSW();
  startVersionWatch();
}
/* ------------------------------------------------------------------ *
 * Update push — poll /api/version; when the gateway is redeployed its
 * embedded-asset hash changes, so we drop the SW cache and reload to the
 * new app shell. This is how new builds reach already-open clients.
 * ------------------------------------------------------------------ */
function startVersionWatch() {
  let known = null;
  const check = async () => {
    try {
      const res = await fetch("api/version", { cache: "no-store" });
      if (!res.ok) return;
      const { version } = await res.json();
      if (!version) return;
      if (known === null) {
        known = version;
        return;
      }
      if (version !== known) {
        known = version;
        // Purge caches so the reload fetches fresh assets, then reload.
        try {
          if (self.caches) {
            const keys = await caches.keys();
            await Promise.all(keys.map((k) => caches.delete(k)));
          }
        } catch (_) {}
        location.reload();
      }
    } catch (_) {
      // Offline / gateway restarting — try again next tick.
    }
  };
  check();
  setInterval(check, 30000);
  // Keep the peer picker fresh without a manual ⟳ (pollPeers self-guards to the
  // picker view + only re-renders on change, so it's cheap and flicker-free).
  setInterval(pollPeers, 5000);
}

/* ------------------------------------------------------------------ *
 * Service worker (progressive enhancement — must not break the app)
 * ------------------------------------------------------------------ */
async function registerSW() {
  if (!("serviceWorker" in navigator)) return;
  try {
    await navigator.serviceWorker.register("sw.js");
  } catch (e) {
    // Expected on plain-http LAN origins; app works fully without SW.
    console.info("SW not registered (ok on http LAN):", e && e.message);
  }
}

init();
