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
[work]
kind        = "work"
description = "Implement a fix or feature for an issue and create a PR"
requires    = ["resource_kind", "checks_status", "issue_status"]

[work.inputs_schema]
type = "object"

[work.inputs_schema.properties]
instruction = { type = "string" }

[work.observe]
resource_kind = { from = "resource.status.resource_kind" }
checks_status = { from = "resource.status.checks_status" }
issue_status  = { from = "resource.status.issue_status" }
revision      = { from = "resource.status.revision" }
pr_url        = { from = "resource.status.pr_url", optional = true }

[work.done_when]
all = [
  { check = "resource_kind", in = ["pull", "issue"] },
  { check = "checks_status", in = ["SUCCESS", "NULL"] },
  { judge = "acceptance criteria are satisfied, with a concrete reason", id = "ac-met" },
  { judge = "the resource change actually resolves the requested work without unaddressed risks", id = "solves" },
]

[work.budget]
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

The rule across the language is: a kind with a body is a Markdown file carrying
a TOML-frontmatter declaration block; a kind without a body is a TOML file.
Grammar consistency comes from the frontmatter being the uniform declaration
form, not from every kind living in the same file format.

Frontmatter is TOML, delimited by `+++`. One serialization means one grammar,
one structural schema, one validator, and one fixture set. The frontmatter
carries the language's value model — projections, computations, tagged values,
completion entries — so a second serialization would specify every construct
twice, and would reintroduce implicit typing into completion contracts, where
`NULL` and `true` are load-bearing values.

The instruction is not a TOML string field, for four reasons. Triple-quote
escaping breaks structurally on an instruction that quotes TOML examples
containing multi-line strings, which shipped instructions do. The container
should match the majority medium: prose dominates a work document, making it a
document with declaration metadata rather than config with an embedded document
— the reverse of a task, which is why a task is a TOML file. Authoring,
reviewing, and exchanging work reads and diffs as prose. And the goal file it
converges with is already a document.

The body below the closing `+++` is the instruction. Its interpolation uses the
same value model, but not the same roots as the frontmatter: the body reads what
the instruction is about — the resource, this work's parameters, the session —
while `[observe]` reads the live state completion depends on. Both root sets are
listed in [`values.md`](values.md).

## Frontmatter

The frontmatter is an ordinary definition document. A work declaration is a
`[<id>]` table carrying `kind = "work"`, with every field under that table —
exactly how every other kind is declared. Work gets no identity spelling of its
own.

| Field | Meaning |
|---|---|
| `kind` | `work`. |
| `description` | What this work is for. |
| `[<id>.inputs_schema]` | The instruction's author-declared parameters. |
| `[<id>.state_schema]` | This work's own state: the keys something else writes. |
| `[<id>.observe]` | The keys this work reads from its resource and from its own state. |
| `requires` | The keys `done_when` reads. |
| `[<id>.done_when]` | The completion predicate. |
| `[<id>.budget]` | The convergence bound, if any. |
| `[[<id>.chains]]` | What this work spawns, and when. See [`chains.md`](chains.md). |

Two rules turn that document into a work document: its frontmatter holds
exactly one declaration, and that declaration's kind is `work`. The body is
that declaration's instruction. Neither rule forks the grammar — the parser,
the schema, and the validator are the ones a TOML config file already uses, so
the work declaration form is independent of the document sugar around it.

Identity follows [`declarations.md`](declarations.md) unchanged: the id is the
table name, filename and directory stay non-semantic, work ids share the one
per-layer namespace every kind shares, and references use the same dotted forms
validated against `kind = "work"`.

Instance identity is separate and orthogonal: the id names the declaration,
while an instance is identified by its resource and instance name.

## State

`state_schema` declares this work's own state: the keys a reviewer or another
session writes into an instance. It is plain JSON Schema, and it carries no
mutability annotation — state is mutable by definition.

One rule covers the whole language: any definition that holds state declares it
with `state_schema`. A resource observer declares the state it publishes about a
resource; a work document declares the state it holds about itself.

That closes a chain:

```text
resource observer  state_schema   the keys it publishes about the resource
        ↓
work document      observe        subscribes to those keys, and to its own state
        ↓
work document      state_schema   the keys it holds itself
```

`verdict_revision` in the example below is a convention, not a reserved key.
Core special-cases nothing about it: it is an ordinary declared state key whose
meaning lives entirely in the configuration that reads it.

## Observation

