# Tasks

Plecture gives autonomous work a place to go. A task document is the work — one
piece of it, made explicit enough to hand over.

A task declaration is an ordinary `[<id>]` table carrying `kind = "task"`, in
any `tasks/*.toml` file. `done_when` says when it is done, `resource_observer`
what it is about, `[[chains]]` what follows from it — and `instruction` (or,
for a longer body, `instruction_file`) says what is to be done. One
declaration carries all of it, because an instruction and the conditions for
calling it finished are one statement about one task.

The place that work goes is a session — assembled from effects by a workflow,
and described in the chapters after this one. Work is divided into tasks; a
task document declares one, and an instance carries it out.

<!-- fixture: tasks/document.toml -->
```toml
[work]
kind              = "task"
description       = "Implement a fix or feature for an issue and create a PR"
resource_observer = "issue_pr"
instruction_file  = "document.md"

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
```

<!-- fixture: tasks/document.md -->
```markdown
Resolve the issue at {{ resource.id }}.

Steps:

1. Understand the issue
2. Investigate the relevant code
3. Implement the changes
4. Write and run tests
5. Commit and push, then open a pull request
```

## Serialization

Every kind is declared the same way: a `[<id>]` table in a TOML definition
document, `task` included. One serialization means one grammar, one
structural schema, one validator, and one fixture set. Discovery does not
special-case task by file class — see [`declarations.md`](declarations.md).

The instruction body is a value the declaration carries, not a TOML string
field, for four reasons. Triple-quote escaping breaks structurally on an
instruction that quotes TOML examples containing multi-line strings, which
shipped instructions do — `instruction_file` sidesteps that entirely, and
`instruction` remains available for a body short enough to embed. Authoring,
reviewing, and exchanging a long instruction reads and diffs better as its own
Markdown file than as a quoted string, and a Markdown sidecar renders on
GitHub the way TOML never will. And the goal file a task converges with is
already a document.

## The instruction

`instruction` and `instruction_file` are two spellings of the same value,
mutually exclusive: declaring both is a load error
(`PLECTURE-CFG-TASK-INSTRUCTION-AND-FILE`). `instruction` is the body inline;
`instruction_file` names a Markdown file, resolved relative to the declaring
TOML file and read as plain prose — no frontmatter of its own, so it renders
correctly wherever Markdown does. It must resolve within the same trusted
layer: a path escaping it is a load error
(`PLECTURE-CFG-TASK-INSTRUCTION-FILE-CROSS-LAYER`), and a path naming no file
is too (`PLECTURE-CFG-TASK-INSTRUCTION-FILE-MISSING`). Declaring neither
leaves the instruction empty.

The Markdown sidecar carries no declaration of its own — it is a template
asset in the sense [`declarations.md`](declarations.md) describes, except that
this one is named by an `instruction_file` rather than sitting unreferenced.

Its interpolation is part of the language, not asset templating. `{{ <path> }}`
in prose is exactly the `from` projection in prose position: the same root
vocabulary, resolved and validated at load against the environment this surface
declares. The `{{ }}` delimiters are inherited from the Go templates the
instruction assets used, and are the mustache-family spelling a reader already
knows.

The roots are the ones the instruction is *about* — `inputs.<key>`,
`resource.id`, `resource.state.<key>`, `self.state.<key>`, `session.*` — listed
in [`values.md`](values.md) alongside the roots `done_when` reads. A projection
preserves its native type everywhere else in the language; in prose position it
is stringified, because prose has nowhere to put a list.

Control flow in the instruction is an open decision. CEL is expression-only,
so a conditional block needs a construct of its own, and none is introduced
here. The instruction assets carried into this shape keep the conditional and
defaulting forms they already had, transitionally, until that decision is
made.

## Declaration

A task declaration is a `[<id>]` table carrying `kind = "task"`, with every
field under that table — exactly how every other kind is declared. Task gets
no identity spelling of its own.

| Field | Meaning |
|---|---|
| `kind` | `task`. |
| `description` | What this task is for. |
| `resource_observer` | The resource observer this task is written for. |
| `instruction` | The instruction, inline. Mutually exclusive with `instruction_file`. |
| `instruction_file` | The instruction, as a sidecar Markdown file. Mutually exclusive with `instruction`. |
| `[<id>.inputs_schema]` | The instruction's author-declared parameters. |
| `[<id>.state_schema]` | This task's own state: the keys something else writes. |
| `[<id>.done_when]` | The completion predicate. |
| `[<id>.budget]` | The convergence bound, if any. |
| `[[<id>.chains]]` | What this task spawns, and when. See [`chains.md`](chains.md). |

