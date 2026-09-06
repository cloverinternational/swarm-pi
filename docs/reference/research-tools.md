# Pi research tools

`research-tools.ts` adds bounded, read-only research primitives to Pi:

- `web_fetch` accepts HTTPS URLs only, blocks private/loopback hosts, supports explicit host allow/deny lists, byte limits, abort-aware timeouts, and returns SHA-256/source retrieval provenance.
- `deepwiki` is an opt-in adapter for `DEEPWIKI_BASE_URL`; `DEEPWIKI_API_KEY` is consumed from the environment or credential JSON and never returned.
- `browser_get_page` is a read-only adapter for an explicitly configured `PI_BROWSER_READ_URL`; it does not navigate or mutate a browser directly.

Existing Exa and xAI extensions remain the search-provider adapters. Credentials are loaded from environment variables or `~/.swarmos/credentials.json`; no live provider calls are required by the offline test suite. Set `PI_BROWSER_READ_URL`/`DEEPWIKI_BASE_URL` only for authorized local bridges. Responses are bounded and include retrieval time, status, content type, byte count, and hash so citations can be traced to the captured response.
