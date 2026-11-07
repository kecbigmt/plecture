---
supersedes:
  - 2026-09-07-resource-and-workspace-effects
  - 2026-09-07-invocation-project-context
---

# Destroy takes no plugin cleanup inputs, and force has one meaning

## Context

`2026-09-07-resource-and-workspace-effects` lets the current destroy operation
supply plugin-owned cleanup inputs beside `force`. An effect declares the keys
it reads with `[<id>.cleanup.inputs_schema]` and reads them as
`cleanup.inputs.<key>`. A caller supplies them through `plect destroy --input`,
the `cleanup_inputs` argument of the MCP `plect_destroy` tool, or a
population's `session.destroy.inputs`. `2026-09-07-invocation-project-context`
states the same: cleanup uses the execution record plus the current
operation's force and plugin-owned cleanup inputs.

One key uses that surface: the shipped GitHub `worktree` effect's
`delete_branch`. For that one boolean, the language carries a declaration
syntax, a root, three entry points, and rules for routing keys between effects
and coercing their types.

The surface also disagrees with the rest of the design in two ways:

- The policy for what a cleanup releases has two authorities: the declaration,
  which the session's revision fixes, and the arguments of each destroy call.
  When they disagree, the call wins, but nothing in the session records which
  one applied.
- `2026-11-04-session-workflow-revisions` fixes the declarations a session runs
  at creation. Cleanup inputs are the one part of a session's behavior that
  every call can still change.

`force` has no single meaning either. In core it turns a refusal into a warning
in several places. Effects receive it as an intent with no stated scope. An
agent that reads "re-run with `--force`" in an error can act on that hint
without asking anyone, and lose work the refusal was protecting.

## Supersession scope

- `2026-09-07-resource-and-workspace-effects`,
  `### Execution identity and ownership`: that the current operation supplies
  `force` and cleanup inputs, validated against the retained cleanup contract.
- `2026-09-07-resource-and-workspace-effects`, `## Consequences`: that the
  implementation must preserve invocation cleanup inputs, including
  plugin-owned options, and that creation inputs do not authorize destructive
  cleanup.
- `2026-09-07-invocation-project-context`, `## Decision`: that cleanup uses the
  current operation's plugin-owned cleanup inputs.

## Decision

### Destroy carries only `force`

A destroy operation passes no plugin-owned input to cleanup. The language has no
`[<id>.cleanup.inputs_schema]`, no `cleanup.inputs` root, and no population
`session.destroy.inputs`. The CLI has no `plect destroy --input`, and the MCP
`plect_destroy` tool has no `cleanup_inputs` argument. Each retired spelling is
refused with a pointer to the migration procedure rather than read as unknown.

What a cleanup releases is decided when the session is created, through the
effect's inputs. An effect whose release policy one session may need to change
records that policy as a `mutable` output, and its cleanup reads
`self.outputs.<key>`. `plect state set-output` rewrites it before destroy. The
change is then part of the session's record, not an argument that each caller
must remember. A creation input may therefore authorize a destructive cleanup
step. The effect owns the safeguard that limits what that step can destroy.

### `force` means: run destroy to the end

`plect destroy --force` ignores destroy's safeguards and errors and runs it to
completion. In core it:

- orphans child sessions;
- removes a session that has no stored workflow revision, without cleanup;
- demotes a failed cleanup to a warning, records that the release was not
  verified, and deletes the session's records;
- releases the prerequisites of a node whose cleanup failed.

Every effect's cleanup receives `force = true`, which permits it to override its
own safeguards as well, such as removing a checkout that has uncommitted
changes. Cleanup receives `force = false` on every other destroy. `plect up
--force-recreate` keeps passing `force = true` to the cleanup it runs before
rebuilding a node.

The language reference, the CLI help, and the MCP tool description each state
this meaning and that what the safeguards protected can be lost.

### An agent asks a human before forcing

The MCP `plect_destroy` tool keeps `force`. The sessions plect destroys mostly
hold the agent's own work, and an agent that cannot clean up after itself when
something goes wrong is the larger cost. The tool description and the `force`
argument description tell the agent to report why destroy failed without
`force`, and to get a human's confirmation before setting it. Every destroy
error that suggests re-running with `--force` repeats that instruction, so an
agent that reads only the error does not retry with `force` on its own.

### The GitHub worktree's branch policy

The `worktree` effect treats branch deletion like GitHub's "automatically
delete head branches" repository setting. The creation input `delete_branch`
(default `false`) replaces `delete_branch_default`. Setup records it as the
mutable output `delete_branch`, and cleanup reads that output. Deletion stays
local and safe (`git branch -d`), so unmerged work is never deleted.

## Consequences

- One authority decides what a cleanup releases: the session's own record. The
  record shows the policy that a destroy will apply, for the node's current
  setup.
- A `set-output` rewrite does not survive a re-setup of its node. A node that is
  set up again records the creation-time value again, whether the rebuild comes
  from `up --force-recreate`, a rebase, an invalidated prerequisite, or a
  failed liveness probe, so the operator re-applies the rewrite after a
  rebuild. Carrying mutable outputs across a re-setup would remove this limit,
  but it changes `mutable` for every effect and is not part of this decision.
- A script that passed `--input` or `cleanup_inputs` fails with a pointer to
  the migration procedure. The same happens to a declaration that uses a
  retired spelling. The procedure lives in
  `docs/migrations/resource-and-workspace-effects-migration.md`, because this
  change ships in the same breaking release.
- Changing one session's policy takes two steps, `set-output` and then
  `destroy`, where it used to take one destroy call.
- A plugin that needs a destruction-time choice must model it as session
  state, not as an option of the destroy call.

## Alternatives considered

### Keep cleanup inputs and validate them against the revision

Validation against the fixed revision would remove the drift between versions.
The policy would still have two authorities, and the language would keep
supporting a whole surface for one boolean.

### Remove `force` from the MCP tool

An agent could then never discard work without a human running the CLI. Most
sessions hold the agent's own work, and leaving an agent unable to free a stuck
session costs more than the confirmation step does. The tool keeps `force` and
requires the confirmation in its description instead.

### Delete the branch with `git branch -D` under `force`

This would let `force` also delete unmerged branches. Unlike an uncommitted
change in a worktree that is being removed anyway, an unmerged branch is work
that outlives the session. Its deletion stays safe in every case.
