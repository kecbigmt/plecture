# Shipped completion conversion

Every shipped completion declaration is now a task document. The github
plugin's `work`, `investigate`, `respond`, and `review`, and the okf plugin's
`pursue_goal` and `goal_review`, moved from `tasks/<id>.toml` effects to
`tasks/<id>.md` documents, and their instruction templates moved into the
documents' bodies. With them went the accommodations the task-document surface
carried while the two coexisted:

- an effect declaration no longer accepts `done_when`, `requires`,
  `[[outputs]]`, or `[[chains]]`;
- an unrooted completion key no longer falls back to an instance's own facts —
  every key names `resource.state.*` or `self.state.*`;
- a chain's `workflow` is a static reference and its inputs are values over
  the chain-input roots, so the `.Work.*` template vocabulary is gone.

Three consequences follow from an effect answering for no completion:

- **Dynamic outputs are gone.** `[[outputs]]` — the `script` and
  `from_resource_status` forms — existed to feed a completion check a live
  value. A document reads a live value from the observer that publishes it, so
  there is nothing left for the mechanism to feed. `plect status --refresh`
  now observes each resource and nothing else, and `plect tick` does the same.
  A value your own config produced with a `script` output becomes either an
  observer's `state_schema` key or a fact recorded with `plect state set`.
- **A nesting layer adds no completion.** An outer effect no longer composes a
  `done_when` over its inner layers, and per-layer budgets go with it: a
  document has one predicate and one budget.
- **A session owes progress when a task document says so.** The watchdog's
  stall accusation used to read a run-scoped effect's `done_when`; it reads
  the session's live task-document instances instead. An activity probe can
  still pardon the silence, exactly as before.

Read `docs/migrations/task-document-surface-migration.md` first: it is the
procedure for converting one declaration. This document is what *else* has to
change, because the shipped side converted.

## Backup

```bash
CONFIG_HOME="${PLECT_CONFIG_HOME:-$HOME/.config/plect}"
STAMP="$(date -u +%Y%m%dT%H%M%SZ)"
cp -r "$CONFIG_HOME" "$CONFIG_HOME.migration-backup.$STAMP"
cp -r "${XDG_STATE_HOME:-$HOME/.local/state}/plect" \
      "${XDG_STATE_HOME:-$HOME/.local/state}/plect.migration-backup.$STAMP"
```

The session store is backed up too, because one key moves inside it (below).

## Update the catalog

```bash
plect plugin update
```

## Each shipped document is written for one resource kind

A task document declares one `resource_observer`, and instantiation refuses a
resource that observer does not claim. The shipped four split by what their
instruction is about:

| Document | `resource_observer` | Instantiate against |
|---|---|---|
| `work` | `issue` | the issue |
| `investigate` | `issue` | the issue |
| `respond` | `pull` | the pull request |
| `review` | `pull` | the pull request |

Consequences to check in your own config:

- A pull-keyed session can no longer instantiate `work` or `investigate`, and
  an issue-keyed session can no longer instantiate `respond` or `review`.
  Where a workflow's `task` input enumerates all four, the value has to match
  the resource the session is keyed to.
- `issue_status` is an issue observation's alone. `respond` used to copy it as
  an output; it does not, and no gate read it.
- Nothing else moved: every other fact a shipped gate reads is published by
  the observer the document now declares. `plugins/github/testdata/gate-keys.txt`
  and `plugins/okf/testdata/gate-keys.txt` are the full mapping, one line per
  fact, regenerated from the shipped declarations.

## A chain-spawned reviewer needs the pull request named

A chain spawns its reviewer against the *declaring* session's resource, which
for issue-keyed work is the issue. `review` is written for the pull request,
so the reviewer's own `review` instance has to be created against the pull
request explicitly. The chain already wires it as `pr_url`, so a host
dispatcher reads it from the session inputs:

