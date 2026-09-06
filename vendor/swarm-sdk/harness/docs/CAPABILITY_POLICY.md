# Harness capability policy

This table is the durable policy source for the declarative harness capability
catalog. The catalog records exposure intent, not ambient runtime availability.
Every capability must be selected by its stable ID; aliases are informational
and never grant access.

`KEEP` means catalog retention, not active exposure. `OPT-IN` capabilities need
an explicit plan selection and any applicable host, credential, or permission
binding. `DEFER-DISCOVER` capabilities remain unavailable until their public
contract is complete. `NEVER-DEFAULT` capabilities additionally require an
explicit per-capability acknowledgement.

Unless a row narrows its roots, each class applies independently to TUI, print,
daemon, headless, client, MCP, and serve roots. Every per-client capability has
exactly one primary class; qualifier columns do not create compound classes.

## Policy classification

| Capability | Roots | Primary class | Active in minimal preset | Host-bound | Credential-bound | Compatibility-only | Presentation-only | Rationale / guard |
|---|---|---|---:|---:|---:|---:|---:|---|
| Explicit provider/model + typed credential reference | all | KEEP | explicit required | no | yes | no | no | Necessary core; raw secret values prohibited. |
| Explicit prompt, workspace, storage, limits | all | KEEP | explicit required | no | no | no | no | No ambient INDEX/context/config merge. |
| `Read` catalog entry | all | KEEP | no | no | no | no | no | Exact selection plus path boundary. |
| Forge `Grep`, unified `grep`, builtin `grep`, `semantic_grep` catalog entries | all | KEEP | no | no | no | no | no | Separate IDs/schemas; exact selection, never alias collapse. |
| `apply_patch` catalog entry | all | KEEP | no | no | no | no | no | Exact selection plus explicit file-mutation permission. |
| `Undo` catalog entry | all | KEEP | no | no | no | no | no | Exact selection plus explicit file-mutation permission. |
| `semantic_rename` catalog entry | all | KEEP | no | no | no | no | no | Exact selection plus explicit file-mutation permission. |
| Hidden legacy `Edit`/`Write` adapters | all | NEVER-DEFAULT | no | no | no | yes | no | Only a named replay/migration compatibility path may retrieve them. |
| Shell/process tools | all | OPT-IN | no | yes | no | no | no | Explicit command/path/approval policy and lifecycle required. |
| Network/web search/fetch | all | OPT-IN | no | no | yes | no | no | Explicit egress and credential policy. |
| Static skills | all | OPT-IN | no | no | no | no | no | Selected content/hash must enter provenance. |
| Hooks | all | OPT-IN | no | yes | no | no | no | Named ordering, timeout, verdict, and failure policy. |
| MCP tools/resource adapters | all | OPT-IN | no | yes | often | no | no | Required server barrier and exact post-discovery allowlist. |
| MCP prompt API | all | OPT-IN | no | yes | often | no | no | Protocol API capability; not provider-tool exposure. |
| Task state/TaskManage | all | OPT-IN | no | often | no | no | no | Persistence/scope must be explicit. |
| Question/plan/visual brokers and tools | all | OPT-IN | no | yes | no | no | partly | Missing selected broker must fail preflight. |
| History / findings (`findings_query`, `HistorySearch`, `HistoryGet`) | all | DEFER-DISCOVER | no | no | no | no | no | Store scope and redaction need a public contract before exposure. |
| Project memory | all | DEFER-DISCOVER | no | no | no | no | no | Initialized storage is not tool exposure; public scope contract absent. |
| Compaction | all | OPT-IN | no | no | no | no | no | Separate context, output, and total budgets. |
| `run_code` (code mode) | all | OPT-IN | no | no | no | no | no | Code mode alters provider-visible policy; explicit opt-in only. |
| `tool_search` (deferred discovery) | all | OPT-IN | no | no | no | no | no | May never promote a capability outside the plan allowlist. |
| Profiles/fallback graphs | all | DEFER-DISCOVER | no | no | yes | no | no | Public graph, retry taxonomy, and provenance required first. |
| Task/Delegate/subagent/background agents | all | DEFER-DISCOVER | no | yes | yes | no | no | Exact child capability intersection and lifecycle required. |
| Schedules/wakeup | all | DEFER-DISCOVER | no | yes | yes | no | no | Clock, persistence, leader, recovery, and prompt sink unresolved. |
| A2A/swarm transport and tools | all | DEFER-DISCOVER | no | yes | maybe | no | no | Host ownership unresolved. |
| Plugins | all | DEFER-DISCOVER | no | yes | maybe | no | no | Hook conversion incomplete; no false active claim. |
| Serve/listener interfaces | all | DEFER-DISCOVER | no | yes | no | no | yes | Interface adapter concern, not core agent policy. |
| Steering/internal tools | all | DEFER-DISCOVER | no | yes | no | no | no | Specialist host/loop-control contract required before exposure. |
| Debug/raw trace tools | all | OPT-IN | no | yes | no | no | partly | Redaction and sensitive-log policy required. |
| Browser/computer-use/deploy/database specialist tools | all | NEVER-DEFAULT | no | yes | maybe | no | no | High-impact host, UI, or network side effects. |
| Credential resolution (reference only) | all | KEEP | explicit required | yes | yes | no | no | Resolve references without exposing raw values. |
| `vault_list`/status/approval inspection | all | OPT-IN | no | yes | yes | no | no | Explicit vault binding and audit. |
| `vault_exec`/`vault_add` | all | NEVER-DEFAULT | no | yes | yes | no | no | Secret mutation and host command execution. |
| Autoskills/curator | all | NEVER-DEFAULT | no | yes | yes | no | no | Prompt mutation, hooks, background model calls, and budgets. |
| Yolo/automatic approval | all | NEVER-DEFAULT | no | yes | no | no | no | Compatibility risk, not desired default policy. |
| Automatic global daemon startup | all | NEVER-DEFAULT | no | yes | no | no | no | Surprising listener and background lifecycle. |
| Ambient config/INDEX/MCP/skills/plugins/provider persistence | all | NEVER-DEFAULT | no | no | maybe | no | no | Includes ambient writes as well as reads. |

Legacy interfaces may intentionally violate this desired table. Compatibility
presets must describe that drift explicitly rather than reclassifying it as the
desired harness posture.
