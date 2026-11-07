# Resource and workspace effects migration

This procedure moves a configuration tree to the resource-entry and ordinary
environment-effect dialect. It is a breaking migration: `workspace_provider`
and `resource_observer` have no aliases, provider output records have no
compatibility reader, and a session is never reconfigured from its current
directory.

Read the whole procedure before making changes. It accompanies the release
that removes the workspace provider. The loader refuses each retired spelling
with a diagnostic that points here: a `kind = "workspace_provider"`
declaration, a workflow's `workspace_provider` or `workspace_provider_inputs`
field, a population's `resource_observer`, `workspace_dirs_root` in
`config.toml`, an effect's `[<id>.cleanup.inputs_schema]`, a
`cleanup.inputs.<key>` reference, and a population's `session.destroy.inputs`.
`plect destroy --input` and the `cleanup_inputs` argument of the MCP
`plect_destroy` tool are refused the same way.

## Where the new release stops

A session pinned to a workflow revision from the previous release cannot run
on this release, because that revision declares its environment with
`workspace_provider`, `resource_observer`, and the `@workflow` pseudo-node,
which this release no longer reads. Nothing converts such a revision. Two
independent stops refuse it, and step 2 below clears both:

- **Storage stop.** The first command of the new release upgrades the
  database and refuses while a session that has not been destroyed still holds
  an unreleased `@workflow` record: a worktree, conversation directory, or
  scratch directory that a workspace provider acquired. The error names this
  procedure. By then an earlier part of the upgrade has already been applied,
  so the previous release refuses the database as newer than it supports.
  Restore the data backup taken right before upgrading (step 3), reinstall
  the previous release, and continue with step 1.
- **Lifecycle stop.** A session that passes the storage upgrade but whose
  pinned revision still uses a retired spelling is listed with `!retired`
  after its revision in `plect ls`, and `plect status` names the spellings
  (`retired_spellings` in JSON output). This release refuses `up`, `down`,
  `destroy`, automatic repair, and `workflow rebase` for it, naming the
  spellings and this procedure. Destroy the session with the previous release.
  `plect destroy <session> --force` on this release is the only operation that
  passes: it removes the record without running cleanup, so nothing verifies
  that the session's directories or other allocations were released.

## Procedure

Steps 1 and 2 run with the previous release; the new release cannot release
what a workspace provider acquired.

### 1. Save uncommitted work

With the previous release still installed, list the sessions that have not
been destroyed (`plect ls`). For each one, save what its directory holds that
exists nowhere else: commit and push branches, or copy files out. Step 2
deletes those directories.

### 2. Destroy sessions with the previous release

With the previous release, `plect destroy` every session listed in step 1.
Its cleanup frees the directory and anything else the workspace provider
acquired. Add `--force` only for a session whose cleanup fails and whose
directory you have already saved or released by hand. Run `plect ls` again
and confirm that no session remains.

### 3. Back up configuration and durable state

Stop resident processes before copying their configuration and durable state.
The backup must include the global tree, every selected project tree, and the
state directory, so that a rollback can restart the previous release on them.

```bash
CONFIG_ROOT="${PLECT_CONFIG_HOME:-$HOME/.config/plect}"
DATA_ROOT="${XDG_DATA_HOME:-$HOME/.local/share}/plect"
STAMP="$(date -u +%Y%m%dT%H%M%SZ)"
cp -a "$CONFIG_ROOT" "$CONFIG_ROOT.resource-effects-backup.$STAMP"
cp -a "$DATA_ROOT" "$DATA_ROOT.resource-effects-backup.$STAMP"
```

For each project root, copy its `.plect/` tree to a sibling backup before
placing a project marker or consolidating configuration.

`PLECT_CONFIG_HOME` is the supported override for the global configuration
tree. Durable state is under `XDG_DATA_HOME` as shown above; this procedure
does not name a state-directory override.

### 4. Rewrite the configuration

1. Rewrite every `kind = "resource_observer"` as `kind = "resource"`, adding
   `name`, delivery actions, and the resource action input schema where the
   former provider owned them.
2. Rewrite every `workspace_provider` as a resource declaration plus one or
   more ordinary `scope = "session"` effects. Move worktree, Slack-thread,
   and local environment setup, cleanup, liveness, and outputs to those
   effects. Cleanup still receives `force`; a release option the provider took
   becomes a creation-time input, as item 5 describes.
3. Replace a workflow provider field with its entry `resource`, add nodes for
   its effects, and add one `workdir = { from = "nodes.<id>.outputs.<key>" }`
   where the workflow needs a directory. Do not create per-node cwd settings.
   The directory-creating node and everything it depends on run in your home
   directory; every other node runs in the workdir. Remove
   `workspace_dirs_root` from `config.toml`: a directory-creating effect takes
   its root as an input (the shipped `worktree` and `thread_dir` effects call it
   `dirs_root` and default to `~/workspace_dirs`).