```bash
# tasks/initial_task.toml, in the branch that instantiates the review task
PR_URL='{{get .SessionInputs "pr_url" ""}}'
RESOURCE='{{.ResourceID}}'
case "$TASK" in
  review) [ -n "$PR_URL" ] && RESOURCE="$PR_URL" ;;
esac
plect task setup "$TASK" --name initial --resource "$RESOURCE" --session '{{.SessionName}}'
```

Which resource a chain-spawned session binds to is core behavior with no
declaration today; that language addition is tracked on the chain-design
issue, and this explicit instantiation is what stands in until it exists.

## `verdict_revision` moves from outputs to state

This is the one persisted-state edit. A live `review` or `goal_review`
instance recorded its verdict as a *mutable output*; the converted document
holds it as *state*, and a document's `self.state.*` is what was recorded into
the instance and nothing else — an output left behind is not read as recorded
state, deliberately, so a stale value cannot satisfy a predicate by accident.

Move it for every live instance, with the session store backed up as above:

```bash
for session in $(plect ls --json | jq -r '.[].session_name'); do
  plect status "$session" --json | jq -r '
    .work[]? | select(.outputs.verdict_revision != null)
    | "\(.instance)\t\(.outputs.verdict_revision)"' |
  while IFS="$(printf '\t')" read -r instance revision; do
    plect state set "$session" --instance "$instance" \
      "$(jq -nc --arg r "$revision" '{verdict_revision:$r}')"
  done
done
```

An instance whose verdict is not moved simply reads as "no verdict yet": its
review reopens and the reviewer records a fresh one. Nothing is lost beyond a
re-review, so skipping this step is a valid choice for a small tree.

`verdict_current` has no successor to move to. It was a script comparing two
facts, and it is now the comparison itself:

```toml
{ expr = "self.state.verdict_revision == resource.state.revision" }
```

## Your own declarations that wrapped a shipped one

### A nesting layer over a completion task

A task document owns no nesting joint, so a host effect that wrapped a shipped
completion declaration with `inner = "official/github/work"` has nothing left
to nest. Replace it with your own `tasks/work.md` task document: a document in
a deeper layer replaces the shipped one by id, so copy the shipped document
and edit it, rather than layering over it.

**Delete the wrapper first.** An id names one declaration, so a layer holding
both `tasks/work.toml` and `tasks/work.md` fails to load — and while the
wrapper is still there, `plect task show work` reports the wrapper's own path
rather than the shipped document's:

```bash
CONFIG_HOME="${PLECT_CONFIG_HOME:-$HOME/.config/plect}"
rm "$CONFIG_HOME/tasks/work.toml"
cp "$(plect task show work --json | jq -r .source_path)" "$CONFIG_HOME/tasks/work.md"
```

**Qualify every reference the copy carries.** A plugin's own reference is
relative and resolves in that plugin's namespace; the same text in user-owned
config is a load error, because a user-owned reference to catalog content
carries its catalog alias. So `resource_observer = "issue"` becomes
`resource_observer = "official.github.issue"`, and likewise for a chain's
`workflow` when it names catalog content. `plect task show <id>` reports
`PLECTURE-CFG-REF-ALIAS-REQUIRED` naming the reference that still needs it.

An `[bind.outputs]` block that re-exposed the inner layer's outputs has no
successor and no need for one: the facts it forwarded are published by the
observer, and a predicate reads them there.

### A chain whose `workflow` was computed

`workflow` is a static reference. A chain that picked its reviewer with a
template —

```toml
workflow = '{{if eq .Work.workflow "claude"}}codex{{else}}claude{{end}}'
```

— has no direct translation, because a chain's target is topology. State one
target, or declare one document per pairing (`tasks/work.md` in a
claude-flavored layer naming the codex reviewer, and the reverse) and let the
layer that is enabled decide.

### A chain's inputs

Each binding becomes a value:

| Before | After |
|---|---|
| `work_session = "{{.Work.session}}"` | `work_session = { from = "task.session" }` |
| `instance = "{{.Work.instance}}"` | `instance = { from = "task.instance" }` |
| `judge_ids = "{{.Work.done_when.pending_judge_ids}}"` | `judge_ids = { from = "task.done_when.pending_judge_ids" }` |
| `revision = "{{.Work.outputs.revision}}"` | `revision = { from = "resource.state.revision" }` |
| `pr_url = "{{.Work.outputs.pr_url}}"` | `pr_url = { from = "resource.state.pr_url" }` |
| `task = "review"` | `task = "review"` (a literal stays a literal) |

