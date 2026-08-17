# Task nesting without task override

## Context

Plugin task customization has relied on same-id shadowing: a user-owned task
file replaces a plugin task file wholesale. That is a legitimate fork, but it
freezes the fork point and hides plugin improvements that still mount from the
catalog.

The observed shadows decompose into a smaller set of intents: different input
values, extra environment for an agent process, chain attachment, one command
path, and workspace layout. Those intents do not all justify copying a full
task definition.

Plecture needs a three-rung customization ladder:

1. parameterization through author-declared task inputs;
2. task nesting for additive lifecycle, locals, env, chain,
   input binding, and output-binding work;
3. whole-file fork as the explicit last resort.

Workflow definitions are user-owned exemplars, so user default values belong in
workflow node inputs, not in the task-nesting mechanism.

## Decision

Plecture adds a task-nesting contract specified by
[`docs/design/task-nesting.md`](../design/task-nesting.md).

A task definition may declare `inner = "<task-ref>"`. That task becomes the
outer task. The referenced task is the inner task. The workflow names the outer
task id, and chains attach to that id.

An inner task reference may name another nested task. The lifecycle order is an
N-layer LIFO stack:

```text
outermost setup -> ... -> innermost setup -> innermost cleanup -> ... -> outermost cleanup
```

The outer task may bind environment variables into the inner task's process
executions and may bind its own validated inputs into the inner task's
validated input object. The outer task's input schema is coherent and
self-owned; it is not an edit to the inner task's schema.

Task nesting is strictly additive. The outer task may not overwrite or
alter the inner task's behavior fields. The inner task has no reference to the
outer task and no virtual dispatch point back into it.

The nested task's public outputs are only what the outer task explicitly
binds. Inner public outputs are not automatically promoted. This follows the
Rust newtype shape: the outer owner gets a reviewable boundary and chooses what
to re-export, while upgrades to the inner definition cannot silently widen the
outer contract. Go-style embedding is rejected here because automatic promotion
is ergonomics-first and makes inner upgrades change the nested task's public
surface without an edit to the outer file. Kotlin delegation and TypeScript
intersection types carry the same lesson: extra members are acceptable when the
outer contract states them, but hidden widening is not.

The outer task may use locals as private intermediate values from its setup.
Locals can feed bound inner inputs, outer cleanup, or explicit public output
binding, but they are not outputs unless the outer task binds them into the
public contract. Boundary wiring uses one namespace: `[bind.inputs]`,
`[bind.env]`, and `[bind.outputs]`.

The reference field is named `inner`. It favors the lifecycle model over a
pattern name: the reader can see that the referenced task is inside the outer
task's setup/cleanup envelope.

Task references gain a catalog-qualified form, following the same qualification
principle as plugin executable references. `inner = "official/claude/claude"`
selects the `claude` task from the enabled `official/claude` plugin without
consulting same-id shadows. Workflow node `uses` accepts the same qualified
form so a user-owned workflow can opt out of a shadow deliberately.

A plugin may nest its own tasks, so a plugin author can factor a shared inner
layer out of related variants instead of duplicating it. Cross-plugin nesting
needs no rule of its own: an inner reference to another plugin would have to
name a catalog alias, and aliases are user-local, so the plugin-layer reference
grammar has no vocabulary for one. The boundary is enforced by grammar rather
than by validation.

Deep merge, patching, and override semantics are rejected. Extension surfaces
are author-declared and closed by default.

## Consequences

Core needs task-reference resolution that can address both the merged task
namespace and a task inside a specific enabled plugin.

Core needs nested-task validation for unknown inner references, nesting cycles,
scope conflicts, rejected outer behavior fields, explicit output binding,
missing required machinery outputs, bound-input schema conflicts, direct-output
schema compatibility, computed-output string typing, repeated environment keys
across layers, and invalid injected environment names.

Core needs lifecycle execution that stores outer setup locals privately while
declaring only the output keys bound by the outer task. Output binding is a
live projection rather than a setup-time copy. Direct output bindings project
the inner value without string rendering; computed output bindings render
templates to strings. Downstream workflow nodes, channels, status, chain output
mappings, and nested-task done_when output checks validate against the outer
public contract, while chain judge ids resolve from the effective innermost
done_when.

Core needs task inspection output to show the nesting chain from the outermost
task to the innermost plugin task, so an operator can audit provenance without
reconstructing it from resolved config files.

