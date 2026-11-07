# Resource and workspace effects migration

This procedure moves a configuration tree to the resource-entry and ordinary
environment-effect dialect. It is a breaking migration: `workspace_provider`
and `resource_observer` have no aliases, provider output records have no
compatibility reader, and a session is never reconfigured from its current
directory.

Read the whole procedure before making changes. Perform it with the release
that removes the workspace provider. The loader refuses each retired spelling
with a diagnostic that points here: a `kind = "workspace_provider"`
declaration, a workflow's `workspace_provider` or `workspace_provider_inputs`
field, a population's `resource_observer`, and `workspace_dirs_root` in
`config.toml`.

## Backup

Stop resident processes before copying their configuration and durable state.
The backup must include the global tree, every selected project tree, and the
state directory because existing sessions need a recorded context and migrated
node/output records.

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

## Release live sessions first

A session whose environment was acquired by a workspace provider owns an
allocation only the previous release knows how to free: its worktree,
conversation directory, or scratch directory. The new release's storage
migration therefore refuses to run while any session that has not been
destroyed still holds an unreleased `@workflow` record, and names this
procedure. With the previous release still installed, `plect destroy` each
such session (add `--force` only to discard a directory you have already
saved), then upgrade. The migration removes the remaining `@workflow` records
and the stored workspace directory column; it cannot reconstruct a directory
from them, because a workflow's work directory is now evaluated from the
workflow's `workdir` and never stored.

## Convert declarations and workflows

1. Rewrite every `kind = "resource_observer"` as `kind = "resource"`, adding
   `name`, delivery actions, and the resource action input schema where the
   former provider owned them.
2. Rewrite every `workspace_provider` as a resource declaration plus one or
   more ordinary `scope = "session"` effects. Move worktree, Slack-thread,
   and local environment setup, cleanup, liveness, and outputs to those
   effects. Preserve cleanup inputs such as `force` and plugin-owned options.
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
5. Move a destruction option a provider took (such as a branch to delete) to
   the effect's `[<id>.cleanup.inputs_schema]`, and read it in cleanup as
   `cleanup.inputs.<key>`; `plect destroy --input key=value` supplies it, and
   an input no released effect declares is an error. `force` is a cleanup root
   of every effect.
6. A direct invocation reads the `.plect/` overlay of its own directory or of
   an ancestor directory only when that directory is listed in `trusted_project_roots` in the
   machine configuration; nothing is trusted implicitly, because an overlay can
   declare effects that run shell. List each project directory whose overlay you
   rely on, as an absolute path. A session records the directory it was created
   in and later operations such as `workflow rebase` expand from that record, so
   a session created before this release has no record and expands from the
   plugin and global layers alone.
7. Move a workflow's initial task selection to each invocation, chain, or
   population. Ensure each task's concrete resource matches the task document.
8. Delete `resource_observer` from every `[[<workflow>.populations]]` entry.
   A population queries its workflow's entry `resource` and validates `query`
   against that resource's query `inputs_schema`; the loader refuses the
   retired field with a diagnostic that points here. A population whose
   `resource_observer` named a different resource than its workflow's
   `resource` moves to a workflow whose entry resource is that resource.

## Establish project contexts

For every intended project configuration, choose one project root, consolidate
only that root's intended `.plect/` definitions, and add this marker:

```toml
schema_version = 3
```

It contains no definitions or other fields and declares only the selected
project layer's dialect. Add the root's canonical path to
`trusted_project_roots` in the machine configuration. Do not preserve ancestor
fragments or copy configuration from a generated checkout.

Before upgrading, release every unreleased allocation with the pre-upgrade
binary where that is possible. Do not copy cleanup code or plugin files into
durable state to make the later release run.

Migrate existing sessions to their recorded project root, initial layer
revisions, and node execution records before running lifecycle operations. A
legacy session starts with no lifecycle-configuration baseline. Its first `up`,
`down`, or `destroy` execution records the current trusted lifecycle
configuration without a change warning; no historical digest or plugin hash is
required for cleanup.

Preserve every concrete execution fact already recorded. Do not manufacture a
missing declaration identity, cwd, setup input, output, nested-layer fact,
trust decision, or allocation identity from today's workflow. An old session
whose context cannot be reconstructed remains stopped without caller-cwd
fallback. A retained declaration that uses a retired spelling and no longer
resolves has the named precondition failure `legacy declaration spelling is
unresolved`; migration does not rewrite it from current workflow declarations.

## Verify and recover

Run the release's configuration validation and inspect every workflow that has
agent actions, every population, each resource action input contract, and both
normal cleanup and explicit force-discard paths. Test a lifecycle-configuration
edit: it warns before the next execution and uses the current trusted
configuration. Test a vanished work directory: cleanup must fail its
precondition without changing directory. Keep the timestamped copies until
sessions have been recreated and the migrated state has been observed in normal
use.

## Recover a residual allocation

When a required definition, trusted configuration, recorded directory, cleanup
input, credential, environment, or reliable target identity is unavailable,
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

To recover, stop the new release, restore the configuration and state copies,
and restart the previous release. Do not mix old configuration with migrated
state or migrated configuration with old state.