A projection reaching a key the observer has not reported yet keeps the chain
from firing, exactly as an absent wired output did.

## okf: `pursue_goal` no longer ships its chain

`pursue_goal` used to declare the chain that spawns a `goal_review` session.
It no longer can: a chain's `workflow` is a static reference, and a reference
written inside a plugin resolves only in that plugin's own namespace — okf
ships no runnable `goal_review` workflow, because which agent runs a review is
a choice the OKF specification does not make.

A host that wants the reviewer spawned automatically declares its own
`pursue_goal` document. Copy the shipped one and append the chain:

```bash
CONFIG_HOME="${PLECT_CONFIG_HOME:-$HOME/.config/plect}"
cp "$(plect task show pursue_goal --json | jq -r .source_path)" "$CONFIG_HOME/tasks/pursue_goal.md"
sed -i 's/^resource_observer = "okf_goal"$/resource_observer = "official.okf.okf_goal"/' \
  "$CONFIG_HOME/tasks/pursue_goal.md"
```

The observer reference is qualified because the copy is user-owned now; see
the note above. Then add, inside the frontmatter, before the closing `+++`:

```toml
[[pursue_goal.chains]]
id        = "goal_review"
workflow  = "goal_review"
placement = "sibling"

[pursue_goal.chains.when]
all = [
  { check = "resource.state.goal_parse_status", in = ["SUCCESS"] },
  { check = "resource.state.goal_status", in = ["open"] },
  { check = "resource.state.checklist_status", in = ["SUCCESS"] },
  { judge_pending = "goal-met" },
]

[pursue_goal.chains.inputs]
task         = "goal_review"
work_session = { from = "task.session" }
instance     = { from = "task.instance" }
judge_ids    = { from = "task.done_when.pending_judge_ids" }
```

`workflow = "goal_review"` is a bare reference, which from user-owned config
resolves in the user-owned layer stack — your own `workflows/goal_review.toml`.

## `pursue_goal` no longer validates its resource in a setup

A task document owns no lifecycle, so `pursue_goal`'s `okf-goal task
validate-goal-resource` setup is gone. Instantiation checks two things in its
place: the resource must resolve to the declared observer, and the first
observation must succeed. A goal file that exists but does not parse reports
`goal_parse_status = "FAILURE"` rather than failing observation, so
`plect task setup pursue_goal` now succeeds and the instance sits unsatisfied
on its first check instead of the setup refusing. `plect status <session>`
names the failing leaf.

## `plect state set-output --task` is not the write path

A reviewer records its verdict with:

```bash
plect state set "$PLECT_SESSION_NAME" --instance review#1 \
  '{"verdict_revision":"<sha>"}'
```

Update every instruction, runbook, and hook of your own that used
`set-output --task`. An agent that read a value out of `plect status --json`'s
`outputs` now finds an observed fact under `observed.state` and a recorded one
under `state`.

## Verification

Load every declaration and resolve its references:

```bash
CONFIG_HOME="${PLECT_CONFIG_HOME:-$HOME/.config/plect}"
for f in "$CONFIG_HOME"/tasks/*.md; do
  [ -e "$f" ] || continue
  plect task show "$(basename "$f" .md)" >/dev/null || echo "failed: $f"
done
```

Then one live session end to end — the flow this conversion exists for:

```bash
plect status <work-session>          # observed … ago, and the pending predicate
plect status <work-session> --json | jq '.chains'   # the reviewer chain's plan
plect tick <work-session>            # observes, decides, spawns a fired chain
```

If a chain reports `blocked_reason: "outputs_missing"`, `missing_outputs`
names the projection that has nothing to read yet — a pull request that does
not exist, a verdict not yet recorded. That is the gate working, not a
failure.
