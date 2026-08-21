# Dissolve the task kind into effect and task

## Context

The configuration language had one kind named `task` that carried two natures.

Its lifecycle nature managed something in the environment: `setup` brought a
thing up, `cleanup` took it down, `[health]` observed it, `[terminal]` exposed
it, and the nesting joint composed it with other layers. Its completion nature
described a unit of work to finish: `done_when` decided when that was, `judge`
leaves brought in independent reviewers, `[[chains]]` spawned follow-on
sessions, and dynamic outputs re-read external state to feed the predicate.

The merger is historical. The kind was originally called `effect`, and was
renamed `task` when it absorbed the completion nature, because "effect" no
longer covered what it had come to include. The name change was the trace of
the merger, not a decision about the concept.

Nothing in shipped configuration uses both natures at once. Auditing every
shipped plugin configuration for this specification phase produced three
measurements:

* Zero usage overlap. No shipped declaration both manages a lifecycle and
  declares a completion contract.
* All six completion declarations declare zero `[health]`, and five of the six
  have a `setup` that exists only to render an instruction template and echo it
  as an output. The sixth (`pursue_goal`) has no instruction template and a
  `setup` that only validates its resource.
* `[[chains]]`, dynamic `[[outputs]]`, and `requires` have no lifecycle-side
  consumers at all. Every declaration using them is a completion declaration.

The instruction those six declarations deliver already lives outside the TOML,
as a Markdown template the `setup` shells out to render. The completion nature's
most important content was therefore already a document, reached through
lifecycle machinery that existed only to fetch it.

## Decision

The `task` kind dissolves into two kinds.

### effect

An effect is a lifecycle-managed provider: contracts, health, an optional
interactive capability, and composition. Its grammar is `scope`,
`setup`/`cleanup`, `inputs_schema`/`outputs_schema`, `[health]`, `[terminal]`,
and the nesting joint (`inner`, `inner.inputs`, `inner.env`, `outputs.bind`,
`locals_schema`).

An effect's outputs are production records: immutable facts from `setup` plus
projections through the nesting joint. `outputs.bind` therefore reads
`inner.outputs.*` and `locals.*` only. Live re-evaluation is not an effect
concern.

A workflow's `[[nodes]]` reference effects, because a node is a position in a
lifecycle graph.

### task

A task is a completion document: a Markdown file whose TOML frontmatter is the
completion contract and whose body is the instruction. The frontmatter holds
exactly one declaration, whose kind is `task`.

Its grammar is `resource_observer` (the observer it is written for),
`inputs_schema` (the instruction's parameters), `state_schema` (the keys a
reviewer or another session writes), `done_when`, `budget`, and `[[chains]]`.

Completion leaves read two live roots — `resource.state.*` for what the declared
observer publishes, and `self.state.*` for the task's own state — resolved at
load against the schemas that declare them.

A task is not referenced by a workflow node. It is instantiated dynamically, or
spawned by another task's chain, and a task instance is identified by its
resource and instance name while the document is identified by its declared id.

### The document form

A kind with a body is a Markdown file carrying a TOML-frontmatter declaration
block; a kind without a body is a TOML file. Grammar consistency comes from the
frontmatter being the ordinary declaration form, not from every kind sharing a
file format.

The instruction is not a TOML string field. Triple-quote escaping breaks
structurally on an instruction that quotes TOML examples containing multi-line
strings, which shipped instructions do; and the container should match the
majority medium, which for a task is prose and for an effect is configuration.

### Naming

`effect` is revived rather than coined. The word described this contract before
the merger, it matches what the contract does — a managed effect on the
environment, applied, reverted, and verified — and it carries the intuition
already established by effect systems and by `useEffect`.

`task` moves to the completion document, because it is the everyday word for a
thing one completes, which is exactly what `done_when` decides.

`work` leaves the kind vocabulary and returns to prose, as the uncountable
phenomenon the project exists to house: work is divided into tasks. The `.work[]`
status field reads correctly under that sense and is unaffected.

Rejected candidates for the lifecycle kind:

* `provider` — the specification's own prose reached for it, but it is a
  non-obvious agent noun for a configuration construct, and it collides with
  `workspace_provider`. That collision may one day be a genus rather than a
  clash, which is an argument for a future unification and not for this rename.
* `node` — names the slot a workflow puts it in rather than the thing itself,
  and breaks immediately at `inner`, where an effect nests inside another effect
  with no node in sight.
* keeping `task` for the lifecycle kind — its everyday meaning is a thing to
  complete, so it would remain a synonym for the very concept being separated
  from it.

Because the CLI's dynamic-instantiation verb targets the completion document, it
keeps its existing spelling and now names the right kind.

## Consequences

Each kind's grammar is closed and small, and load-time validation can be exact
about which fields belong where — a completion field in an effect and a
lifecycle field in a task are both errors a schema catches.

The six completion declarations lose their render-glue `setup` entirely, along
with the `instruction` output that carried its result: the instruction is the
document body. Their `[[outputs]]` bulk copies dissolve into direct reads of the
declared observer's published keys.

Live re-evaluation lives on exactly one surface, and mutable write-through
remains a nesting-joint property, so "what changes underneath me" and "what I
produced once" are no longer expressible in the same place.

Documents written before this decision use `task` for what is now `effect`.
Design documents whose titles carry that reading — `task-nesting.md` among them —
are retitled during the implementation phase rather than here, so this
specification's own prose stays in the new vocabulary throughout.

## Alternatives considered

### Keep one kind and mark field applicability

The specification did this first: one kind, with each field annotated as valid
in "node mode" or "instance mode", and invalid combinations reported as load
errors.

It described the split without making it. The annotations had to encode which
nature a field belonged to, mode had to be derived from whether a workflow node
referenced the declaration, and every chapter had to explain the two readings
before it could explain anything else. The evidence that a field belonged to one
nature or the other was the same evidence for separating them.

### Split by adding a subkind

A `kind = "task"` with a `subkind` discriminator would have kept one reference
grammar and one schema branch.

It preserves the merger in the shape of the configuration while denying it in
the vocabulary: a reference site expecting one subkind and rejecting the other
is a kind check with extra steps, and the shared branch would hold no shared
fields once the natures were separated.

### Move only the instruction into a document

The instruction could have become a document while the completion contract
stayed in TOML alongside the lifecycle fields.

That splits the completion nature across two files while leaving it entangled
with the lifecycle nature in one of them — the opposite of the grouping the
evidence supports, since the instruction and the conditions for calling it
finished are one statement about one task.
