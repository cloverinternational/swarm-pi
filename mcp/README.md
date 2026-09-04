# Pi Swarm MCP adapter

A closed-by-default MCP bridge for declared servers. It supports stdio JSON-RPC, HTTP/SSE-style JSON responses, lazy discovery, stable `mcp__<server>__<tool>` names, exact tool allowlists, environment-name credential references, OAuth bearer tokens, lifecycle close, and typed failure categories.

Manifests are supplied by the host as `MCPManifest[]` (the adapter does not read ambient config). In closed mode stdio commands must be pinned paths. Header and OAuth values are read only from explicitly allowlisted environment variables; values are never included in manifest listings. SSE responses are accepted when the response body contains `data:` JSON events. Full streaming SSE is intentionally deferred.

```ts
const manager = new MCPManager(manifests, { closed: true, registerTool: pi.registerTool });
await manager.discover("server-id");
await manager.call("server-id", "tool", args, signal);
await manager.close();
```
