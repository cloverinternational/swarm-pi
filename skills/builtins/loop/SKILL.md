---
name: loop
description: Run a prompt or slash command on a recurring interval (e.g. /loop 5m /foo, defaults to 10m)
when_to_use: When the user wants a recurring task / poll / repeat on an interval. Not for one-off tasks.
version: 1.0.0
---
Parse the user's input using ParseLoopInput to extract the interval and prompt.

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
1. Convert the interval to a cron expression using IntervalToCron
2. Call the CronCreate tool with:
   - cron: the cron expression
   - prompt: the parsed prompt
   - recurring: true
3. Execute the prompt immediately (don't wait for the first cron fire)
4. Confirm to the user with the job ID and that recurring tasks auto-expire after 7 days

If no interval was given and the model wants to self-pace, use the ScheduleWakeup tool instead, passing the same input verbatim and the sentinel <<autonomous-loop-dynamic>>.
