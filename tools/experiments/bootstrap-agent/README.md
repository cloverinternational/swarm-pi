# Bootstrap-agent experiment harness

This is an isolated, opt-in benchmark harness. It uses synthetic evidence and
real `pi -p --model clover-plexus/claude-fable-5` subprocesses with tools,
extensions, context files, and skills disabled. Selector IDs are resolved only
against the supplied corpus; evidence and task proposals remain untrusted.

Run the requested screening (three cases × four arms):

```sh
node tools/experiments/bootstrap-agent/runner.mjs
```

Outputs are generated under ignored `artifacts/bootstrap-agent/`. Token usage
is reported only when Pi's JSON response exposes it; otherwise it is explicitly
`unavailable`. This harness does not create production tasks, persist memory,
or prove first-call integration.
