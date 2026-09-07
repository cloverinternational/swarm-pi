---
name: loop
description: Run a prompt or slash command on a recurring interval (e.g. /loop 5m /foo, defaults to 10m)
when_to_use: When the user wants a recurring task / poll / repeat on an interval. Not for one-off tasks.
version: 1.0.0
---
Use the session-local scheduler tool. Do not search source code or implement scheduling.

If the prompt is empty, show this usage message:
Usage: /loop [interval] <prompt>

Run a prompt or slash command on a recurring interval.

Intervals: Ns, Nm, Nh, Nd (e.g. 5m, 30m, 2h, 1d). Minimum granularity is 1 minute.
If no interval is specified, defaults to 10m.

Examples:
  /loop 5m /babysit-prs
  /loop 30m check the deploy
  /loop 1h /standup 1
  /loop check the deploy          (defaults to 10m)
  /loop check the deploy every 20m

Otherwise:
1. Extract the interval and prompt (default interval: 10m).
2. Call scheduler with action="create", interval=the interval, prompt=the task.
3. Execute the prompt immediately (don't wait for the first cron fire)
4. Confirm to the user with the job ID and that recurring tasks auto-expire after 7 days

For a one-shot self-wake, call scheduler with action="create", delay="5m" (or the desired delay), and prompt=the next instruction. Use action="list" to inspect schedules or action="cancel" with id to cancel. Schedules belong to the current TUI session and stop on shutdown.
