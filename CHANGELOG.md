# Changelog

Release metadata is kept in `package.json`, `update-manifest.json`, and this file together.

## [Unreleased]

- Use Tailscale LocalAPI ETag/If-Match conditional writes to reject concurrent
  Serve changes, preserve JSON responses up to an explicit 8 MiB bound, and
  revalidate process start-time identity immediately before stopping a daemon.
- Require explicit `/paseo setup apply` or tool `apply: true` before modifying hostname
  configuration or Tailscale Serve; ordinary session startup does not expose the daemon.
- Retain daemon records on failed stops, clean up failed launches, support legacy
  stop records, and serialize update/build with lifecycle operations. Reject
  non-target Serve routes and anchor Linux atomic writes to directory descriptors.

- Automate per-machine Paseo Tailscale setup: preserve nested hostname config,
  hot-reload without restarting agents, configure nonconflicting persistent
  tailnet-only HTTPS routes, and verify HTTP plus WebSocket readiness.
- Use installation-relative Paseo paths, per-user daemon state, loopback defaults,
  bounded asynchronous subprocesses, and shared setup/start locking. Add focused
  regression tests and document installation prerequisites and limitations.

- Harden tool reliability, history argument normalization, and TaskManage validation.

- Add optional `/mem on|off|status` bootstrap memory enforcement with prompt and tool-call safeguards.

- Align HistorySearch field normalization and segment filtering with case, runtime, sorting, and ordering options.

- Add the paseo tool extension and register it in the 30-tools layer.
- Auto-start Paseo sessions with Tailscale-aware listening and add mobile relay pairing.
- Add a bootstrap-settings adapter that augments the native /settings panel.
- Mark the paseo vendor submodule as shallow.

- Add a repository-backed update checker with hourly checks and `/swarm-update`.
- Allow overriding the update manifest URL with `PI_SWARM_UPDATE_URL` for testing.
- Add CI: mandatory changelog gate, release-metadata consistency, build and test.
- Add the Plexus OpenCodeReview (luna) advisory PR review workflow.
- Refine bootstrap selector evidence, shared-memory namespaces, and renderer output.
- Register the agent-mcp and oh-my-pi vendor submodules in `.gitmodules` so CI checkout succeeds.
- Sync `package-lock.json` with the `@pi-swarm/bootstrap` workspace so `npm ci` succeeds.
- Make tests CI-safe: honor explicit `headless: false` in InteractionBroker and derive the project name from the checkout path.

To publish a release, update the root package version, this changelog, and
`update-manifest.json` in one commit, then tag the commit. The checker only
notifies; `/swarm-update install` delegates installation to Pi with
`pi update --extensions`.

## [0.1.0] - 2026-01-01

- Initial Pi-Swarm extension pack release.