Identity follows [`declarations.md`](declarations.md) unchanged: the id is the
table name, filename and directory stay non-semantic, task ids share the one
per-layer namespace every kind shares, and references use the same dotted forms
validated against `kind = "task"`.

Instance identity is separate and orthogonal: the id names the declaration,
while an instance is identified by its resource and instance name.

## State

`state_schema` declares this task's own state: the keys a reviewer or another
session writes into an instance. It is plain JSON Schema, and it carries no
mutability annotation — state is mutable by definition.

One rule covers the whole language: any definition that holds state declares it
with `state_schema`. A resource observer declares the state it publishes about a
resource; a task document declares the state it holds about itself.

Those two schemas are the two roots a completion predicate reads:
`resource.state.*` for what the observer publishes, and `self.state.*` for what this
task holds. Both are live — every read is current as of that evaluation.

There is no intermediate declaration between a schema and the predicate that
reads it. A key does not have to be re-listed to be readable: the observer's
`state_schema` already says what exists, and this document's `state_schema` says
what it keeps.

`verdict_revision` in the examples here is a convention, not a reserved key.
Core special-cases nothing about it: it is an ordinary declared state key whose
meaning lives entirely in the configuration that reads it.

<!-- fixture: tasks/observe-live-roots.toml -->
```toml
[review]
kind              = "task"
description       = "Review a pull request and record a verdict"
resource_observer = "issue_pr"
instruction       = "Review the pull request at {{ resource.id }} and record your verdict."

[review.inputs_schema]
type = "object"

[review.inputs_schema.properties]
instruction = { type = "string" }

# verdict_revision is this task's own state: written into the instance by the
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

## What a task document is not

A task document owns no lifecycle. It has no `setup`, no `cleanup`, no health
probe, no interactive endpoint, and no nesting joint. It brings nothing up and
takes nothing down — those are an effect's concerns, and a task document is
dispatched into a session a workflow has already built.

<!-- fixture: tasks/lifecycle-field.invalid.toml -->
```toml
[broken_task]
kind              = "task"
description       = "A task document that tries to own a lifecycle"
resource_observer = "issue_pr"
instruction       = "Resolve the issue at {{ resource.id }}."

[broken_task.setup]
type = "exec"
bin  = "github-issue-pr"
args = ["render-instruction"]

[broken_task.done_when]
all = [{ check = "resource.state.checks_status", in = ["SUCCESS"] }]
```

## Documents and instances

A task document is authored. Its identity is the id its declaration carries,
in the one per-layer namespace every kind shares, and references resolve to it
by that id.

A task instance is created from a document, by dynamic instantiation or by
another document's chain spawn. Its identity is its resource plus its instance
name. The two identities are orthogonal: one document backs many instances, and
an instance's resource says nothing about which id declared it.

The distinction runs through the rest of this specification. A document is
authored, declared, and referenced; an instance is created, evaluated, and
finalized. `done_when` is declared once on the document and
evaluated per instance.

## Resource binding

A task document declares the observer it is written for:

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
task document      done_when      reads them as resource.state.*
```

Because the observer is known from the declaration, a key it does not publish is
a load error rather than a surprise at run time. And because the declaration
states a type, instantiation checks compatibility up front: binding an instance
to a resource that does not resolve to the declared observer fails immediately,
rather than producing an instance that can never satisfy.

Every shipped task document is written for exactly one resource type, so this
dependency already existed — it was hiding in a runtime convention. Declaring it
is the move this language makes everywhere else.

An observer that reports more than one kind of thing is a sign the observer wants
splitting rather than the declaration wanting widening. Where a document's
completion depends on which kind it got — a pull request's checks against an
issue's timestamp, say — two observers state that better than one observer plus a
kind check.

A `done_when` check on the observer's own kind key remains useful for narrowing
*within* one observer, where a single observer publishes more than one subtype:
`resource.state.resource_kind in ["pull", "issue"]` distinguishes two shapes
the same observer reports.

A workflow's `[[nodes]]` never reference a task document. A node names an effect,
because a node is a position in a lifecycle graph; task arrives afterward,
against the session that graph produced.

## Validation rules

- A task declaration carries a `resource_observer`.
- `resource_observer` resolves to a definition of that kind.
- A task declares at most one of `instruction` and `instruction_file`.
- `instruction_file` resolves, relative to the declaring file, to a readable
  file within the declaring layer.
- A completion key reads `resource.state.*` or `self.state.*`.
- A `resource.state.*` key names a property the declared observer's
  `state_schema` declares, and a `self.state.*` key one this document's declares —
  both checked at load.
- An instance's resource resolves to the declared observer.
- Instantiation observes the resource once; a failed first observation rejects
  instantiation, and no instance is created.
- A lifecycle field is not part of the task grammar.
- A workflow node referencing a task document is a kind mismatch.
