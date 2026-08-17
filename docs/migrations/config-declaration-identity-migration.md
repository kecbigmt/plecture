# Config declaration identity migration

This migration covers the configuration language change decided in
`docs/adr/2026-08-17-config-declaration-identity.md`: TOML configuration
definitions are declared by top-level `[<id>]` tables with a required `kind`
field, directory layout is author organization, and definition ids are unique
across TOML definition kinds in a layer.

The change is intentionally breaking. Plecture is pre-1.0, so operators migrate
their own config once instead of relying on a compatibility shim that reads both
placement-as-kind and table-declared layouts.

## Backup

Stop any running resident `plect serve` process before backing up runtime
state, using the service manager that starts it. The resident process writes
`state.json`; stopping it avoids copying a half-written snapshot. Keep it
stopped until any state rewrite in this migration is complete.

Back up global config, runtime state, and any trusted repo overlays before
editing:

```bash
CONFIG_HOME="${PLECT_CONFIG_HOME:-${XDG_CONFIG_HOME:-$HOME/.config}/plect}"
DATA_DIR="${XDG_DATA_HOME:-$HOME/.local/share}/plect"
STAMP="$(date -u +%Y%m%dT%H%M%SZ)"
BACKUP_DIR="$CONFIG_HOME.migration-backup.$STAMP"

mkdir -p "$BACKUP_DIR"
cp -r "$CONFIG_HOME" "$BACKUP_DIR/config-home"

if [ -f "$DATA_DIR/state.json" ]; then
  mkdir -p "$BACKUP_DIR/state"
  cp "$DATA_DIR/state.json" "$BACKUP_DIR/state/state.json"
fi

find . -type d -name .plect -prune -print | while read -r OVERLAY; do
  cp -r "$OVERLAY" "$OVERLAY.migration-backup.$STAMP"
done
```

If you use hand-authored plugin directories outside a catalog, back up each
plugin directory too. Runtime state stays under the XDG data dir even when
`--config-home` points at a different config tree.

## Add Definition Tables

For every TOML definition under `workspaces/`, `resources/`, `environments/`,
`channels/`, `tasks/`, and `workflows/`, move the definition body under a
top-level `[<id>]` table and add the required `kind` field.

Use this mapping from the old directory to the new `kind` value:

| Old directory | `kind` value |
|---|---|
| `workspaces/` | `workspace_provider` |
| `resources/` | `resource_observer` |
| `environments/` | `environment` |
| `channels/` | `channel` |
| `tasks/` | `task` |
| `workflows/` | `workflow` |

The initial id is the old filename stem. Rename ids that repeat a provider name,
collide across kinds in the same plugin or user-owned layer, or do not match
`^[A-Za-z_][A-Za-z0-9_]*$`.

Example before:

```toml
scope = "run"
setup = "agent-runtime launch"

[outputs_schema]
type = "object"
```

Example after:

```toml
[runtime]
kind = "task"
scope = "run"
setup = "agent-runtime launch"

[runtime.outputs_schema]
type = "object"
```

Array tables move under the definition table too:

```toml
[review_session]
kind = "workflow"
workspace_provider = "official.github.worktree"

[[review_session.nodes]]
uses = "official.claude.runtime"

[[review_session.event.channel]]
uses = "official.claude.structured_delivery"
```

## Update References

Reference values no longer include a kind segment. The reference site declares
the expected kind, and the loader checks the resolved target's `kind`.

Use relative references within the same user layer or inside one plugin:

```toml
[review_session]
kind = "workflow"
workspace_provider = "worktree"

[[review_session.nodes]]
uses = "runtime"

[[review_session.event.channel]]
uses = "structured_delivery"

[[pursue_goal.chains]]
workflow = "goal_review_session"
```

Use catalog-qualified references from user-owned config to catalog content:

```toml
[review_session]
kind = "workflow"
workspace_provider = "official.github.worktree"

[[review_session.nodes]]
uses = "official.claude.runtime"

[[review_session.event.channel]]
uses = "official.claude.structured_delivery"
```

There is no alias-optional form in stored config. Replace scaffold examples
such as `claude.runtime` with the catalog alias actually used in
`catalogs.toml`, such as `official.claude.runtime`.

If a workflow node omits `id`, its node id defaults to the referenced task id.
For `uses = "official.claude.runtime"`, the default node id is `runtime`. Add
explicit node ids when two nodes would otherwise default to the same id.

Catalog aliases and plugin path segments must be non-empty dot-free segments
matching `^[A-Za-z0-9][A-Za-z0-9_-]*$`, so dotted references can be parsed
without ambiguity and leading hyphens do not look like CLI flags. Hyphens are
valid after the first character in aliases and plugin path segments.

## Update Shipped Plugin References

If user-owned workflows, tasks, or channel bindings reference shipped plugin
ids, update them to the responsibility names:

| Plugin | Old id | Kind | New id |
|---|---|---|---|
| `tmux` | `tmux` | task | `terminal` |
| `claude` | `claude` | task | `runtime` |
| `claude` | `claude_initial_prompt` | task | `initial_prompt` |
| `claude` | `claude` | channel | `structured_delivery` |
| `codex` | `codex` | task | `tui_runtime` |
| `codex` | `codex_exec` | task | `exec_runtime` |
| `codex` | `codex_initial_prompt` | task | `initial_prompt` |
| `codex` | `codex_exec` | channel | `exec_delivery` |
| `github` | `github` | workspace_provider | `worktree` |
| `github` | `github` | resource_observer | `issue_pr` |
| `slack` | `slack` | channel | `thread_messages` |
| `okf` | `local-okf` | workspace_provider | `concept_workspace` |
| `okf` | `okf_goal` | resource_observer | `goal` |
| `okf` | `goal_review` | workflow | `goal_review_session` |
| `okf` | `goal_review` | task | `record_goal_verdict` |
| `okf` | `goal_bootstrap` | task | `bootstrap_goals` |