The production fitness target is a zero-copy path for all seven
plugin-counterpart shadows listed in the design note. Task nesting covers
additive lifecycle, locals, input binding, path injection, chain attachment, and
explicit output binding.
Agent-process environment for terminal-launched runtimes, worker state
location, queue message formatting, workspace layout, branch naming, cleanup
defaults, and review-state observation remain author-declared parameterization
surfaces in their owning plugins. Runtime argument wiring for a retiring
third-party service is outside this decision. Whole-file forks remain available
when behavior cannot be expressed by an author-declared input or by additive
nesting.

## Alternatives considered

### Keep same-id shadowing as the only customization tool

Whole-file replacement is simple and remains necessary for true forks. It is
too coarse as the only customization surface: copying a plugin task to change a
few values disconnects the user from later plugin improvements.

### Deep merge or `extends`

Deep merge would let a user patch arbitrary parts of a plugin task, but it
makes provenance and final behavior difficult to audit. It also turns every
nested field into an extension surface, including fields the task author never
declared stable.

### Task override

An outer task that can overwrite inner fields recreates the fragile-base-class
problem. The inner task author can no longer reason locally about setup,
cleanup, health, terminal, outputs, or done_when behavior because a downstream
config layer may replace pieces in place.

The no-override rule follows the same shape as coherence and final-by-default
systems: the owner of a definition declares its extension surface, and other
parties create a named variant instead of mutating that definition in place.

### Single-level nesting only

Single-level nesting would make the implementation smaller, but it guards
against a problem this design does not have. Implicit cross-layer merge rules
make multi-level systems hard to trace; this design avoids them because every
layer owns a complete input schema, explicit public output boundary, and
private locals. Go embedding, Rust newtypes, and middleware stacks all nest
freely when each layer has an auditable boundary.

### Innermost environment wins

Letting the innermost `bind.env` value win on duplicate keys would keep
execution moving, but it hides which layer changed the process environment.
Plecture prefers fail-loud configuration errors for ambiguous ownership, so a
repeated environment key anywhere in the nesting chain is a load error.

### Task composition name

Task composition is rejected as the feature name. The plugin-boundary decision
already uses configuration-level composition for workflow-layer combining, so
reusing the term here would overload a reserved concept. Task nesting names the
actual mechanism: containment through `inner`, LIFO lifecycle, and per-layer
private locals.

### Output wiring table named `[outputs]`

Naming the explicit output wiring table `[outputs]` is rejected because task
definitions already use `[[outputs]]` for dynamic output scripts, whose Go type
is `DynamicOutput`. Reusing the same TOML key for a table and an array of tables
would make the implementation depend on shape sniffing and make bracket typos
select another feature. Adopting `[outputs]` for public output binding would
force a `[[dynamic_outputs]]` migration of shipped task configuration.

### Boundary namespace named `[forward.*]`

`[forward.inputs]` reads well for inner inputs, but forwarding is a relay word.
It strains for environment values created by the outer layer and for public
outputs flowing from inner outputs or locals to downstream consumers.

### Boundary namespace named `[wire.*]`

`[wire.*]` is rejected because Plecture configuration as a whole is a
declarative wiring language. Naming one construct with the language's general
character word would discriminate too little.

### Same-name `expose` plus `[publish]`

A same-name re-export shorthand plus a separate output table would create two
vocabularies for one public boundary. Once every public field is declared in
the outer output schema, the shorthand saves only one binding line per key while
splitting the reviewable contract across two places.

### Alternative reference names

| Name | Result | Reason |
|---|---|---|
| `inner` | chosen | It makes the LIFO shape visible: the referenced task sits inside the outer task's lifecycle envelope. |
| `decorates` | rejected | It has the right additive connotation, but the lifecycle order is not self-evident without pattern literacy. |
| `around` | rejected | It hints at surrounding lifecycle, but also suggests aspect-style interception that this contract does not provide. |
| `delegates` | rejected | It names dispatch semantics only and hides cleanup unwind. |
| `wraps` | rejected | It suggests bidirectional interception and possible transformation of inner behavior. |
| `extends` | rejected | It carries inheritance and deep-merge connotations that contradict the no-override rule. |
| `embeds` | rejected | It recalls Go embedding, whose promotion rules are about lookup rather than lifecycle. |
