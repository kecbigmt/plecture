# Task extension: extends

## Context

#222 proposed unifying configuration composition on a tree-wide overlay: a
merge algebra applied per node class (scalar, keyed map, append set) across
files within a layer and across cascade layers, with document-scoped
overlays and tree-local placement. The owner-ratified decision this ADR
records (2026-08-23, orchestrator conversation) rejects that breadth for
tasks. A named extension declaration with nesting's discipline — a single
composition point, a closed whitelist, no override — carries `done_when`'s
append-set monotonicity forward without the general merge algebra, the
document-scoped overlay concept, or tree-local placement. #222 closes with a
pointer here.

Effect nesting (`docs/adr/2026-08-17-task-nesting.md`) already proved out an
additive, closed-whitelist customization ladder for lifecycle composition,
and that decision explicitly rejected naming its own mechanism `extends`:
"It carries inheritance and deep-merge connotations that contradict the
no-override rule." That rejection was about composing an effect's lifecycle
(setup, cleanup, health, terminal) — the effect/task dissolution
(`docs/adr/2026-08-22-task-dissolution-into-effect-and-task.md`) has since
separated lifecycle (effect) from declaration (task) entirely. `extends` here
names a different composition: specializing a task *declaration* —
instructions, chains, a completion contract, and schema defaults — which is
closer to what inheritance-shaped naming actually describes, still held to
the same no-override discipline effect nesting established. The two
mechanisms do not compose with each other: `inner.uses` stays effect-only,
`extends` stays task-only, one blade per category.

The task instruction became an ordered `[[instructions]]` array
(`docs/adr/2026-08-23-retire-task-frontmatter.md`) specifically so this
decision could append a segment onto a base document rather than
concatenating strings. This is that forthcoming design.

## Decision

A task declaration may carry `extends = "<task reference>"`, the ordinary
dotted reference grammar validated against `kind = "task"`. An extension is
a real declaration with its own id; a referencing site (a workflow's chain
target, a dynamic-dispatch id) names the extension, and the base stays
untouched and independently referable.

Composition is a closed, entirely additive whitelist:

- `[[instructions]]` elements append after the base's, in declaration order.
- `[[chains]]` — the extension's own chains add to the base's.
- `done_when.all` — leaf append only. Judge ids are unique across the whole
  extends chain; a collision is a load error
  (`PLECTURE-CFG-EXTENDS-JUDGE-ID-DUPLICATE`).
- `inputs_schema` / `state_schema` — a new property key is freely addable. An
  existing key may only gain a default where none is set anywhere in the
  inner chain (`PLECTURE-CFG-EXTENDS-DEFAULT-REDECLARED` otherwise); every
  other change to an existing key is a load error
  (`PLECTURE-CFG-EXTENDS-SCHEMA-TYPE`).

`resource_observer` is not part of the whitelist: an extension inherits the
base's rather than restating it, and a document declaring both `extends` and
`resource_observer` is a load error (`PLECTURE-CFG-EXTENDS-INHERITED-FIELD`,
structural — the schema's own `if`/`then`/`else` on `taskDefinition` catches
it before any reference resolves). Restating it would be two authorities
answering the same question, exactly the shape this repository's audit
discipline treats as a defect class. `description` is the deliberate
exception: it is per-declaration display metadata, outside composition
entirely — an extension states its own, omitted means empty, and the base's
is untouchable by construction, mirroring how effects carry none at all.
`budget` is untouched by this decision: it stays an ordinary, independent
field on whichever declaration sets it, since it is a convergence bound on
one instance rather than a composed contract.

Depth is unbounded, aligned with effect nesting, and an extends chain that
reaches itself is a load error (`PLECTURE-CFG-EXTENDS-CYCLE`). There is no
grammar for removing, replacing, or weakening a base leaf, element, or key —
a task essentially different from its base is a full declaration of its own,
deliberately: weakening a gate has to be visible as a whole declaration,
never a small diff against something it silently no longer agrees with.

`plect task show` on an extension prints the composed extends chain,
outermost (the extension itself) first, naming for every layer the
instruction segment, the chains, and the judges that layer itself
contributes — the per-element provenance a reader needs to trust a composed
declaration without reconstructing it from every file in the chain by hand.

The full grammar, the worked examples, and the validation rules are
`docs/language/tasks.md`'s Extension section; the language chapter is this
decision's specification, not a paraphrase of it.

## Consequences

