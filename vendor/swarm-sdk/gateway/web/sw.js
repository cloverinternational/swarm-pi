// Swarm Mobile service worker.
// App-shell cache-first; API/RPC/SSE are ALWAYS network-only (never cached).
const CACHE = "swarm-mobile-v1";
const SHELL = [
  "./",
  "index.html",
  "app.js",
  "styles.css",
  "manifest.webmanifest",
  "icons/icon-192.png",
  "icons/icon-512.png",
];

self.addEventListener("install", (event) => {
  event.waitUntil(
    caches.open(CACHE).then((c) => c.addAll(SHELL)).then(() => self.skipWaiting())
  );
});

self.addEventListener("activate", (event) => {
  event.waitUntil(
    caches.keys()
      .then((keys) => Promise.all(keys.filter((k) => k !== CACHE).map((k) => caches.delete(k))))
      .then(() => self.clients.claim())
  );
});

// True for dynamic routes that must never be cached.
function isDynamic(url) {
  const p = url.pathname;
  return (
    p === "/rpc" || p.endsWith("/rpc") ||
    p === "/sse" || p.endsWith("/sse") ||
    p === "/ws" || p.endsWith("/ws") ||
    p.includes("/api/") || p.endsWith("/api/peers") || p.endsWith("/api/select")
  );
}

self.addEventListener("fetch", (event) => {
  const req = event.request;
  const url = new URL(req.url);

  // Only handle same-origin GETs; everything else goes straight to network.
  if (req.method !== "GET" || url.origin !== self.location.origin) return;

  // Never cache the live control channels.
  if (isDynamic(url)) {
    event.respondWith(fetch(req));
    return;
  }

  // Navigation + app shell: NETWORK-first, fall back to cache, then
  // index.html. Cache-first pinned users to a stale shell forever after a
  // daemon upgrade (the embedded assets change but the cache name doesn't);
  // on a LAN the network fetch is instant, and offline still works via the
  // cache fallback.
  event.respondWith(
    fetch(req)
      .then((res) => {
        if (res && res.ok) {
          const copy = res.clone();
          caches.open(CACHE).then((c) => c.put(req, copy)).catch(() => {});
        }
        return res;
      })
      .catch(() =>
        caches.match(req).then((hit) => {
          if (hit) return hit;
          if (req.mode === "navigate") return caches.match("index.html");
          return new Response("offline", { status: 503, statusText: "offline" });
        })
      )
  );
});
