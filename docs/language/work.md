# Work

Plecture gives autonomous work a place to go. A work document is that place.

A work document is a Markdown file. Its frontmatter is the completion
contract — what this work observes, when it is done, who may judge it, what it
spawns — and its body is the instruction. One file carries both, because an
instruction and the conditions for calling it finished are one statement about
one piece of work.

Work is the language's first-class primitive. Tasks and workflows exist to give
it somewhere to run.

<!-- fixture: work/document.md -->
```markdown
+++
kind        = "work"
description = "Implement a fix or feature for an issue and create a PR"
requires    = ["resource_kind", "checks_status", "issue_status"]

[inputs]
instruction = { type = "string" }

[observe]
resource_kind = { from = "resource.status.resource_kind" }
checks_status = { from = "resource.status.checks_status" }
issue_status  = { from = "resource.status.issue_status" }
revision      = { from = "resource.status.revision" }
pr_url        = { from = "resource.status.pr_url", optional = true }

[done_when]
all = [
  { check = "resource_kind", in = ["pull", "issue"] },
  { check = "checks_status", in = ["SUCCESS", "NULL"] },
  { judge = "acceptance criteria are satisfied, with a concrete reason", id = "ac-met" },
  { judge = "the resource change actually resolves the requested work without unaddressed risks", id = "solves" },
]

[budget]
heartbeat_budget = 3
on_exhaust       = "escalate"
+++
Resolve the issue at {{ resource.id }}.

Steps:

1. Understand the issue
2. Investigate the relevant code
3. Implement the changes
4. Write and run tests
5. Commit and push, then open a pull request
```

## Serialization

Frontmatter is TOML, delimited by `+++`.

One serialization means one grammar, one structural schema, one validator, and
one fixture set. The frontmatter carries the language's value model —
projections, computations, tagged values, completion entries — so a second
serialization would specify every construct twice, and would reintroduce
implicit typing into completion contracts, where `NULL` and `true` are load-
bearing values.

The body below the closing `+++` is the instruction. Its interpolation uses the
same roots the frontmatter does.

## Frontmatter

| Field | Meaning |
|---|---|
| `kind` | `work`. |
| `description` | What this work is for. |
| `[inputs]` | The instruction's author-declared parameters. |
| `[observe]` | The keys this work reads from its resource and its own recorded state. |
| `[records]` | Keys a reviewer or another session writes, rather than keys observed. |
| `requires` | The keys `done_when` reads. |
| `[done_when]` | The completion predicate. |
| `[budget]` | The convergence bound, if any. |
| `[[chains]]` | What this work spawns, and when. See [`chains.md`](chains.md). |

## Observation

`[observe]` is the only surface in the language with live roots. Every value in
it is current as of each evaluation: `resource.status.*` reads the resource
observer's state, and `self.*` reads this work's own recorded keys.

Observation is declared per key, with renames where this work wants different
names than the observer uses:

<!-- fixture: observers/per-key-outputs.md -->
```markdown
+++
kind        = "work"
description = "Review a pull request, reading the observer's state under this work's own names"
requires    = ["kind", "checks"]

[observe]
kind     = { from = "resource.status.resource_kind" }
checks   = { from = "resource.status.checks_status" }
revision = { from = "resource.status.revision" }
pr_url   = { from = "resource.status.pr_url", optional = true }

[done_when]
all = [
  { check = "kind", in = ["pull", "issue"] },
  { check = "checks", in = ["SUCCESS", "NULL"] },
]
+++
Review the pull request at {{ resource.id }}.
```

Because observation is per key, a status that does not apply to a resource kind
is a literal sentinel rather than an absence — `in ["SUCCESS", "NULL"]` accepts
it without it ever satisfying on its own.

An observation may also be computed, which is how a comparison against
recorded state stays correct at the moment the resource changes:

<!-- fixture: work/observe-live-roots.md -->
```markdown
+++
kind        = "work"
description = "Review a pull request and record a verdict"
requires    = ["resource_kind", "verdict_current"]

[inputs]
instruction = { type = "string" }

# verdict_revision is written by the reviewer through `plect state
# set-output`, not observed from the resource, so it is declared rather than
# projected.
[records]
verdict_revision = { type = "string" }

[observe]
resource_kind   = { from = "resource.status.resource_kind" }
revision        = { from = "resource.status.revision" }
verdict_current = { expr = "self.verdict_revision == resource.status.revision" }

[done_when]
all = [
  { check = "resource_kind", in = ["pull", "issue"] },
  { check = "verdict_current", in = [true] },
]

[budget]
heartbeat_budget = 3
on_exhaust       = "escalate"
+++
Review the pull request at {{ resource.id }} and record your verdict.
```

Nothing about a value being "dynamic" needs its own declaration form.
Re-evaluation rides on root liveness.

## Completion

`[done_when]` is a conjunction of leaves. A check leaf compares an observed or
recorded key; a judge leaf waits for independent reviewer input recorded by
`plect judge`, optionally restricted to reviewers in a declared relation.

`requires` names the keys those checks read. Every check names a `requires`
entry, and every `requires` entry is observed or recorded, so a typo in either
surfaces at load time.

`[budget]` bounds convergence. Omitting it leaves completion unbounded, which
is what a standing goal needs: continuing to exist is not exhaustion.

## What a work document is not

A work document owns no lifecycle. It has no `setup`, no `cleanup`, no health
probe, no interactive endpoint, and no nesting joint. It brings nothing up and
takes nothing down — those are a task's concerns, and a work document is
dispatched into a session a workflow has already built.

<!-- fixture: work/lifecycle-field.invalid.md -->
```markdown
+++
kind        = "work"
description = "A work document that tries to own a lifecycle"

[setup]
type = "exec"
bin  = "github-issue-pr"
args = ["render-instruction"]

[done_when]
all = [{ check = "checks_status", in = ["SUCCESS"] }]
+++
Resolve the issue at {{ resource.id }}.
```

## Instantiation

A work document is created by `plect task setup` or by another work document's
chain spawn. Its identity comes from its resource and its instance name.

A workflow's `[[nodes]]` never reference it. A node names a task, because a
node is a position in a lifecycle graph; work arrives afterward, against the
session that graph produced.

## Validation rules

- A work document opens with `+++` frontmatter declaring `kind = "work"`.
- `[observe]` projects `resource.status.*` and `self.*` only.
- An observed key names a property the resolved resource observer's
  `state_schema` declares.
- Every `done_when` check names a `requires` entry, and every `requires` entry
  is declared in `[observe]` or `[records]`.
- A lifecycle field is not part of the work grammar.
- A workflow node referencing a work document is a kind mismatch.
