# The Slack plugin's thread workspace provider is `thread_workspace`; `thread` is now the resource observer

`official/slack` declared its workspace provider as `thread`. The plugin now
also ships a `thread` resource observer (its `query.subscribe` face is the
workflow population source for an unbound Slack mention). A resource name
belongs to the resource itself, so the workspace provider — which names what
it provides, the same way the GitHub plugin's workspace provider is
`worktree` rather than `github` — is renamed to `thread_workspace`, freeing
`thread` for the resource observer. The workspace provider's file moved from
`workspaces/thread.toml` to `workspaces/thread_workspace.toml`; its `match`,
`setup`, and `cleanup` are otherwise unchanged. The resource observer's file
is `resources/thread.toml`.

## Backup

```bash
CONFIG_HOME="${PLECT_CONFIG_HOME:-$HOME/.config/plect}"
STAMP="$(date -u +%Y%m%dT%H%M%SZ)"
cp -r "$CONFIG_HOME" "$CONFIG_HOME.migration-backup.$STAMP"
```

No session state changes, so `state.json` needs no backup for this rename —
see "Nothing else moves" below.

## Rewrite the reference

A workflow that names the workspace provider takes the new address:

```toml
[my_ops_workflow]
kind               = "workflow"
workspace_provider = "official.slack.thread_workspace"   # was "official.slack.thread"
```

Find every reference:

```bash
CONFIG_HOME="${PLECT_CONFIG_HOME:-$HOME/.config/plect}"
grep -rn "workspace_provider *= *['\"]" "$CONFIG_HOME"
```

TOML accepts either quote, so the pattern matches both. A hit naming your
own provider stays as it is; only one selecting the Slack plugin's thread
provider changes — that is, one whose value is the plugin's address under
whatever alias you registered it (`official.slack.thread` for the
`official` alias, under the pre-rename id). A reference that still names the
old id resolves to nothing and says which address it meant, so
`plect workflow list` will name any you miss.

A workflow population entry that wants the new resource observer's
subscribe-only query face uses the freed address directly — there is
nothing to rewrite for it, since no prior config could have referenced it:

```toml
[[my_ops_workflow.populations]]
resource_observer = "official.slack.thread"
expire_after       = "8h"
```

## Nothing else moves

- **No session state.** A workspace provider id is not a key anything is
  stored under, and neither is a resource observer id: both are load-time
  static references. No session's tasks, outputs, or workspace paths are
  affected by this rename.
- **Session naming is unchanged.** The provider's `name` expression still
  derives `slack/<channel_id>-<root thread_ts digits>` from the permalink;
  only the definition id that hosts that expression moved.
- **Every invocation is unchanged.** `slack-adapter`'s HTTP API and CLI
  subcommands (`subscribe unbound-mentions`, `resource observe`) keep their
  existing names and flags; only the config tables that bind them moved.
