# MCP Contract

This document defines the MCP server configuration schema, credential mapping, merge rules, IPC methods, validation, and error codes for SwarmOS and IDE integration. It is the single source of truth for later phases.

## File locations

- Global MCP config: `~/.swarmos/mcp_servers.json`
- Project MCP config: `<workspace>/.swarmos/mcp_servers.json`
- MCP credentials: `~/.swarmos/credentials.json` (core.Credentials.MCP)
- Plugin MCP config: `<plugin>/.mcp.json` (read-only, parsed by sdk/plugins)

## Canonical schema

### Top-level (mcp_servers.json)

```
{
  "schema_version": 1,
  "servers": [<MCPServerConfig>],
  "plugin_overrides": {
    "<plugin_name>": {
      "<server_name>": <MCPServerOverride>
    }
  }
}
```

- `schema_version` (required, int): schema version. Current: 1.
- `servers` (required, array): list of user-defined servers for this scope.
- `plugin_overrides` (optional, object): per-plugin overrides (see Plugin overrides).

### MCPServerConfig

```
{
  "name": "github",
  "type": "http",
  "enabled": true,
  "credential_ref": "github",
  "command": "npx",
  "args": ["-y", "@modelcontextprotocol/server-github"],
  "env": {"LOG_LEVEL": "info"},
  "secret_env": ["GITHUB_TOKEN"],
  "working_dir": "/path/to/dir",
  "url": "https://mcp.example.com",
  "headers": {"User-Agent": "SwarmOS"},
  "secret_headers": ["Authorization"],
  "auth": {
    "mode": "bearer",
    "header": "Authorization",
    "prefix": "Bearer "
  },
  "timeout_sec": 30,
  "retries": 3,
  "oauth": {
    "client_id": "swarmos-desktop",
    "client_secret_ref": "oauth.client_secret",
    "scopes": ["mcp.read", "mcp.write"],
    "auth_url": "https://auth.example.com/authorize",
    "token_url": "https://auth.example.com/token"
  },
  "tools": {
    "mode": "all",
    "enabled": ["tool_a"],
    "disabled": ["tool_b"]
  }
}
```

Fields:

- `name` (required, string): unique per file. Pattern: `[A-Za-z0-9._-]{1,64}`.
- `type` (optional, string): `stdio`, `http`, `sse`, `oauth`. Default:
  - `stdio` if `command` is set
  - `http` if `url` is set
  - `http` maps to streamable HTTP transport, `oauth` maps to OAuth HTTP transport.
- `enabled` (optional, bool): default false for user entries; plugin defaults may vary.
- `credential_ref` (optional, string): key in `credentials.json`. Default: `name`.
- `command`, `args`, `env`, `working_dir`: stdio-only. `env` is non-secret.
- `url`, `headers`: http/sse/oauth only. `headers` is non-secret.
- `secret_env` (optional, array): env keys sourced from credentials (see secrets).
- `secret_headers` (optional, array): header keys sourced from credentials.
- `auth` (optional, object): how to apply `credentials.mcp[credential_ref].token`.
  - `mode`: `bearer`, `header`, `env`, `query`, `none` (default `bearer` for http/sse, `none` for stdio).
  - `header`: header name (default `Authorization` for `bearer`/`header`).
  - `prefix`: header prefix (default `Bearer ` for `bearer`).
  - `env`: env var name when `mode` is `env`.
  - `query`: query key when `mode` is `query`.
- `timeout_sec` (optional, int): connection timeout in seconds. Default 30.
- `retries` (optional, int): transport retries. Default 3.
- `oauth` (optional, object): required when `type` is `oauth`.
  - `client_id` (required)
  - `client_secret_ref` (optional, string): key in credentials headers map. Default `oauth.client_secret`.
  - `scopes` (optional, array of string)
  - `auth_url`, `token_url` (optional): manual override for discovery.
- OAuth flow uses `sdk/tools/mcp/oauth` with a credentials-backed token storage adapter. Do not re-implement PKCE or token persistence.
- `tools` (optional, object): per-tool enable/disable.
  - `mode`: `all`, `allowlist`, `blocklist` (default `all`).
  - `enabled`: list used when `mode=allowlist`.
  - `disabled`: list used when `mode=blocklist`.
  - Tool names use the MCP server-reported tool name (pre-sanitization).