`observe` is the only surface in the language with live roots. Every value in
it is current as of each evaluation: `resource.status.*` reads the resource
observer's published state, and `self.*` reads this work's own declared state.

Observation is declared per key, with renames where this work wants different
names than the observer uses:

<!-- fixture: observers/per-key-outputs.md -->
```markdown
+++
[review]
kind        = "work"
description = "Review a pull request, reading the observer's state under this work's own names"
requires    = ["kind", "checks"]

[review.observe]
kind     = { from = "resource.status.resource_kind" }
checks   = { from = "resource.status.checks_status" }
revision = { from = "resource.status.revision" }
pr_url   = { from = "resource.status.pr_url", optional = true }

[review.done_when]
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
[review]
kind        = "work"
description = "Review a pull request and record a verdict"
requires    = ["resource_kind", "verdict_current"]

[review.inputs_schema]
type = "object"

[review.inputs_schema.properties]
instruction = { type = "string" }

# verdict_revision is this work's own state: written into the instance by the
# reviewer rather than observed from the resource. It carries no mutability
# annotation, because state is mutable by definition.
[review.state_schema]
type = "object"

[review.state_schema.properties]
verdict_revision = { type = "string" }

[review.observe]
resource_kind   = { from = "resource.status.resource_kind" }
revision        = { from = "resource.status.revision" }
verdict_current = { expr = "self.verdict_revision == resource.status.revision" }

[review.done_when]
all = [
  { check = "resource_kind", in = ["pull", "issue"] },
  { check = "verdict_current", in = [true] },
]

[review.budget]
heartbeat_budget = 3
on_exhaust       = "escalate"
+++
Review the pull request at {{ resource.id }} and record your verdict.
```

Nothing about a value being "dynamic" needs its own declaration form.
Re-evaluation rides on root liveness.

## Completion

`[done_when]` is a conjunction of leaves. A check leaf compares an observed or
recorded key; a judge leaf waits for independent reviewer input recorded against the
instance, optionally restricted to reviewers in a declared relation.

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
[broken_work]
kind        = "work"
description = "A work document that tries to own a lifecycle"

[broken_work.setup]
type = "exec"
bin  = "github-issue-pr"
args = ["render-instruction"]

[broken_work.done_when]
all = [{ check = "checks_status", in = ["SUCCESS"] }]
+++
Resolve the issue at {{ resource.id }}.
```

## Documents and instances

A work document is authored. Its identity is the id its frontmatter declares,
in the one per-layer namespace every kind shares, and references resolve to it
by that id.

A work instance is created from a document, by dynamic instantiation or by
another document's chain spawn. Its identity is its resource plus its instance
name. The two identities are orthogonal: one document backs many instances, and
an instance's resource says nothing about which id declared it.

The distinction runs through the rest of this specification. A document is
authored, declared, and referenced; an instance is created, evaluated, and
finalized. `observe` and `done_when` are declared once on the document and
evaluated per instance.

## Resource binding

An instance is a document paired with a resource. The document is
resource-agnostic: it declares key names and never names a concrete resource.

The resource arrives at instantiation — from the session's default, or given
explicitly — and resolves by pattern matching to a resource observer. That is
the moment the chain closes:

```text
resource observer  state_schema   the keys it publishes
        ↓
work instance      observe        subscribes to those keys
        ↓
work instance      done_when      evaluates over them
```

One honest consequence: an observed key's existence cannot be validated when
the document loads, because which observer will publish it is unknown until a
resource is bound. Load-time validation of `observe` is structural only — the
roots are checked, the key names are not. Key existence is checked at
instantiation, against the observer the resource resolved to.

The convention for constraining what a document expects is a `done_when` check
on the observer's own kind key, which keeps a document from satisfying against
a resource it was not written for.

A workflow's `[[nodes]]` never reference a work document. A node names a task,
because a node is a position in a lifecycle graph; work arrives afterward,
against the session that graph produced.

## Validation rules

- A work document opens with `+++` frontmatter declaring `kind = "work"`.
- A work document's frontmatter holds exactly one declaration, whose kind is
  `work`.
- `observe` projects `resource.status.*` and `self.*` only.
- A `self.*` projection names a `state_schema` property.
- An observed key names a property the resolved resource observer's
  `state_schema` declares.
- Every `done_when` check names a `requires` entry, and every `requires` entry
  is declared in `observe` or `state_schema`.
- A lifecycle field is not part of the work grammar.
- A workflow node referencing a work document is a kind mismatch.
