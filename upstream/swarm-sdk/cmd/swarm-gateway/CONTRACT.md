# Swarm Mobile — Interface Contract (source of truth)

The gateway serves the PWA and proxies to a peer's serve.Mux. Both sides code to THIS.

## Gateway HTTP surface (same origin as PWA)
- `GET  /api/peers`  → `{"peers":[{handle,name,type,status,model,workspace,serveURL,endpointURL,pid}], "selected":"<handle>"}`
- `POST /api/select` body `{"handle":"..."}` → `{"selected":"..."}` (sets default proxy target; also honored per-request via `?peer=`)
- `POST /rpc?peer=<handle>`  → transparently proxied to the peer's serve `/rpc` (JSON-RPC 2.0). If `?peer` omitted, uses selected.
- `GET  /sse?peer=<handle>`  → proxied SSE stream (MUST be unbuffered; flush per line). 
- `GET  /ws?peer=<handle>`   → proxied WebSocket (bonus; PWA uses SSE + POST as primary).
- `GET  /` and other paths   → embedded PWA static assets (go:embed). `/manifest.webmanifest`, `/sw.js`, `/icons/*`.
- Optional `--token T`: if set, all /api,/rpc,/sse,/ws require `?token=T` or `Authorization: Bearer T`.

## JSON-RPC over /rpc (POST). Request:
`{"jsonrpc":"2.0","id":1,"method":"<m>","params":{...}}` → `{"jsonrpc":"2.0","id":1,"result":...}` or `{"error":{"code","message"}}`

### Methods the PWA uses
- `client.snapshot` params `{}` → `{ActiveConvID,Conversations,Provider,Model,OperatingMode,IsStreaming,InputTokens,OutputTokens,TotalTokens,CurrentContext,...}`
- `client.getMessages` params `{"convID":""}` (empty=active) → `{convID, messages:[{Role,Content,Timestamp,...}]}`
- `client.sendMessage` params `{"convId":"","message":"..."}` → starts a turn server-side; results arrive via SSE. (note: sendMessage uses key `convId`; getMessages uses `convID`.)
- `client.listConversations` params `{}` → conversation list
- `client.switchConversation` params `{"id":"..."}`
- `client.setMode` params `{"mode":"plan|act|auto"}`
- `client.setModel` params `{"model":"..."}` ; `client.setProvider` `{"provider":"...","model":"..."}`
- `client.pendingApprovals` params `{}` → list of pending tool approvals
- `client.respondApproval` params `{"callID":"...","allow":true}` (also "always" variant if present)
- `client.tokenUsage` `{}` → `{input,output,total}`

## SSE wire format (text/event-stream), one event per block:
```
event: <kind>
data: <json EventEnvelope>

```
EventEnvelope = `{kind, at?, agent?:{type,update}, payload?, source?:{kind,agentID,peerHandle,convID}}`

### Event kinds the PWA must handle
- `stream_start` / `stream_end` — turn boundaries (toggle streaming spinner).
- `agent` — streaming agent update; `agent.type` discriminates. Content deltas live here (append to the in-progress assistant bubble). Tool calls also arrive as `agent` updates.
- `mode_changed`, `model_changed` — update header.
- `conv_switched`, `conv_created`, `conv_updated` — refresh list / reload messages.
- `approval_requested` — payload is an approval request → show approve/deny UI, then call `client.respondApproval`.
- `compaction` — show a subtle notice.
- `error` — payload has message → toast.
- Peer/task events (`peer_joined`, etc.) — optional.

The browser EventSource API cannot set headers; when `--token` is used the PWA passes `?token=` on the /sse URL. Reconnect on drop.

## PWA installability requirements (Pixel/Chrome)
- `manifest.webmanifest`: name, short_name, start_url ".", display "standalone", background/theme color, icons 192+512 (maskable).
- `sw.js`: cache the app shell (index.html, css, js, manifest, icons) for offline load. Network-first for /api,/rpc,/sse (never cache those).
- Served over http on LAN — Chrome allows install for http on private IPs? NOTE: Chrome requires https OR localhost for full PWA install/service-worker. LAN http may not register SW. Mitigation documented in M7 (chrome://flags unsafely-treat-insecure-origin, or add-to-homescreen still works as a shortcut). App must be fully FUNCTIONAL without SW (SW is progressive enhancement).