The `github` task ids `work`, `review`, `respond`, `investigate`, and
`gh_guard` do not change. The `codex` channel id `terminal_submit` does not
change. The `okf` task id `pursue_goal` does not change.

## Update Frozen Workflow State

The new definition id syntax invalidates workflow ids that contain dots or
hyphens. Existing sessions freeze their workflow id in runtime `state.json`.
Before renaming a workflow id, find sessions and done-when judge records that
refer to the old id:

```bash
DATA_DIR="${XDG_DATA_HOME:-$HOME/.local/share}/plect"
STATE="$DATA_DIR/state.json"
OLD_WORKFLOW="coding-claude"

jq -r --arg old "$OLD_WORKFLOW" '
  .sessions // {}
  | to_entries[]
  | select(.value.workflow == $old)
  | .key
' "$STATE"

jq -r --arg old "$OLD_WORKFLOW" '
  .. | objects | select(.reviewer_workflow? == $old) | .reviewer_workflow
' "$STATE"
```

For each renamed workflow id, either destroy and recreate affected sessions with
the new workflow id, or rewrite `state.json` after taking the backup above.
Stop any running resident `plect serve` process before rewriting the state file,
and restart it after the rewrite. The resident process owns state writes while
it is running and can otherwise clobber or race a hand edit.

Destroying and recreating is the normal remedy when a session's frozen workflow
does not match the requested workflow. Rewriting preserves the session record:

```bash
OLD_WORKFLOW="coding-claude"
NEW_WORKFLOW="coding_claude"
TMP="$(mktemp)"

jq --arg old "$OLD_WORKFLOW" --arg new "$NEW_WORKFLOW" '
  (.sessions[]? | select(.workflow == $old) | .workflow) = $new
  | (.. | objects | select(.reviewer_workflow? == $old) | .reviewer_workflow) = $new
' "$STATE" > "$TMP"

mv "$TMP" "$STATE"
```

Check both session `workflow` fields and done-when `reviewer_workflow` fields
because reviewer workflow names are stamped in state at record time.

## Convert Same-Id Plugin Overrides

Same-id user-owned definitions no longer shadow plugin definitions, and
same-id user-owned workflow fragments no longer append to plugin workflows.
Before updating references, compare trusted user config and trusted repo
overlays against the shipped plugin ids listed above and the keep-their-id list.

For a same-id user-owned task, workspace provider, resource observer,
environment, or channel that previously replaced a plugin definition:

1. Keep or rename the user-owned definition with a valid `[<id>]` table and
   required `kind`.
2. Update user-owned workflows, tasks, or channel bindings that should use it to
   the relative reference, such as `runtime`.
3. Update references that should continue to use the plugin definition to the
   catalog-qualified reference, such as `official.claude.runtime`.

For a same-id workflow fragment that previously added nodes or event channels to
a plugin workflow:

1. Copy the plugin workflow into a trusted user-owned config layer under a
   `[<id>]` table with `kind = "workflow"`, or choose a new user-owned workflow
   id.
2. Manually merge the local nodes and event channels into that user-owned
   workflow.
3. Use catalog-qualified references for plugin tasks, channels, resource
   observers, or workspace providers that the workflow still composes.
4. Update user-owned entrypoints and references to the relative workflow
   address, such as `review_session`.

Remove the old fragment after the replacement workflow loads. A cloned
workspace-dir `.plect/workflows/` fragment also gets its identity from the
`[<id>]` table with `kind = "workflow"`; the filename stem is no longer the
workflow id.

## Choose Layout

After adding definition tables, directory layout is optional organization. You
may keep kind directories:

```text
config/tasks/runtime.toml
config/channels/structured_delivery.toml
```

Or group by feature:

```text
config/review/runtime.toml
config/review/structured_delivery.toml
config/review/session.toml
```

The loader reads the definition table and required `kind`; it does not infer
kind or id from either layout.

Resource-observation implementation names and directories follow the same
breaking migration. Existing `resources/` config directories migrate to
`kind = "resource_observer"` definition tables, and follow-up Go identifier and
directory renames ride this migration rather than adding compatibility aliases.

## Verification

Confirm that every definition loads and that references resolve to the expected
kind:

```bash
plect plugin verify
plect workflow show <workflow-id>
```

For a workflow that you normally run, verify the plan before starting work:

```bash
plect up --dry-run <session>
```

If a load error reports a same-id conflict, rename one definition id and update
references to it. If a load error reports a kind mismatch, update the referenced
definition or point the reference at a definition whose `kind` matches the
reference site.

## Rollback

Restore the backed-up config and use a plect binary built before this change:

```bash
CONFIG_HOME="${PLECT_CONFIG_HOME:-${XDG_CONFIG_HOME:-$HOME/.config}/plect}"
DATA_DIR="${XDG_DATA_HOME:-$HOME/.local/share}/plect"
BACKUP_DIR="$CONFIG_HOME.migration-backup.$STAMP"

rm -rf "$CONFIG_HOME"
mv "$BACKUP_DIR/config-home" "$CONFIG_HOME"

if [ -f "$BACKUP_DIR/state/state.json" ]; then
  mkdir -p "$DATA_DIR"
  cp "$BACKUP_DIR/state/state.json" "$DATA_DIR/state.json"
fi
```

Restore any backed-up `.plect` overlay or hand-authored plugin directory the
same way.
