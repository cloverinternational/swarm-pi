# swarm-policy

Deterministic, fail-closed authorization primitives for Pi tools. This package is the enforcement seam; registration, prompts, and UI approval are not security boundaries.

- filesystem paths are workspace-bound (including symlink resolution)
- process execution is explicit, bounded, shell-free by default, and killable
- network hosts and credential environment variables are allowlisted
- output is byte-limited and audit records are redacted
- dangerous operations require an explicit approval token

The executor is intentionally offline and uses Node built-ins, so it can be tested without provider credentials.