### MCPServerOverride

Partial server config applied on top of a plugin server config. Only fields present are overridden. Validation rules for `MCPServerConfig` apply to the merged result.

## Secrets and credentials

All secrets live in `~/.swarmos/credentials.json` and are never returned via IPC.

### credentials.json layout

```
{
  "providers": {"...": {}},
  "mcp": {
    "github": {
      "token": "REDACTED",
      "headers": {
        "Authorization": "Bearer REDACTED",
        "GITHUB_TOKEN": "REDACTED",
        "oauth.client_secret": "REDACTED",
        "oauth.refresh_token": "REDACTED",
        "oauth.expires_at": "2025-01-01T00:00:00Z",
        "oauth.token_type": "Bearer",
        "oauth.scope": "mcp.read mcp.write"
      }
    }
  }
}
```

Rules:

- Credentials are keyed by `credential_ref` (default `name`).
- `token` is the primary secret used by `auth`.
- `headers` is a secret key/value store. Values can be injected as:
  - HTTP headers listed in `secret_headers`
  - stdio env vars listed in `secret_env`
- Merge order for headers/env:
  - config values
  - secrets from `secret_headers`/`secret_env`
  - `auth`-derived header/env (unless already set)
- Reserved keys in `headers` with prefix `oauth.` are used by the OAuth token storage adapter:
  - `oauth.client_secret` (client secret)
  - `oauth.refresh_token`
  - `oauth.expires_at` (RFC3339)
  - `oauth.token_type`
  - `oauth.scope`
- OAuth access tokens are stored in `token` and never emitted via IPC.
- Secret values must never appear in `mcp_servers.json`, IPC payloads, logs, or UI state.

## Example mcp_servers.json

```
{
  "schema_version": 1,
  "servers": [
    {
      "name": "filesystem",
      "type": "stdio",
      "command": "npx",
      "args": ["-y", "@modelcontextprotocol/server-filesystem", "/tmp"],
      "env": {"LOG_LEVEL": "info"},
      "secret_env": ["FILESYSTEM_API_KEY"],
      "enabled": false,
      "timeout_sec": 30,
      "retries": 3,
      "tools": {"mode": "all"}
    },
    {
      "name": "github",
      "type": "http",
      "url": "https://mcp.example.com",
      "headers": {"User-Agent": "SwarmOS"},
      "secret_headers": ["Authorization"],
      "auth": {"mode": "bearer", "header": "Authorization", "prefix": "Bearer "},
      "enabled": true,
      "tools": {"mode": "blocklist", "disabled": ["delete_repo"]}
    },
    {
      "name": "cloud-catalog",
      "type": "oauth",
      "url": "https://api.example.com/mcp",
      "oauth": {
        "client_id": "swarmos-desktop",
        "client_secret_ref": "oauth.client_secret",
        "scopes": ["mcp.read", "mcp.write"]
      },
      "enabled": true
    }
  ],
  "plugin_overrides": {
    "example-plugin": {
      "analytics": {"enabled": false}
    }
  }
}
```

## Merge and override rules

### Sources and precedence

Resolved config is the merge of multiple sources. Precedence (highest wins):

1. Project config (`<workspace>/.swarmos/mcp_servers.json`)
2. Global config (`~/.swarmos/mcp_servers.json`)
3. Plugin overrides (from the same config file, scoped by plugin)
4. Plugin base (`.mcp.json`)

Notes:

- Names must be unique within each source file.
- If the same `name` appears in multiple sources, the highest precedence entry is active and lower entries are marked `shadowed` in IPC responses.
- Conflicts within a single file (duplicate names) are rejected at validation time.

### Origin badges

IPC responses expose:

- `origin`: `project`, `global`, `plugin`, `marketplace`, `import`, `builtin`
- `origin_detail`: string such as `plugin:my-plugin`, `marketplace:acme`, `import:claude`
- `shadowed_by`: array of `{name, origin}` for active overrides

### Plugin overrides and undo

- Plugin MCP servers are read-only from their plugin `.mcp.json` files.
- User edits are stored in `plugin_overrides` keyed by plugin name and server name.
- The effective config is: `plugin_base` merged with `plugin_override` (override wins).
- Undo removes the override entry, restoring plugin defaults.