`app/internal/lang` gains `extends` to the task kind surface, a static-
topology check rejecting a computed `extends` (the same rule
`workspace_provider` and `inner.uses` already follow), and an extends-chain
resolver (`Validation.ExtendsChain`, cycle-checked via a visited-definition
set) that `ValidateTaskContracts` uses to compose the contracts a document's
own `done_when` and chains resolve against: `self.state.*` and judge ids
accumulate across the whole chain, not just the document's own declaration.
This runs natively, wired into the corpus's `TestNativeConformanceFixtures`
harness rather than deferred — unlike effect nesting, task contract
validation already lives in `lang`, so an extends-unaware version of it would
reject a valid extension's reference to inherited state the moment this
feature shipped.

`app/internal/config` composes the effective runtime document inside
`LoadTaskDocuments` itself, unconditionally — not only inside the heavier
`ValidateTaskDocuments` contract pass: instructions join with a blank line,
chains and `done_when` leaves concatenate, and schema properties merge by
key, reusing the same `Validation.ExtendsChain` resolution `lang` exposes
rather than re-deriving the rules a second time. Composing unconditionally
matters because two existing callers — `service.loadDisplayTasks` (list and
status display) and `watchdog.EvaluateHealth` (the L2 stall signal) —
deliberately read task documents straight off `LoadTaskDeclarations` and skip
the full contract pass for speed; had composition stayed behind
`ValidateTaskDocuments` only, both would silently evaluate an extension's
`done_when` missing every leaf its base contributed. Resolving `extends`
needs only the task namespace, so the registry `LoadTaskDocuments` builds for
it carries no observers or workflows, keeping this path as cheap as it was.
The composed chain's own per-layer contributions are kept on the result
(`TaskDocument.ExtendsLayers`) so `plect task show` can attribute every
composed element to the declaration that supplied it.

`plecture.schema.json`'s `taskDefinition` gains `extends` and an
`if`/`then`/`else` making `resource_observer` required exactly when `extends`
is absent. `docs/language/README.md` gains five diagnostic codes:
`PLECTURE-CFG-EXTENDS-INHERITED-FIELD` (structural), and `-CYCLE`,
`-JUDGE-ID-DUPLICATE`, `-DEFAULT-REDECLARED`, `-SCHEMA-TYPE` (semantic).

The conformance corpus gains `testdata/config-language/tasks/extends/`: the
three worked examples quoted verbatim in `docs/language/tasks.md` (a
cross-tool reviewer choosing between two static chains, a gate-variant
judge-recording extension, and a three-layer official/team/personal chain),
plus the four required error fixtures and the inherited-field structural
fixture.

No migration is needed: `extends` is new, additive vocabulary on an existing
kind, and no previously valid task declaration changes meaning.

Downstream, unstarted work this decision enables but does not itself do:
task-document re-homing into plugins, devbox extension-ization of its
existing host task forks, and an official orchestrator plugin.

## Alternatives considered

### Carry #222's overlay design forward for tasks specifically

Scoping the general merge algebra to just the task kind would still need a
document-scoped-overlay placement rule and a rediscovery of which file
"wins" for a given key — exactly the auditability cost #222's decision
identified for the general case. A named extension declaration keeps every
composed input enumerable from one reference chain instead of "every file
that happens to declare a fragment against this id."

### Allow `resource_observer` restatement on an extension, checked for agreement

Letting an extension restate the same observer, validated to match the
chain's, avoids a new diagnostic. It reintroduces the two-authorities shape
this repository's own audit discipline treats as a recurring defect class: a
reader (or a future edit) now has two declarations of the same fact with no
structural reason they must stay in sync beyond a validator remembering to
check. Forbidding the restatement outright removes the second authority
instead of policing agreement between two.

### Let a later layer's schema-key redeclaration freely win

Composing `inputs_schema` / `state_schema` by simple last-write-wins per key
(the more common "deep merge" instinct) was rejected the same way #222's
general merge algebra was: it lets a distant extension silently narrow or
retype a key a base's own logic depends on. Restricting composition to
"add a key" and "add a default where none exists" keeps every extension's
delta small enough to read as a diff against the base's contract rather than
a replacement of it.

### A separate `docs/design/task-extends.md`

Effect nesting has both a design document and an ADR, from before this
repository's configuration-language specification settled on the
`docs/language/` chapter model. `docs/language/tasks.md` already is that
model's semantic specification, worked examples, and validation rules for
every task construct, extends included; a parallel design document would
either duplicate it or drift from it, which the model's own layering rule
(`docs/language/README.md`, "Layers of specification") exists to prevent.
