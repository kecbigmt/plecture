# GitHub issue/pull observer split migration

The GitHub plugin's one resource definition became two. `resources/github.toml`
is gone; `resources/issue.toml` and `resources/pull.toml` take its place, each
recognizing only its own kind of identifier and typing only the facts that kind
actually has.

The change is intentionally breaking. Plecture is pre-1.0, so the definition
migrates once instead of a union definition being kept alive beside the split
one — two definitions whose `match` both claim a GitHub URL is a load error, so
a compatibility shim could not coexist with the result anyway.

Take a backup of the config directory before starting:

```bash
cp -a ~/.config/plect ~/.config/plect.bak
```

## What changed

| Before | After |
|---|---|
| One definition `github`, matching `/issues/<n>` and `/pull/<n>` | `issue` matching `/issues/<n>`, `pull` matching `/pull/<n>` |
| One `state_schema` whose halves are `"NULL"` by turns | One `state_schema` per kind, over the keys that kind observes |
| `resource_kind` enum `pull` / `issue` / `unknown` | `resource_kind` enum `issue` on one, `pull` on the other |
| A pull request observation reports `issue_status = "NULL"` | A pull request observation does not report `issue_status` |
| An identifier that is neither exits 0 with an all-`NULL` document | The observation fails |

An issue's `checks_status`, `revision`, `pr_url`, `mergeable_state`, and
`review_decision` still report its linked pull request exactly as before —
`resources/issue.toml`'s `state_schema` now says so in the contract rather than
only in a comment. Nothing about how a linked pull request is discovered
changed.

## Steps

### 1. Remove a user-owned override of the old definition

Only if `~/.config/plect/resources/github.toml` exists. It declares `github`,
which still matches both kinds, so leaving it in place makes every GitHub URL
match three definitions and fail to resolve:

```bash
plect resource status https://github.com/<owner>/<repo>/issues/1
# resource id "..." matches more than one resource definition: [github issue]
```

Split it the same way the plugin's is — one file per kind, matching
`/issues/<n>` and `/pull/<n>` separately — or delete it if it was only
overriding what the plugin already ships:

```bash
rm ~/.config/plect/resources/github.toml
```

### 2. Update anything reading the definition id

`plect resource status --json` reports `.definition`, which was `"github"` and
is now `"issue"` or `"pull"`. A script branching on it needs both names.

### 3. Clear the stale `issue_status` on pull-keyed instances

A pull-keyed instance keeps whatever `issue_status` it last observed, because
an output that is no longer produced retains its prior value. No shipped
`done_when` reads it, so nothing is gated on the stale value; it shows up only
in `plect status` output. Clear it per instance if the display matters:

```bash
plect state set-output <session> --task <task-handle> '{"issue_status":""}'
```

## Verification

```bash
plect resource status https://github.com/<owner>/<repo>/issues/<n> --json | jq '.definition, .state'
plect resource status https://github.com/<owner>/<repo>/pull/<n> --json | jq '.definition, .state'
```

The first reports `"issue"` with an `issue_status`; the second reports `"pull"`
without one. Both report the same `checks_status`, `revision`, and
`mergeable_state` they did before the split.