### Plugin editability flags

IPC responses include:

- `editability`: `readonly`, `editable_with_warning`, or `editable`
- `edit_warning`: message displayed for `editable_with_warning`

Rules:

- Closed source marketplace plugins are `readonly`.
- Local or git plugins with an OSI license are `editable_with_warning` (warn that updates may overwrite behavior).
- Free marketplace plugins with source available are `editable_with_warning`.

## IPC contract (JSON-RPC)

All IPC payloads exclude secrets. Secrets never appear in responses. Credential writes happen out of band (direct file write by the IDE or the OAuth flow inside headless).

### listMCPServers

Params:

```
{ "scope": "all|global|project|plugin" }
```

Result:

```
{
  "servers": [<MCPServerRecord>]
}
```

`MCPServerRecord`:

```
{
  "name": "github",
  "scope": "global",
  "origin": "global",
  "origin_detail": "global",
  "config": {<MCPServerConfig without secrets>},
  "status": {
    "state": "disconnected|connecting|connected|error",
    "tool_count": 12,
    "tools_enabled": 11,
    "tools_disabled": 1,
    "last_error": "",
    "last_error_at": 0
  },
  "shadowed_by": [{"name": "github", "origin": "project"}],
  "editability": "editable_with_warning",
  "edit_warning": "Editing plugin servers is not preserved across updates.",
  "has_credentials": true
}
```

### addMCPServer

Params:

```
{ "scope": "global|project", "server": <MCPServerConfig> }
```

Result:

```
{ "status": "added", "name": "github" }
```

### updateMCPServer

Params:

```
{
  "scope": "global|project|plugin",
  "name": "github",
  "patch": {<partial MCPServerConfig>},
  "replace": false
}
```

Result:

```
{ "status": "updated", "name": "github" }
```

Notes:

- `scope=plugin` writes to `plugin_overrides` only.
- `replace=true` replaces the stored config in that scope.

### removeMCPServer

Params:

```
{ "scope": "global|project|plugin", "name": "github" }
```

Result:

```
{ "status": "removed", "name": "github" }
```

### toggleMCPServer

Params:

```
{ "scope": "global|project|plugin", "name": "github", "enabled": true }
```

Result:

```
{ "status": "toggled", "name": "github", "enabled": true }
```

### setMCPToolEnabled

Params:

```
{ "server": "github", "tool": "repo_list", "enabled": true }
```

Result:

```
{ "status": "toggled", "server": "github", "tool": "repo_list", "enabled": true }
```

### listMCPPermissionPolicies

Params:

```
{ "scope": "global|project", "scope_id": "" }
```

Result:

```
{ "policies": {"file_write": "interactive", "network_access": "allow"} }
```

Permission values follow sdk/tools.Permission: `file_read`, `file_write`, `file_delete`, `bash_execute`, `network_access`, `database_read`, `database_write`, `container_access`, `secret_access`, `hook_manage`.

### updateMCPPermissionPolicy

Params:

```
{
  "permission": "file_write",
  "policy": "allow|deny|interactive|sandbox",
  "scope": "global|project",
  "scope_id": "",
  "constraints": {"paths": ["/tmp"]}
}
```

Result:

```
{ "status": "updated", "permission": "file_write", "policy": "interactive" }
```

### refreshMCPServers

Params:

```
{ "name": "github" }
```

Result:

```
{ "status": "refreshing", "name": "github" }
```

If `name` is omitted, all enabled servers reconnect.

### previewMCPImport

Params:

```
{
  "source_type": "claude|codex|json|toml",
  "scope": "global|project",
  "payload": {"path": "/path/to/file", "text": "..."}
}
```

Result:

```
{
  "import_id": "uuid",
  "detected_format": "claude|codex|json|toml",
  "servers": [<MCPServerConfig>],
  "conflicts": [{"name": "github", "existing_origin": "global"}],
  "warnings": ["unknown key: foo"],
  "required_credentials": ["github"]
}
```

Importer notes:

- Claude: `.mcp.json` map of `name -> {type, command, args, env, url}`.
- Codex: `config.toml` with `[mcp_servers.<name>]` tables using similar fields.
- JSON/TOML: accepts either canonical schema or `.mcp.json`-style maps.

