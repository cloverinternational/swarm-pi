# Swarm Mobile — LAN control from your Pixel

## Goal
Install an app on the Pixel that controls the swarm daemon (and any TUI/headless peer)
over the local network: see the live conversation, stream responses, send messages,
switch mode/model, approve tools, pick which peer to drive.

## Architecture (chosen)
Phone (Chrome PWA)  ──HTTP/LAN──▶  swarm-gateway (0.0.0.0:8787)  ──loopback──▶  peer serve.Mux (/rpc,/sse,/ws)

- **Why PWA, not native APK:** no Android SDK present; PWA installs from Chrome
  ("Install app"), runs standalone, works fully over LAN. Zero heavy toolchain.
- **Why a gateway:** daemon/TUI/headless serve endpoints bind 127.0.0.1 (loopback),
  so the phone cannot reach them directly. The gateway binds 0.0.0.0 on the LAN and
  reverse-proxies to a chosen peer's ServeURL. Serving the PWA from the SAME origin
  means no CORS config on the serve.Mux.

## Components
1. **swarm-gateway** (Go, `cmd/swarm-gateway`)
   - Bind `0.0.0.0:PORT` (default 8787).
   - `GET /api/peers` → list live peers (a2a.ListPeers) with ServeURL/EndpointURL, type, status, model, workspace.
   - `POST|GET /rpc`, `/sse`, `/ws` → reverse-proxy to the selected peer (query/header `?peer=<handle>`, or default = first daemon/serve-capable peer).
   - `GET /` and static → embedded PWA assets (go:embed).
   - Print LAN URLs (all non-loopback IPv4) + an ASCII QR code so the phone can scan.
   - Optional shared-secret token (`--token`) checked on /api + proxied routes for basic LAN safety.
2. **PWA frontend** (`cmd/swarm-gateway/web`)
   - Mobile-first single page: peer picker, session header (provider/model/mode/tokens),
     chat scrollback (getMessages), live streaming (SSE), composer (sendMessage),
     mode cycle, model pick, tool-approval prompts (pendingApprovals + respondApproval).
   - `manifest.webmanifest` + service worker (offline shell) + maskable icons → installable.
   - No build step: vanilla ES modules + CSS. Embedded and served by the gateway.

## Milestones
- M1 Gateway skeleton: bind, peers API, static embed, LAN IP + QR print. [sub-agent A]
- M2 Reverse proxy /rpc /sse /ws to selected peer (SSE must stream, no buffering). [sub-agent A]
- M3 PWA UI: peers → chat → send → stream → mode/model → approvals. [sub-agent B]
- M4 PWA installability: manifest + SW + icons; verify Lighthouse-style criteria.
- M5 Integrate, `go build ./...`, run gateway + a headless peer, curl every route.
- M6 End-to-end: drive a real turn through the gateway; verify SSE frames arrive.
- M7 Pixel install: one-command launcher (`swarmos mobile` or run gateway), print URL+QR, write install steps.

## Test plan
- Unit-ish: curl /api/peers, /rpc client.snapshot, /sse (read first events), static /.
- Runtime: start headless peer (loopback serve) + gateway (0.0.0.0); from another
  process hit the gateway's public IP and confirm proxied snapshot + streamed send.
- Install: document Chrome → Install app on the Pixel; verify standalone launch.
