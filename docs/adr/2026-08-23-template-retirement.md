---
supersedes: 2026-08-18-template-get-default-argument
---

# Retire the transitional Go-template render pass

## Context

A task document's instruction body rendered in two passes:
`lang.RenderInstruction` resolved `{{ dotted.path }}` projections against the
configuration language's own root vocabulary, and `template.RenderBody` then
ran the result through `text/template` for everything `bodyProjection` did
not match — conditional blocks, the `get` default-argument helper, and `$var`
bindings. `app/internal/lang/validate.go` named this a deliberate transitional
carrier: "control flow in an instruction body is an open decision," left open
because CEL (the language's expression form everywhere else) is
expression-only, and no conditional construct had been designed for prose
position.

An investigation (issue #248) inventoried every production site still
depending on the second pass: the six shipped task documents
(`plugins/github`'s `work`/`investigate`/`respond`/`review`, `plugins/okf`'s
`pursue_goal`/`goal_review`), user-global host templates under
`$CONFIG_HOME/templates/`, and the `plect template render`/`plect template
list` commands (plus the `plect_template_list` MCP tool) that rendered
template assets directly. It classified every non-projection construct by
shape and verified, by isolated test, that `{{ inputs.<key> }}` errors rather
than rendering empty when the key is absent — the second pass's `get` helper
was the only thing in the pipeline that tolerated absence.

That investigation's inventory was itself incomplete: it named every
consumer of the second render *pass*, but not every shell-out to the `plect
template render` *command*. `plugins/claude/config/tasks/claude_initial_prompt.toml`
and `plugins/codex/config/tasks/codex_initial_prompt.toml` — the effects that
type a session's initial prompt into a freshly booted agent pane — each call
`plect template render "$TEMPLATE" --session "$session_name"` from inside
their own setup shell script, to resolve a `template` input's name to
rendered text before typing it. That is a second, independent path into the
same retiring command, found only while auditing every reference to `plect
template` ahead of deleting it.

Every control-flow use found was one of two shapes:

- **Structural choice between prose variants** — a gate-specific review body
  vs. a generic one, an owner-specific paragraph in an orchestrator template.
  This is a document-identity decision, not a computation: which document to
  use is exactly the kind of choice `extends` and chain/workflow wiring
  already make.
- **Optional-instruction append** — a paragraph included only when a caller
  supplied free-form extra instructions, via
  `{{- if get .Inputs "instruction" ""}}...{{- end}}`. This is the one shape
  with no exact plain-projection equivalent, because a bare
  `{{ inputs.instruction }}` has no syntax for "render as this when absent."

## Decision

Retire the second render pass entirely. An instruction body supports exactly
one `{{ ... }}` form: the `{{ dotted.path }}` projection already validated
against the roots `docs/language/values.md` documents for `task.instruction`.
No replacement control-flow or defaulting construct is introduced for prose
position — `docs/language/tasks.md`'s open decision closes as "no control
flow in instruction bodies," not as "a different control-flow construct."

A `{{ ... }}` that is not a bare dotted path is a load-time diagnostic,
`PLECTURE-CFG-TASK-INSTRUCTION-CONTROL-FLOW`, not a form silently carried
through as literal text. Surfacing it as a diagnostic — the same choice
`PLECTURE-CFG-SHELL-INTERPOLATION` already makes for a similarly transitional
`{{` in shell scripts — means a plugin or config author discovers a stray
construct at load, before any session ever dispatches an agent against
rendered prose that still contains unexecuted template syntax.

The optional-instruction append converts to a plain, unconditional
projection, with the input's own `inputs_schema` property carrying a
`default`:

```toml
[review.inputs_schema.properties]
instruction = { type = "string", default = "" }
```

`bindDocumentInputs` (`app/internal/service/taskdoc.go`) applies that default
when the caller supplied no value, so `{{ inputs.instruction }}` always
resolves. This is ordinary JSON Schema `default` handling in the input-binding
step that already exists for every task document, not a new instruction-body
capability — the instruction body itself still only ever sees a resolved
value, never a conditional. The six shipped task documents, and the label
they show alongside the projection, are converted in the same change: "may be
empty" replaces the paragraph disappearing outright, which is a stated,
intentional behavior change (see the migration note).

The gate-specific/generic prose split was already converted to document
split plus `extends` composition ahead of this change (issues #282/#285), so
no further conversion of that shape was needed here.

`plect template render`, `plect template list`, the `plect_template_list` MCP
tool, and the `app/internal/template` package they were built on are deleted
outright rather than kept around for the templates they used to render:
nothing in the running system reads a `templates/` directory any more, per
the host-side conversion (devbox#918) that preceded this change.

`claude_initial_prompt`/`codex_initial_prompt`'s `template` input (a name to
resolve and render) becomes `prompt` (already-resolved text, delivered
as-is). The effect's job was always "type this text into a booted pane, with
paste-burst and submit retries for the agent CLI's TUI contract" — resolving
*what* text to type from a template name was incidental, done only because
`plect template render` existed to call. Renaming the input to accept
resolved text directly removes the dependency on the retired command without
removing any of the effect's own capability: a caller now supplies the text
through an ordinary `from` projection (a task document's own rendered
instruction, a workflow node's output, a literal), the same composition
mechanism every other input in the language already uses, rather than a
second, effect-private rendering step.

Whatever now supplies that text (most likely a task-document instance's own
`lang.RenderInstruction` output) is a host-side wiring decision, made outside
this repository — the effect declaration only needs the text handed to it,
not to know where it came from. Verifying that a host workflow actually wires
some source into `prompt` is out of this change's scope, tracked in the pull
request as a cross-repo follow-up rather than guessed at here.

## Consequences

- `app/internal/lang/validate.go`'s `instructionBody` check rejects any
  `{{ ... }}` that is not a bare dotted-path projection, at the semantic
  layer, alongside the existing root-membership check for paths it does
  recognize.
- `app/internal/service/taskdoc.go`'s `renderInstruction` calls only
  `lang.RenderInstruction`; the second `template.RenderBody` pass is gone,
  and so is the package.
- `app/commands/template.go` (the `plect template` command tree),
  `app/internal/mcpserver`'s `plect_template_list` tool, and
  `app/internal/service/templatevars.go` (the projection that fed
  `--session` template rendering) are deleted, along with their tests.
- No `text/template` import remains outside `app/internal/webui`, which uses
  `html/template` for its own, unrelated HTML rendering and was never part of
  this pipeline.
- `plugins/claude/config/tasks/claude_initial_prompt.toml` and
  `plugins/codex/config/tasks/codex_initial_prompt.toml` take `prompt`
  (resolved text) instead of `template` (a name to render); their shipped
  test fixtures (`testdata/effects/scenarios.toml`, the `bin/plect` stand-in,
  and the recorded `effect-invocations.txt`) are updated to match, and no
  longer invoke `plect` as a subprocess at all.
- `docs/migrations/template-retirement-migration.md` carries the procedure
  for user-owned config: the breaking optional-instruction-append behavior
  change, and how to find and convert a remaining Go-template construct.

## Alternatives considered

### Add a small task-body language feature for defaulted/optional projections or conditional sections

Rejected. It would preserve the optional-instruction append's exact
behavior, but at the cost of introducing a new configuration-language
construct for exactly the case `docs/language/tasks.md` had left open —
trading a transitional carrier for a permanent one, and reopening a decision
this change exists to close. Per `docs/design/README.md`, a new construct of
this kind needs its own ADR, worked examples, and validation rules; nothing
in the surveyed corpus needed exact behavior preservation badly enough to pay
that cost.

### Keep `RenderBody` but reduce its usage to the residual host templates

Rejected. It leaves `app/internal/template`, `plect template render`, and the
transitional validation comment alive indefinitely, contradicting the
direction that motivated the investigation in the first place: pass 2 was
already documented as transitional, not as a permanent second engine.

### Render a stray non-projection `{{ ... }}` as literal text instead of a diagnostic

Rejected. A stray `{{if ...}}` or `{{get ...}}` surviving into rendered
prose — visible to whatever reads the instruction, most often an agent — is a
worse failure mode than a load-time error: it is silent at config-load time
and only discovered once a session is already dispatched against confusing
prose. `PLECTURE-CFG-SHELL-INTERPOLATION` already establishes the precedent
of treating a stray `{{` in a similarly transitional position as a rejected
construct rather than inert literal text.
