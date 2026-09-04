# `@pi-swarm/schedule`

Swarm-compatible scheduling for Pi. The package exposes a host-neutral scheduler,
an atomic JSON persistence adapter, and Pi tool registrations:

- `cron_create`
- `cron_list`
- `cron_delete`
- `schedule_wakeup`

Durable tasks are stored in the upstream-compatible
`<workspace>/.swarm/scheduled_tasks.json` array. Session-only tasks and wakeups
remain in memory. Fired prompts are delivered through an injected prompt sink;
the Pi extension uses `sendUserMessage(..., { deliverAs: "followUp" })`.

Cron expressions use standard five-field local-time syntax. A non-recurring
cron fires only at its next matching occurrence. Wakeup delays use the positive
subset of Go duration syntax (`30s`, `5m`, `1h30m`) and are capped at one year.

```ts
import { Scheduler } from "@pi-swarm/schedule";

const scheduler = new Scheduler({
  workDir: process.cwd(),
  sink: async (prompt) => console.log(prompt),
});
await scheduler.start();
await scheduler.create({ prompt: "daily check", cron: "0 9 * * *", recurring: true, durable: true });
```