### applyMCPImport

Params:

```
{
  "import_id": "uuid",
  "scope": "global|project",
  "conflict_policy": "skip|replace|rename",
  "rename_map": {"github": "github-imported"}
}
```

Result:

```
{ "status": "applied", "count": 3 }
```

### Status notifications

MCP status updates are sent as JSON-RPC notifications:

```
{
  "jsonrpc": "2.0",
  "method": "update",
  "params": {
    "type": "mcp_status",
    "payload": {
      "name": "github",
      "state": "connected",
      "tool_count": 12,
      "tools_enabled": 11,
      "tools_disabled": 1,
      "last_error": ""
    }
  }
}
```

## Validation rules and size limits

All MCP config and import payloads are treated as untrusted input.

Validation rules:

- Reject unknown keys and deep nesting beyond the defined schema.
- Enforce ASCII control character rejection in string fields.
- Require HTTPS for `url` on `http`, `sse`, and `oauth` transports. Exception: allow `http://localhost`, `http://127.0.0.1`, or `http://[::1]` for local development.
- No secrets allowed in `mcp_servers.json` (env values, header values, oauth client secret).
- `name` must be unique per file and match the allowed pattern.
- Legacy `enabled_tools`/`disabled_tools` fields are rejected; use `tools.mode`.
- `auth` is ignored for `type=oauth` (OAuth tokens are handled by the OAuth flow).
- Missing required secrets for `secret_env`, `secret_headers`, or `oauth.client_secret_ref` return `mcp_credentials_required`.

Size limits (per file or import):

- Max file size: 512 KB
- Max servers per file: 200
- Max tool names per server: 500
- Max env or header entries per server: 64
- Max key length: 128
- Max value length: 2048
- Max args per server: 64

Cloud sync policy:

- Shared/team policy is read-only, server-signed, and applied as a policy layer (no destructive writes).
- Shared/team settings must be gated by admin/staff claims before applying.
- Sync payloads may only change `enabled` and `tools` allow/deny lists.
- Sync payloads may not set `command`, `args`, `working_dir`, `url`, `headers`, `env`, or OAuth fields.

## Manual QA checklist

1. Build IPC binary: run `bun run build:all` from `/Users/neddana/GitHub/swarm/IDE`.
2. Stdio transport: add a server with `type=stdio`, `command=npx`, and args like `-y @modelcontextprotocol/server-filesystem /tmp`; enable it, verify status moves to connected and tools appear, then run a tool.
3. HTTP transport: add a server with `type=http` and a TLS URL (or `http://localhost` for local testing), set `auth.mode` and credentials if required, enable it, and verify status + tool discovery.
4. SSE transport: add a server with `type=sse` and a TLS URL (or `http://localhost` for local testing), enable it, and verify connection + tool discovery.
5. OAuth transport: add a server with `type=oauth`, `oauth.client_id`, and `oauth.scopes`, run the OAuth login flow, then confirm tokens are in `~/.swarmos/credentials.json` and `mcp_servers.json` only references `client_secret_ref`.
6. Import flows: run Claude, Codex, JSON, and TOML imports; verify conflicts/warnings and that secret values move to credentials (no secrets appear in config).
7. Overrides: create a project server with the same name as a global server and confirm the global entry is marked shadowed in IPC/IDE with an override indicator.
8. Credentials separation: inspect `~/.swarmos/mcp_servers.json` to confirm no tokens/secret values, and `~/.swarmos/credentials.json` to confirm tokens/headers are stored under `mcp`.
9. Reconnect/backoff: stop an MCP server process or disconnect the network and verify status transitions to error with automatic reconnect attempts.

## Error codes

Use JSON-RPC standard codes plus MCP-specific codes in the -32000 range.

- `-32010` mcp_invalid_config
- `-32011` mcp_conflict
- `-32012` mcp_not_found
- `-32013` mcp_not_editable
- `-32014` mcp_credentials_required
- `-32015` mcp_import_error
- `-32016` mcp_transport_error
- `-32017` mcp_permission_denied

## Open items

- Local HTTP exception is limited to loopback hosts (`localhost`, `127.0.0.1`, `::1`).
- Marketplace plugins without an explicit OSI license and source repository are treated as closed source and read-only.
