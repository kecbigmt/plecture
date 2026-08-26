# GitHub Projects v2 item id (`PVTI_...`) resource removal

`official.github.worktree`'s setup (`github-worktree setup --resource`) no
longer accepts a GitHub Projects v2 item id (`PVTI_...`) as a resource
identifier. `ResolveProjectItemID` and `IsProjectItemID` — the direct,
ambient-`gh`-authenticated `gh api graphql` lookup that turned a `PVTI_...`
id into the issue/pull request URL it pointed at — are removed outright, not
deprecated. Only an issue or pull request URL is accepted now.

Decided by owner ruling on [audit #335](https://github.com/kecbigmt/plecture/issues/335)
(tracked as part of [#334](https://github.com/kecbigmt/plecture/issues/334)):
the plugin's shipped `[worktree]` provider declaration
(`plugins/github/config/workspaces/worktree.toml`) matches only an issue or
pull request URL, so no `plect up`/`plect subscribe` dispatch through the
shipped catalog could ever reach the `PVTI_...` path. It was reachable only
by a **custom** `workspace_provider` `match` pattern that captured
`PVTI_...` and still routed to this same provider, or by invoking the
`github-worktree` binary directly with `--resource PVTI_...`.

The change is intentionally breaking. Plecture is pre-1.0, so an operator on
either of those two paths migrates once instead of relying on a compatibility
shim; a remaining `PVTI_...` resource now fails at parse with an
"invalid GitHub URL" error rather than resolving through GraphQL.

## Backup

Before editing anything, back up session state and any custom workspace
provider configuration you maintain:

```bash
DATA_HOME="${XDG_DATA_HOME:-$HOME/.local/share}/plect"
CONFIG_HOME="${PLECT_CONFIG_HOME:-$HOME/.config/plect}"
STAMP="$(date -u +%Y%m%dT%H%M%SZ)"
[ -f "$DATA_HOME/state.json" ] && cp "$DATA_HOME/state.json" "$DATA_HOME/state.json.migration-backup.$STAMP"
[ -d "$CONFIG_HOME" ] && cp -a "$CONFIG_HOME" "$CONFIG_HOME.migration-backup.$STAMP"
```

## Find affected configuration and sessions

A custom `match` pattern that captures a `PVTI_...` id and routes to
`official.github.worktree` (or a workflow that inherited one):

```bash
CONFIG_HOME="${PLECT_CONFIG_HOME:-$HOME/.config/plect}"
grep -rn "PVTI" "$CONFIG_HOME" .plect 2>/dev/null
```

A live session created from a `PVTI_...` resource id — its `resource_id`
stores exactly the string that matched the provider's `match` pattern at
creation time, unresolved, regardless of what setup's own outputs later
resolved it to:

```bash
DATA_HOME="${XDG_DATA_HOME:-$HOME/.local/share}/plect"
jq -r '.sessions[] | select(.resource_id // "" | startswith("PVTI_")) | .session_name' "$DATA_HOME/state.json" 2>/dev/null
```

No hits from either command means this removal has no effect on your
deployment — this is the expected outcome for a host running only the
shipped catalog, per the audit's own finding above.

## Rewrite a custom `match` pattern

Resolve the `PVTI_...` id to its issue or pull request URL yourself (the
project-management tool that produced the id, or a one-off
`gh api graphql` call) and pass that URL to `plect up` / `plect subscribe`
instead. There is no in-repository replacement for the GraphQL lookup this
removal deletes — resolving a project item id is now entirely the caller's
responsibility, matching how every other resource identifier this provider
accepts is already a URL its caller supplies.

## Verify

Re-run both commands from [Find affected configuration and sessions](#find-affected-configuration-and-sessions).
No hits from either is the completion condition.
