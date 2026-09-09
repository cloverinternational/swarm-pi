# Changelog

Release metadata is kept in `package.json`, `update-manifest.json`, and this file together.

## [Unreleased]

- Add a repository-backed update checker with hourly checks and `/swarm-update`.

To publish a release, update the root package version, this changelog, and
`update-manifest.json` in one commit, then tag the commit. The checker only
notifies; `/swarm-update install` delegates installation to Pi with
`pi update --extensions`.

## [0.1.0] - 2026-01-01

- Initial Pi-Swarm extension pack release.
