# Retire dormant declaration fields and the environment execution plane

## Context

Plecture's configuration is a declarative wiring language, and its surfaces are
supposed to derive from observed need. A usage survey of the shipped plugin
catalog plus production user configuration found four task-definition-level
fields violating that norm, in two different ways.

**Fields with declarers but no consumer.** `primary` was declared `true` on
three shipped task definitions (`claude`, `codex`, `codex_exec`) and in user
configuration, but nothing in `app/` or `contracts/` read it. It survived as a
struct field copied from `config.TaskDefinition` into `task.Resolved` and no
further. Its original consumer — a "session principal task" concept — was
removed in an earlier arc, and definition authors kept declaring it in good
faith because nothing said it had stopped mattering.

**Fields with a consumer but no declarers.** `idle_after` was decoded into the
task instance and never read by anything, a leftover of the health-vocabulary
consolidation: no reader and no writer.

`execution` is the more consequential case. It selects a task's execution plane
(`host` or `environment`), and its machinery was alive: `task.ResolveExecution`
ran during task assembly, `channel` and `resource` definitions carried their own
`execution` field, and plane-routing paths existed end to end (an
`EnvironmentExecutor` wrapping an environment's `exec` script, an `@environment`
pseudo-node with its own setup/cleanup hooks, a `.Environment.outputs.<key>`
template surface, and a `channel.Executor` seam adapted in `dispatch`). What
did not exist was a single declarer, anywhere: no shipped plugin and no user
configuration set `execution` on a task, channel, or resource, and none set the
workflow-level `environment`. Every node therefore resolved to the host default
on every run.

The environment plane is the frozen experiment reverted on 2026-07-08 with a
full-redesign verdict. This repository's rule is that speculative complexity
which ships anyway carries an explicit removal condition; the plane had reached
the condition — a frozen experiment whose surface had accumulated declarers-free
plumbing across five packages and two config kinds — without being removed.

The `resource` variant made the YAGNI violation explicit in its own doc comment:
the field was "not yet consulted" and was "parsed and validated so config
authors can declare it ahead of that wiring."

A fifth field, `requires`, was in scope for a naming review only. It names the
output keys a task's `done_when` reads, and it is the contract that makes a typo
in either the predicate or the outputs schema a compile-time error.

## Decision

Retire `primary`, `idle_after`, and `execution` from the declaration surface,
and retire the environment execution plane with them, including its plumbing.
Keep `requires` under its current name.

The retirement covers:

- `primary` and `idle_after` on task definitions, plus the three shipped
  `primary = true` declarations;
- `execution` on task, channel, and resource definitions;
- `task.ResolveExecution` and the per-node plane routing it fed;
- the workflow-level `environment` and `environment_inputs` fields;
- the `environment` declaration kind — `environments/*.toml`,
  `config.EnvironmentConfig`, and `Config.LoadEnvironments`;
- the `@environment` pseudo-node, its setup/cleanup hooks, and the
  `.Environment.outputs.<key>` template surface;
- `task.EnvironmentExecutor` and the `channel.Executor` seam that adapted it for
  exec channels.

Loading fails loud on every retired key. A task definition declaring `primary`,
`idle_after`, or `execution`; a workflow declaring `environment` or
`environment_inputs`; and a channel or resource declaring `execution` are each a
load error naming the retired key and pointing at the migration, rather than a
key the decoder silently drops.

**Environment-scope recommendation (the audit this decision was asked to
record): the workflow-level `environment` / `environment_inputs` fields and the
`environment` kind retire together with `execution` rather than staying as the
redesign's anchor.** Three reasons.

First, the evidence standard that condemns `execution` condemns them
identically. Zero declarers is the finding, and it is the finding for all of
them; keeping one half of a surface that nobody declares because the other half
was retired does not make the kept half load-bearing.

Second, keeping `environment` after `execution` is gone is not preservation, it
is a silent redesign. `execution` was the per-node selector; the workflow's
`environment` was the default every node inherited. Removing only the selector
would leave an all-or-nothing, whole-workflow plane — a different design from
the one that was frozen, arrived at by deletion rather than by decision, and
with no consumer asking for it.

Third, the frozen plane's verdict was that revival happens on the redesign's own
terms and must not build on the current plumbing. An anchor a future design is
forbidden to build on is not an anchor. Git history preserves the reverted
design better than a live config surface does, and a live surface keeps costing:
documentation, load paths, layer-conflict rules, and every subsequent field
audit.

**`requires` naming verdict: keep the name.** The alternative considered was a
rename toward what the field literally lists — `reads`, or `required_outputs`.
The generic objection to `requires` is that in adjacent tooling vocabularies
(GitHub Actions `needs`, package manifests) it names a dependency on another
unit of work, whereas here it names output keys of the declaring task itself.
That objection does not bite in this language: node dependencies are derived
from `.Nodes.<id>.outputs.<key>` references and were deliberately removed from
the node surface, so nothing else in Plecture's vocabulary competes for the
word. Against a purely nominal gain stands a breaking rename for every current
declarer, and the mechanism itself is well regarded. Keep it.

## Consequences

Configuration that declares any retired key stops loading, with an error naming
the key. This is a breaking change and ships with a one-time migration
(`docs/migrations/retired-declaration-fields-migration.md`), not a
compatibility read, per the pre-1.0 policy.

An `environments/` directory left behind in a plugin or in global configuration
is no longer read at all. It is inert rather than an error: it is a directory
the loader no longer looks in, and the workflow key that used to point at it —
`environment` — is what fails loud. The migration says to delete it.

Every task setup, cleanup, healthcheck, and dynamic-output fetch now runs on the
host through a single path. `task.Executor` survives as the seam tests swap to
observe what each exec path issues; it no longer carries a second
implementation. `RunSetup`, `RunCleanup`, and `ExecuteTaskSetup` lose their
variadic executor parameter, and `RenderInputs` loses its variadic
environment-outputs parameter — signatures that existed only to let the plane be
threaded through without breaking pre-existing call sites.

A session principal task and a container-or-remote execution plane are both
plausible future needs. Each is reintroduced from the need when it appears —
for the principal task, probably from whatever the health declaration already
establishes de facto; for the execution plane, from the redesign that the
2026-07-08 revert called for.

## Alternatives considered

### Retire `execution` but keep the environment kind as a redesign anchor

Rejected for the three reasons recorded in the Decision. The decisive one is
that it is not a null change: with the per-node selector gone, the surviving
surface would silently become a whole-workflow-only plane that no design
document describes and no declarer wants.

### Keep `primary` and give it a consumer

`primary` had declarers, which is weak evidence that authors wanted the concept.
Rejected because the declarations were maintenance inertia rather than demand:
nothing had behaved differently for them since the original consumer was
removed, and no user reported the resulting absence. Inventing a consumer to
justify a field inverts the norm that surfaces derive from observed need. If the
concept is wanted again, the `[health]` activity declarer is the candidate that
already carries the role in practice.

### Deprecate rather than remove, with a warning on load

Rejected by the compatibility policy: pre-1.0, breaking changes ship with a
one-time migration, not a compatibility shim. A warning path also keeps every
retired field alive in the loader — the exact cost the retirement is meant to
remove — and trains authors to ignore load output.

### Silently ignore retired keys

Rejected because it reproduces the failure this decision is correcting.
`primary` was declared in good faith for an entire arc precisely because a
dropped key looks like an accepted one. A named load error is what makes a
retirement observable to the author who declared the key.
