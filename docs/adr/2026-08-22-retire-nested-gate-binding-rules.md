# Retire nested gate binding rules

## Context

The combined task kind allows completion conditions and chains on every layer
of a nesting chain. Two load rules protect those gates from the composed output
boundary: an inner completion check must reach the outer contract through
direct output bindings, and a chain output reference must reach the composed
contract. Without those rules, an outer layer can omit, rename, or substitute a
fact that a nested gate reads.

The language now dissolves that kind into lifecycle effects and completion
tasks. Effects retain nesting but have no gates. Tasks carry `done_when` and
chains, do not nest, and read `resource.state` and `self.state` roots validated
against their declaring schemas. The output-binding neutralization hole is
therefore not expressible in the resulting language, and both rules lose their
job with the surface they constrain.

Building per-layer gate evaluation and an audit namespace in the combined
kind would add semantics and state projection to an engine that the dissolution
removes.

## Decision

Remove the direct-binding requirement for nested completion checks and the
composed-contract reachability requirement for chain output references. Keep
the existing judge-id and private-local validation for chains.

Do not add per-layer completion or chain evaluation to the combined kind. Do
not add a multi-layer audit namespace. If task evaluation facts need an audit
surface after the dissolution, specify it on the flat task document and its two
state roots.

Until the dissolution is implemented, the combined kind keeps its existing
composed-output evaluation. The owner accepts the interim risk that a nested
gate can remain pending or observe an outer value with the same spelling after
its binding guard has been removed.

## Consequences

A nested definition loads when an inner completion fact is omitted from the
outer contract or carried through a computed binding. Its chains also load when
their output references do not reach the composed public contract. Plain task
chain input references remain validated against that task's own output schema,
and nested chain locals remain rejected.

The lasting implementation is limited to removing two validation rules and
their load errors. It introduces no evaluation engine, persisted state, status
projection, or public audit shape that the effect/task dissolution would need
to remove.

The interim combined runtime does not gain the neutralization protection that
per-layer evaluation would provide. The dissolution closes that gap by making
effects gate-free and making every task gate read schema-validated state from a
flat task document.

No migration is required. No deployed nested bindings rely on either rejected
configuration shape.

## Alternatives considered

Adding per-layer evaluation and a multi-layer audit namespace was rejected
because both are tied to gates on nested lifecycle layers, a surface the
ratified language removes.

Keeping both load rules until the dissolution was rejected because the interim
validators could be carried into the new language accidentally, preserving
binding constraints after their neutralization rationale no longer applies.
