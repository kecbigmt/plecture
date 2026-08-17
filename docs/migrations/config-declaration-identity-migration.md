# Config declaration identity migration

This migration covers the configuration language change decided in
`docs/adr/2026-08-17-config-declaration-identity.md`: TOML configuration
definitions are declared by `[<kind>.<id>]` headers, directory layout is author
organization, and definition ids are unique across TOML definition kinds in a
layer.

The change is intentionally breaking. Plecture is pre-1.0, so operators migrate
their own config once instead of relying on a compatibility shim that reads both
placement-as-kind and header-declared layouts.

## Backup

Back up global config and any trusted repo overlays before editing:

```bash
CONFIG_HOME="${PLECT_CONFIG_HOME:-${XDG_CONFIG_HOME:-$HOME/.config}/plect}"
STAMP="$(date -u +%Y%m%dT%H%M%SZ)"

cp -r "$CONFIG_HOME" "$CONFIG_HOME.migration-backup.$STAMP"

find . -type d -name .plect -prune -print | while read -r OVERLAY; do
  cp -r "$OVERLAY" "$OVERLAY.migration-backup.$STAMP"
done
```

If you use hand-authored plugin directories outside a catalog, back up each
plugin directory too.

## Add Definition Headers

For every TOML definition under `workspaces/`, `resources/`, `environments/`,
`channels/`, `tasks/`, and `workflows/`, move the definition body under a
`[<kind>.<id>]` header.

Use this mapping from the old directory to the new header kind:

| Old directory | Header kind |
|---|---|
| `workspaces/` | `workspace` |
| `resources/` | `resource` |
| `environments/` | `environment` |
| `channels/` | `channel` |
| `tasks/` | `task` |
| `workflows/` | `workflow` |

The initial id is the old filename stem. Rename ids that repeat a provider name,
collide across kinds in the same plugin or user-owned layer, or do not match
`^[A-Za-z][A-Za-z0-9_-]*$`.

Example before:

```toml
scope = "run"
setup = "agent-runtime launch"

[outputs_schema]
type = "object"
```

Example after:

```toml
[task.runtime]
scope = "run"
setup = "agent-runtime launch"

[task.runtime.outputs_schema]
type = "object"
```

Array tables move under the definition header too:

```toml
[[workflow.review_session.nodes]]
uses = "official.claude.task.runtime"

[[workflow.review_session.event.channel]]
uses = "official.claude.channel.structured_delivery"
```

## Update References

Reference values now include a kind segment.

Use relative references within the same user layer or inside one plugin:

```toml
[workflow.review_session]
workspace_provider = "workspace.worktree"

[[workflow.review_session.nodes]]
uses = "task.runtime"

[[workflow.review_session.event.channel]]
uses = "channel.structured_delivery"

[[task.pursue_goal.chains]]
workflow = "workflow.goal_review_session"
```

Use catalog-qualified references from user-owned config to catalog content:

```toml
[workflow.review_session]
workspace_provider = "official.github.workspace.worktree"

[[workflow.review_session.nodes]]
uses = "official.claude.task.runtime"

[[workflow.review_session.event.channel]]
uses = "official.claude.channel.structured_delivery"
```

There is no alias-optional form in stored config. Replace scaffold examples
such as `claude.task.runtime` with the catalog alias actually used in
`catalogs.toml`, such as `official.claude.task.runtime`.

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
| `github` | `github` | workspace | `worktree` |
| `github` | `github` | resource | `issue_pr` |
| `slack` | `slack` | channel | `thread_messages` |
| `okf` | `local-okf` | workspace | `concept_workspace` |
| `okf` | `okf_goal` | resource | `goal` |
| `okf` | `goal_review` | workflow | `goal_review_session` |
| `okf` | `goal_review` | task | `record_goal_verdict` |
| `okf` | `goal_bootstrap` | task | `bootstrap_goals` |

The `github` task ids `work`, `review`, `respond`, `investigate`, and
`gh_guard` do not change. The `codex` channel id `terminal_submit` does not
change. The `okf` task id `pursue_goal` does not change.

## Choose Layout

After adding definition headers, directory layout is optional organization. You
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

The loader reads the definition header; it does not infer kind or id from either
layout.

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
references to it. If a load error reports a kind mismatch, update the reference
kind or the referenced definition id so the site, reference, and target agree.

## Rollback

Restore the backed-up config and use a plect binary built before this change:

```bash
rm -rf "$CONFIG_HOME"
mv "$CONFIG_HOME.migration-backup.$STAMP" "$CONFIG_HOME"
```

Restore any backed-up `.plect` overlay or hand-authored plugin directory the
same way.
