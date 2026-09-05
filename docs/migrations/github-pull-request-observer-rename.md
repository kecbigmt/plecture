# The GitHub plugin's pull request resource observer is `pull_request`

`official/github` declared its pull request resource observer as `pull`, an
abbreviation that reads ambiguously next to `issue`. It is now `pull_request`,
matching the resource it observes. The file moved from
`resources/pull.toml` to `resources/pull_request.toml`; its `observe` action,
`query` face, and `state_schema` are otherwise unchanged.

## Backup

```bash
CONFIG_HOME="${PLECT_CONFIG_HOME:-$HOME/.config/plect}"
STAMP="$(date -u +%Y%m%dT%H%M%SZ)"
cp -r "$CONFIG_HOME" "$CONFIG_HOME.migration-backup.$STAMP"
```

No session state changes, so `state.json` needs no backup for this rename —
see "Nothing else moves" below.

## Rewrite the reference

A task document written for the pull request observer takes the new address:

```toml
[my_review_task]
kind              = "task"
resource_observer = "official.github.pull_request"   # was "official.github.pull"
```

A workflow population entry takes the same new address:

```toml
[[my_workflow.populations]]
resource_observer = "official.github.pull_request"   # was "official.github.pull"
```

Find every reference:

```bash
CONFIG_HOME="${PLECT_CONFIG_HOME:-$HOME/.config/plect}"
grep -rn "resource_observer *= *['\"]" "$CONFIG_HOME"
```

TOML accepts either quote, so the pattern matches both. A hit naming your
own observer stays as it is; only one selecting the GitHub plugin's pull
request observer changes — that is, one whose value is the plugin's address
under whatever alias you registered it (`official.github.pull` for the
`official` alias). A reference that still names the old id resolves to
nothing and says which address it meant, so `plect workflow show` on the
affected workflow will name any you miss.

## Nothing else moves

- **No session state.** `resource_observer` is a load-time static
  reference; it is not a key anything is stored under, so no session's
  tasks or outputs are affected.
- **`resource.state.resource_kind` is unchanged.** The observed fact stays
  the string `"pull"` — that value identifies the resource's kind
  independent of which definition id observes it, the same way
  `resources/issue.toml`'s `resource_kind` stays `"issue"`.
- **Every invocation is unchanged.** `github-issue-pr`'s `observe` and
  `query-pulls` subcommands, and `github-webhook-receiver`'s
  `subscribe-pulls` subcommand, keep their existing names and flags; only
  the config table that binds them moved.
