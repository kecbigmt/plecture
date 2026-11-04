---
supersedes: 2026-09-07-resource-and-workspace-effects
---

# Workflow public outputs are derived on read

## Context

`2026-09-07-resource-and-workspace-effects` stores the declaration-owned public
projection as session-owned JSON and refreshes or invalidates it when a source
execution changes. That decision predates frozen session workflow revisions.
While each operation reloaded current configuration, the declaration could
change between operations, so a stored evaluation had a purpose. A session now
freezes its revision at creation, which makes the record a pure function of
data the session already stores: the revision's `outputs` declaration, the
stored node outputs, and the session inputs.

A stored copy would add a column, a migration, and an invalidation duty on every
write path that changes nodes, including pruning a released node, repairing a
shadowed execution, importing a session, and destroying one.

## Supersession scope

- `2026-09-07-resource-and-workspace-effects`, `### Workflow public outputs`:
  the decision to store the declaration-owned public projection as
  session-owned JSON that is refreshed or invalidated when a source execution
  changes.
- `2026-09-07-resource-and-workspace-effects`, `## Decision`: the clause that
  public outputs are persisted with the session.

## Decision

Public outputs are not stored. The record `workflow.outputs.*` reads is
evaluated on each read from the session's frozen revision, its stored node
outputs, and its inputs, so it has no invalidation of its own. A binding is
evaluable once every node it reads is a source.

One evaluation serves every reader, and a parameter selects which nodes count as
a source. Display, instructions, event delivery, and rebase classification count
a node only while it is produced, so they never show a value a released or
failed node merely remembers. A setup or cleanup inside a run counts the last
recorded outputs of any node that has a record, because a dependent of a node
whose liveness failed, or that was left failed, is released after that failure
is recorded and must still resolve its bindings.

A complete record must satisfy `outputs_schema`; a record that does not is not
published. A binding that fails although every node it reads counts as a source
is named in a warning rather than failing the operation.

The unchanged parts of the superseded decision remain in force, including the
expanded producer dependencies that join cycle detection and preparation-graph
derivation, and that a recorded directory is a historical fact rather than a
current output.

## Consequences

Evaluation cost is paid per read, bounded by the declared bindings; revision
decoding is already cached by digest. A cache can be added later without a
schema change. A workflow edit that changes a binding reaches an existing
session only through an explicit rebase, which treats a node that reads a
changed public output as a changed node.

## Alternatives considered

### Keep the stored projection

This keeps a copy that duplicates what the revision, node outputs, and inputs
already determine, and every node write path must remember to refresh it.

### Derive the stored copy inside each write transaction

This repairs the omitted-write and reset cases but still leaves write paths that
skip the refresh and adds a column and migration for no information the session
does not already hold.
