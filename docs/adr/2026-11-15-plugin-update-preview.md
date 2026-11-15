---
supersedes: 2026-11-09-operator-declared-plugin-services
---

# Plugin update preview and service restart condition

## Context

An operator can update a plugin while a declared service uses it. The earlier
restart condition refers to code changes, but the supervisor compares the
plugin's content hash. An operator also needs to see the files and services
affected by an update and retain the resolved revision for a later pinned
update.

## Supersession scope

This decision replaces one decision of
`2026-11-09-operator-declared-plugin-services`:

- `## Decision`: the restart condition for an already-declared service during an explicit plugin update.

## Decision

An explicit `plect plugin update` authorizes restarting an already-declared
service when the plugin's content hash changes. Before any build or lockfile
change, the command shows changed files grouped by kind, the declared services
that will restart, and the resolved target revision. It compares the mounted
plugin tree with the fetched tree. If the old tree is unavailable, it says so
and lists every fetched file as changed.

`--dry-run` prints the same summary without building, changing the lockfile,
or restarting services. The printed revision can be passed to a later
`plugin update --revision` to install the reviewed revision even if upstream
advances. The summary is an awareness aid, not a supply-chain control. No
per-executable hashes are stored.

## Consequences

Operators can inspect the update and apply a pinned revision later. A catalog
revision change without a plugin content change does not restart that plugin's
services.

## Alternatives considered

- Track hashes per executable. This adds state without changing the
  supervisor's plugin-level restart decision.
- Require confirmation before applying an update. The preview informs the
  operator without adding an interactive step to automation.
