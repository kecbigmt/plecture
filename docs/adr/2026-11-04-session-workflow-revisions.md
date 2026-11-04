---
supersedes: 2026-09-08-minimum-cleanup-contract
---

# Session workflow revisions

## Context

A session stores a workflow name while lifecycle operations reconstruct its
definition from the current configuration tree. A configuration edit can then
change an existing session's setup, repair, health, delivery, or cleanup without
an explicit transition. Removing a definition can make outstanding cleanup
impossible. Deployment restarts and configuration edits can coincide, so an
automatic repair may execute a new declaration against an old allocation.

## Supersession scope

- `2026-09-08-minimum-cleanup-contract`, `### Lifecycle-configuration change notification`:
  the comparison with current configuration before each lifecycle operation,
  its warning, and its session-wide execution baseline.
- `2026-09-08-minimum-cleanup-contract`, `### Cleanup and execution records`:
  the decision to resolve cleanup executable code from the current trusted
  configuration tree rather than a stored definition.
- `2026-09-08-minimum-cleanup-contract`, `### Migration`: the outcome that an
  unresolved legacy declaration remains a cleanup precondition failure while
  startup proceeds without a revision.

## Decision

Session creation expands the selected workflow and the effect and task
declarations its session can execute, including dynamic task instances,
resolved nesting, and cascade, and stores a canonical JSON body in
`workflow_revisions`. The body contains nodes, binding
expressions, health and healthcheck declarations, event channels, ticks, and
instruction text. Its SHA-256 digest is the revision identity. Canonical JSON
sorts object keys recursively, retains array order, uses UTF-8 without HTML
escaping, and contains no insignificant whitespace. The workflow address is
part of the body, so distinct workflow identities cannot share one row. Equal
expanded definitions share a revision row.

Each session with a workflow references one revision. Lifecycle planning and
run-state evaluation use that revision for setup, repair, health, tick,
delivery, and cleanup, including when the original workflow file is gone. A
configuration edit does not replace the session's revision. To repair a cleanup
declaration, the operator edits trusted configuration and explicitly rebases
the session before cleanup; until rebase is available, destroying and recreating the
session is the transition path. Lifecycle operations do not compare current
configuration with the stored revision or update a notification baseline; the
revision shown in session inspection identifies the declarations they execute.

The database becomes a source of executable declarations. Only the trusted
configuration tree expanded by serve or the CLI may write a revision; runtime
inputs, untrusted messages, and stored execution results cannot author one.
Plugin executable contents, executable lookup through `PATH`, and population
definitions are outside the revision. Population continues to use current
configuration. A session without a workflow has no revision; its dynamic task
declarations and channels resolve from current trusted configuration.
Executable references resolve against the mounted plugins when an operation
runs. A plugin-owned declaration records its catalog-qualified
plugin ID and path relative to the plugin mount. A change in the mount's cache
directory does not change the revision or break a bare executable reference.

`workflow_revisions` has `digest` as primary key, plus `workflow`, `body`, and
`created_at`. `sessions.workflow_revision` identifies the stored body. A
revision with no live session references is deleted after destroy and during
periodic cleanup; there is no reference counter. `plect ls` and `plect status`
show the first eight hexadecimal characters of the digest.

Migration backs up the data directory and trusted configuration, then expands
each existing session's workflow from the configuration present at migration
time and assigns its revision. Startup stops with the affected session's name
if a required definition cannot be resolved. The operator restores that
trusted declaration from backup and retries. Migration does not invent a
partial revision or reconstruct missing execution facts. A durable completion
marker prevents an incomplete session created after migration from restarting
the backfill. Such a session can retry setup or be removed with force.

## Consequences

Existing sessions keep consistent behavior across configuration changes and
restarts. Storage grows with distinct expanded definitions rather than session
count. A stored revision must be protected as executable configuration, and
configuration fixes require an explicit session transition before they affect
existing allocations. A plugin upgrade or removal can make a stored executable
reference fail during cleanup; restoring the plugin, rebasing when available,
or explicitly discarding the session with force are the operator's choices.

## Alternatives considered

### Resolve current configuration on every operation

This permits implicit behavior changes and can remove the declarations needed
for cleanup.

### Store a copy per session

This duplicates identical definitions across sessions and complicates cleanup.

### Run a session without a revision after migration failure

This would silently resume execution from current configuration, recreating
the behavior the revision is meant to remove.
