# Swarm Mobile — control swarm from your Pixel

Turn any running swarm process (daemon, TUI, or headless peer) into something you
can drive from your phone over your home WiFi — see the live conversation, stream
responses, send messages, switch mode/model, and approve tools.

```
Phone (Chrome PWA) ──HTTP/LAN──▶ swarm-gateway (0.0.0.0:8787) ──loopback──▶ peer serve.Mux
```

The gateway binds your LAN, discovers peers, reverse-proxies to the one you pick,
and serves the installable PWA from the same origin (no CORS setup needed).

## 1. Start a swarm process (if none is running)

Any of these publishes a serve endpoint the gateway can reach:

```bash
swarmos daemon                 # persistent headless engine (recommended)
# or just an interactive TUI:
swarmos
# or a headless peer:
swarmos -p ... / swarm-headless peer -handle myphone ...
```

## 2. Start the gateway (the one command)

```bash
cd swarm-sdk
go run ./cmd/swarm-gateway            # defaults: :8787, auto-select a peer
# or build once:
go build -o swarm-gateway ./cmd/swarm-gateway && ./swarm-gateway
```

Optional flags:

```
-addr   :8787                 listen address (binds 0.0.0.0 for LAN)
-swarm  default               swarm name to discover peers in
-peer   ""                    pin a specific peer handle (default: auto-select, prefers daemons)
-token  ""                    require a shared secret on data routes (recommended on shared WiFi)
```

On start it prints every LAN URL and a scannable QR code, e.g.:

```
  listening on :8787
    http://192.168.1.50:8787
  scan this on your Pixel …
  █▀▀▀▀▀█ …  (QR)
```

## 3. Install on the Pixel

1. Phone + this computer must be on the **same WiFi**.
2. Open **Chrome** on the Pixel and go to the printed URL (or scan the QR).
3. Pick your peer from the list — you're now controlling it live.
4. To install as an app: Chrome **⋮ menu → Install app** (or "Add to Home screen").
   It launches standalone, full-screen, with its own icon.

### Note on service worker / offline (Chrome + plain-http LAN)
Chrome only registers a service worker over **https or localhost**, so on a plain
`http://192.168.x.x` LAN the offline cache may not activate. **The app is fully
functional without it** — the service worker is only progressive enhancement.
If you want full PWA/offline install, either:
- open `chrome://flags/#unsafely-treat-insecure-origin-as-secure`, add your
  gateway URL, and relaunch Chrome; or
- put the gateway behind an https reverse proxy / tailscale hostname.

## 4. Security

The gateway exposes control of your agent to anyone on the LAN. On untrusted
networks pass `-token YOURSECRET`; the printed URL then includes `?token=…` and
the PWA carries it through on every request. Data routes (`/api`, `/rpc`, `/sse`,
`/ws`) reject requests without it.

## What the app can do
- List peers, pick which one to drive (`?peer=` per request or default select).
- Live session header: provider · model · mode · tokens.
- Full chat scrollback + live streaming responses (SSE).
- Send messages; cycle mode (plan/act/auto); change model.
- Approve/deny tool calls from your phone.