4. Replace `@workflow` output consumers with explicit workflow public
   outputs and `outputs_schema`; rewrite display, instructions, channel, and
   downstream bindings to `workflow.outputs.*`.
5. Move a release option a provider took (such as whether to delete a branch),
   or a key of an effect's `[<id>.cleanup.inputs_schema]`, to a creation-time
   input of the effect. Setup records it as a `mutable` output and cleanup
   reads it as `self.outputs.<key>`. Remove every
   `[<id>.cleanup.inputs_schema]`, every `cleanup.inputs.<key>` reference, and
   every population `session.destroy.inputs` table. Where a script ran
   `plect destroy --input key=value`, set the policy as the node's input when
   the session is created, or rewrite it for one session before destroying it:
   `plect state set-output <session> --node <id> '{"key":value}'`. For the
   shipped `github` `worktree` effect, the node input `delete_branch_default =
   "true"` becomes `delete_branch = true`, and `plect destroy <session> --input
   delete_branch=true` becomes `plect state set-output <session> --node
   worktree '{"delete_branch":true}'` followed by `plect destroy <session>`.
   `force` remains a cleanup root of every effect; an agent that uses
   `plect_destroy` asks a human before setting it.
6. A direct invocation reads one project definition tree: the `.plect/` of
   the nearest directory at or above it whose `.plect/project.toml` exists,
   and only when that directory is listed in `trusted_project_roots`. See
   [Establish project contexts](#establish-project-contexts).
7. Move a workflow's initial task selection to each invocation, chain, or
   population, as described in "Initial tasks" below.
8. Delete `resource_observer` from every `[[<workflow>.populations]]` entry.
   A population queries its workflow's entry `resource` and validates `query`
   against that resource's query `inputs_schema`; the loader refuses the
   retired field with a diagnostic that points here. A population whose
   `resource_observer` named a different resource than its workflow's
   `resource` moves to a workflow whose entry resource is that resource.

### Session names

A session created from a resource identifier is named after the resource name
alone (the value the resource's `name` derives); the workflow id is no longer
appended as a default tag. `plect up <resource> --tag <tag>` appends `+<tag>`.
A session records the entry resource and the tag it was created for, and a
live session with the composed name but a different entry is reported as a
collision.

1. Rewrite scripts, chains, and orchestration prompts that build a session
   name as `<resource-name>+<workflow-id>`: the name of an untagged session is
   the resource name. Pass `--tag` where two sessions for one resource must run
   side by side, for example a work session and a review session.
2. Every session this release runs was created by it, so it records a
   resolved entry: a session created before this release is destroyed in
   step 2.
3. A workflow whose sessions others locate by name (`plect status`,
   `plect event publish`) needs the same rewrite in those callers.

### Resources

The storage migration records each external resource once, in a `resources`
table, and every table that referred to a resource by its identifier string
refers to that row instead. A resource is identified by the name its
declaration's `name` derives, so two spellings of one identifier are one
resource.

1. The migration cannot evaluate a declaration's `name`. Each distinct
   identifier stored before the upgrade becomes one resource with no type,
   named by its own id, and every session, task, node (its resource and its
   subscription), population member and delivery retry that stored the
   identifier refers to it.
2. No live session should reach this: step 2 destroys every session the
   previous release created. A session left live anyway meets the lifecycle
   stop described above, including a resource-addressed `plect up` of its
   identifier. A population member recorded before the upgrade refers to the
   untyped resource, so the population's next poll admits the same item as a
   new member and tombstones the old one, whose session then follows the
   population's destroy policy (`auto_destroy`, or a recorded dry run); that
   destroy meets the same stop unless the policy forces it.
3. `plect status --json` reports `identity.resource` (`type`, `name`,
   `locator`) in place of `identity.resource_id` and `identity.resource_type`,
   and `plect ls --json` reports `resource` in place of `resource_id`. The Web
   API's `SessionSummary` and `SessionDetail` likewise carry `resource`
   (`type`, `name`, `locator`) in place of `resourceId`, and a summary for a
   session with no resource omits it. The identifier is `locator`; rewrite a
   consumer that reads `resource_id` or `resourceId` to read
   `resource.locator`.
4. Resource names are unique across resource types. Two declarations whose
   `name` derives one value from different identifiers conflict: the second is
   refused when it resolves. Change one declaration's `name` so their values
   differ. A `name` that derives a value outside
   `[A-Za-z0-9][A-Za-z0-9/_+-]*` is refused; correct its expression.
5. An effect that parsed the tag out of `session.name` reads `session.tag`
   instead, because a resource name may contain `+`. The shipped GitHub
   `worktree` effect passes `"--tag", { from = "session.tag", default = "" }`
   to `github-worktree setup`; make the same change in a host copy of it.

### Initial tasks

Core selects a session's initial task. `plect up --task <task>` no longer
writes `{"task": <task>}` into the session inputs for a workflow node to act
on: core checks that the task document's resource type claims the binding,
fails before creating anything when it does not, and instantiates the task as
the session's `initial` instance after the workflow's session nodes run.

1. Delete the node that instantiated the initial task from every workflow
   (in a typical tree, a `uses = "initial_task"` node whose setup ran
   `plect task setup "$task" --name initial`), and the effect declaration once
   nothing uses it. A workflow that keeps the node and is invoked with
   `--task` creates `initial` twice over: the node first, then core, which
   keeps the node's instance only when it is the same task bound to the same
   resource and otherwise fails the up.
2. Remove `task` from each workflow's `inputs_schema`, including its
   `required` entry and any `"none"` enum value. A remaining `task` input is
   an ordinary session input with no meaning to core.
3. Move each chain's `[<doc>.chains.inputs] task = "<task>"` to the chain's
   own `task = "<task>"` field. Loading checks that the task is written for
   the target workflow's resource type and reports
   `PLECTURE-CFG-RESOURCE-OBSERVER-MISMATCH` otherwise.
4. Replace `plect up --task none` (and an MCP `task` of `"none"`) with
   omitting the task. `none` is no longer special and resolves like any
   other task id.
5. Where the initial task works a resource other than the entry, pass it as
   `--task-resource <resource-id>`. A task bound to the entry resource needs
   no flag.

### Establish project contexts

For every intended project configuration, choose one project root, consolidate
only that root's intended `.plect/` definitions, and add this marker:

```toml
schema_version = 3
```

It contains no definitions or other fields and declares only the selected
project layer's dialect. Add the root's canonical path to
`trusted_project_roots` in the machine configuration, or run `plect workflow
list` from below the root at a terminal and confirm the trust prompt, which
records the same entry. Do not preserve ancestor fragments or copy
configuration from a generated checkout.

A project definition replaces a global or plugin definition with the same id
as a whole; it no longer merges into it. Write each project workflow as a
complete declaration, including its `resource` and every node. A project tree
declares only effects, task documents, and workflows: a resource or channel
there is a load error, so move it to the global configuration.

A population is identified by the configuration context that declares it as
well as by its workflow and name, and every existing population belongs to the
global-only context. A population-owned session keeps its population's
context. To move a population into a project root, destroy its sessions with
the pre-upgrade population configuration in place, then declare it in the
project tree: the resident
evaluator started from that root admits its members afresh. `plect serve`
selects the context of the directory it starts in and stops when that root is
not trusted; start it from the project root, or from a directory with no
project marker for the global-only context.

### 5. Upgrade the binary

Install the new release. Its first command upgrades the database: it removes
the remaining `@workflow` records and the stored workspace directory column,
and records resources and project roots as "Resources" and "Establish project
contexts" describe. The upgrade cannot reconstruct a directory from the
removed records, because a workflow's work directory is evaluated from the
workflow's `workdir` and never stored. If the upgrade refuses, see "Where the
new release stops" above.

### 6. Recreate sessions

Run `plect ls` and confirm that no session is marked `!retired`. Recreate the
sessions you need with `plect up <resource>`, adding `--tag` where two
sessions for one resource run side by side. Run it from below the trusted
project root whose workflow the session uses: the upgrade records no project
root for an existing session, and a new session records the root its
invocation selected.

## Verify and recover

Run the release's configuration validation and inspect every workflow that has
agent actions, every population, each resource action input contract, and both
normal cleanup and explicit force-discard paths. A configuration edit does not
change an existing session until `plect workflow rebase` moves it to the new
definition. Test a vanished work directory: cleanup must fail its
precondition without changing directory. Keep the timestamped copies until
sessions have been recreated and observed in normal use.

## Recover a residual allocation

When a required definition, trusted configuration, recorded directory,
credential, environment, or reliable target identity is unavailable,
automatic reconstruction stops and reports operator recovery required. A
lifecycle-configuration warning alone does not have this outcome. Do not create
an empty directory or infer release from current configuration: neither proves
that a process or external allocation is gone.

1. Inspect the retained execution record, its unavailable reason, and the
   dependency-blocked executions.
2. Release the residual allocation through its external owner, following the
   retained plan's release order.
3. After the owner-approved acknowledgement operation is available, use
   `plect execution acknowledge-release --session <name> --execution <ulid>
   --reason <text>` for that exact execution. It records an auditable operator
   assertion, not successful cleanup, and does not release any other
   allocation. Until then, preserve the unresolved record or use the explicit
   discard in the next step.
4. Retry ordinary `up` after every applicable retained obligation is resolved;
   it reconstructs in the latest desired workflow's setup dependency order.

`--force-recreate` never substitutes for confirmation or discard, and recovery
never runs cleanup in another directory. `plect destroy --force` is the only
explicit discard: it records that release was not verified and removes the
session's remaining execution records.

## Roll back

To recover, stop the new release, restore the configuration and state copies,
and restart the previous release. Do not mix old configuration with migrated
state or migrated configuration with old state.
