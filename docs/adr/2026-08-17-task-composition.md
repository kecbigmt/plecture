# Task composition without task override

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
2. task composition for additive lifecycle, env, chain, and input-forwarding
   work;
3. whole-file fork as the explicit last resort.

Workflow definitions are user-owned exemplars, so user default values belong in
workflow node inputs, not in the task-composition mechanism.

## Decision

Plecture adds a task-composition contract specified by
[`docs/design/task-composition.md`](../design/task-composition.md).

A task definition may declare `inner = "<task-ref>"`. That task becomes the
outer task. The referenced task is the inner task. The workflow names the outer
task id, and chains attach to that id.

The lifecycle order is LIFO:

```text
outer setup -> inner setup -> inner cleanup -> outer cleanup
```

The outer task may inject environment variables into the inner task's process
executions and may forward its own validated inputs into the inner task's
validated input object. The outer task's input schema is coherent and
self-owned; it is not an edit to the inner task's schema.

Task composition is strictly additive. The outer task may not overwrite or
alter the inner task's behavior fields. The inner task has no reference to the
outer task and no virtual dispatch point back into it.

The reference field is named `inner`. It favors the lifecycle model over a
pattern name: the reader can see that the referenced task is inside the outer
task's setup/cleanup envelope.

Task references gain a catalog-qualified form, following the same qualification
principle as plugin executable references. `inner = "official/claude/claude"`
selects the `claude` task from the enabled `official/claude` plugin without
consulting same-id shadows. Workflow node `uses` accepts the same qualified
form so a user-owned workflow can opt out of a shadow deliberately.

Deep merge, patching, and override semantics are rejected. Extension surfaces
are author-declared and closed by default.

## Consequences

Core needs task-reference resolution that can address both the merged task
namespace and a task inside a specific enabled plugin.

Core needs composed-task validation for unknown inner references, composition
cycles, scope conflicts, rejected outer behavior fields, forwarded-input schema
conflicts, and invalid injected environment names.

Core needs lifecycle execution that stores outer setup outputs privately while
preserving the inner task's public output contract for downstream workflow
nodes, channels, status, and chains.

Task-shaped shadows that only add env, chains, forwarding, or a small path
injection can become short outer tasks. Script-internal command variation still
requires plugin task parameterization. Workspace layout remains a workspace
provider or workflow concern. Whole-file forks remain available when behavior
cannot be expressed by an author-declared input or by additive composition.

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
