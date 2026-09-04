# Durable memory history

The `memory_history` Pi tool provides versioned durable recall. Records are stored as
Pi session entries (`pi-swarm-memory`) and are isolated by namespace, workspace,
and session. `remember` redacts common API keys, bearer tokens, and password/token
assignments before appending. `search` matches text and tags; `replay` returns the
scope's records chronologically. The `migrate` operation converts legacy `memory`
entries offline into schema version 1 records. Unknown future versions are ignored
to avoid unsafe interpretation.
