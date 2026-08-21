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
kind              = "work"
description       = "Implement a fix or feature for an issue and create a PR"
resource_observer = "issue_pr"

[work.inputs_schema]
type = "object"

[work.inputs_schema.properties]
instruction = { type = "string" }

[work.done_when]
all = [
  { check = "resource.state.resource_kind", in = ["pull", "issue"] },
  { check = "resource.state.checks_status", in = ["SUCCESS", "NULL"] },
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
same value model, but not the same roots as `done_when`: the body reads what the
instruction is about — the resource, this work's parameters, the session — while
completion reads the live state it depends on. Both root sets are listed in
[`values.md`](values.md).

## Frontmatter

The frontmatter is an ordinary definition document. A work declaration is a
`[<id>]` table carrying `kind = "work"`, with every field under that table —
exactly how every other kind is declared. Work gets no identity spelling of its
own.

| Field | Meaning |
|---|---|
| `kind` | `work`. |
| `description` | What this work is for. |
| `resource_observer` | The resource observer this work is written for. |
| `[<id>.inputs_schema]` | The instruction's author-declared parameters. |
| `[<id>.state_schema]` | This work's own state: the keys something else writes. |
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

Those two schemas are the two roots a completion predicate reads:
`resource.state.*` for what the observer publishes, and `self.state.*` for what this
work holds. Both are live — every read is current as of that evaluation.

There is no intermediate declaration between a schema and the predicate that
reads it. A key does not have to be re-listed to be readable: the observer's
`state_schema` already says what exists, and this document's `state_schema` says
what it keeps.

`verdict_revision` in the examples here is a convention, not a reserved key.
Core special-cases nothing about it: it is an ordinary declared state key whose
meaning lives entirely in the configuration that reads it.

<!-- fixture: work/observe-live-roots.md -->
```markdown
+++
[review]
kind              = "work"
description       = "Review a pull request and record a verdict"
resource_observer = "issue_pr"

[review.inputs_schema]
type = "object"

[review.inputs_schema.properties]
instruction = { type = "string" }

# verdict_revision is this work's own state: written into the instance by the
# reviewer rather than published by the observer. It carries no mutability
# annotation, because state is mutable by definition.
[review.state_schema]
type = "object"

[review.state_schema.properties]
verdict_revision = { type = "string" }

[review.done_when]
all = [
  { check = "resource.state.resource_kind", in = ["pull", "issue"] },
  { expr = "self.state.verdict_revision == resource.state.revision" },
]

[review.budget]
heartbeat_budget = 3
on_exhaust       = "escalate"
+++
Review the pull request at {{ resource.id }} and record your verdict.
```

## Completion

`done_when` is a conjunction of leaves. A check leaf compares one key, named by
its root path. An expression leaf states a predicate computed over those roots —
which is how a recorded verdict is compared against a live revision, with no key
of its own to hang on. A judge leaf waits for independent reviewer input
recorded against the instance, optionally restricted to reviewers in a declared
relation.

A leaf reads `resource.state.*` or `self.state.*`, and nothing else. Both are
resolved at load against the schemas that declare them, so a misspelled key
fails before anything runs.

`budget` bounds convergence. Omitting it leaves completion unbounded, which is
what a standing goal needs: continuing to exist is not exhaustion.

## What a work document is not

A work document owns no lifecycle. It has no `setup`, no `cleanup`, no health
probe, no interactive endpoint, and no nesting joint. It brings nothing up and
takes nothing down — those are a task's concerns, and a work document is
dispatched into a session a workflow has already built.

<!-- fixture: work/lifecycle-field.invalid.md -->
```markdown
+++
[broken_work]
kind              = "work"
description       = "A work document that tries to own a lifecycle"
resource_observer = "issue_pr"

[broken_work.setup]
type = "exec"
bin  = "github-issue-pr"
args = ["render-instruction"]

[broken_work.done_when]
all = [{ check = "resource.state.checks_status", in = ["SUCCESS"] }]
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
finalized. `done_when` is declared once on the document and
evaluated per instance.

## Resource binding

A work document declares the observer it is written for:

```toml
resource_observer = "issue_pr"
```

The field is named after the kind it references, the same way a workflow's
`workspace_provider` is, and it is validated the same way — an ordinary dotted
reference whose target must declare `kind = "resource_observer"`.

An instance is still a document paired with a resource. The document is
*type-declared and instance-late-bound*: which resource, it learns at
instantiation; what kind of resource, it states up front.

Declaring it closes the chain at load time:

```text
resource observer  state_schema   the keys it publishes
        ↓
work document      done_when      reads them as resource.state.*
```

Because the observer is known from the declaration, a key it does not publish is
a load error rather than a surprise at run time. And because the declaration
states a type, instantiation checks compatibility up front: binding an instance
to a resource that does not resolve to the declared observer fails immediately,
rather than producing an instance that can never satisfy.

Every shipped work document is written for exactly one resource type, so this
dependency already existed — it was hiding in a runtime convention. Declaring it
is the move this language makes everywhere else.

A `done_when` check on the observer's own kind key remains useful for narrowing
*within* one observer, where a single observer publishes more than one subtype:
`resource.state.resource_kind in ["pull", "issue"]` distinguishes two shapes
the same observer reports.

A workflow's `[[nodes]]` never reference a work document. A node names a task,
because a node is a position in a lifecycle graph; work arrives afterward,
against the session that graph produced.

## Validation rules

- A work document opens with `+++` frontmatter declaring `kind = "work"`.
- A work document's frontmatter holds exactly one declaration, whose kind is
  `work`, and declares a `resource_observer`.
- `resource_observer` resolves to a definition of that kind.
- A completion key reads `resource.state.*` or `self.state.*`.
- A `resource.state.*` key names a property the declared observer's
  `state_schema` declares, and a `self.state.*` key one this document's declares —
  both checked at load.
- An instance's resource resolves to the declared observer.
- A lifecycle field is not part of the work grammar.
- A workflow node referencing a work document is a kind mismatch.
