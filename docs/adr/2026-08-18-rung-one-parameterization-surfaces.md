# Rung-1 parameterization surfaces for channels and workspace providers

## Context

The customization ladder in
[`../design/task-nesting.md`](../design/task-nesting.md) places
author-declared parameterization at rung 1, below task nesting and forking,
and the plugin-counterpart shadow mapping assigns ten concrete parameters to
that rung across the shipped `claude`, `codex`, and `github` plugins.

Task definitions already have the declaration surface rung 1 needs:
`[inputs_schema]` on the definition, `inputs` on the workflow node. Four of
the mapped parameters land there directly — `launch_env` on the two runtime
tasks and the exec worker, `state_root` on the exec worker.

The other two config kinds the mapping names do not. A channel declares
`[input_schema]`, but every input is effectively required: delivery renders
under `missingkey=error`, so a definition that references an optional
parameter fails outright when a workflow leaves it unset, and a channel's
`timeout` is a decoded duration with no path from an input at all. A
workspace provider has no input surface whatsoever — its hooks see the
resource, the session, the workspace-dirs root, and the session inputs, none
of which a plugin author can declare or validate.

Without a surface on those two kinds, six of the ten mapped parameters have
nowhere to live, and the shadows they exist to retire stay on rung 3.

## Decision

Add the two missing surfaces, both shaped after the ones that already exist.

**Channels gain declared defaults and a resolved timeout.** A
`[input_schema]` entry may declare `default`, which delivery fills in for a
key the referencing `[[event.channel]]` left unset; `required` and `default`
together are a load error. `timeout` becomes a template over `.Inputs`,
resolved and parsed per delivery — a literal like `"5s"` is a template with
no actions, and a literal that does not parse as a duration is a load error.
Rendering the timeout sees `.Inputs` and not `.Event`: the per-attempt
deadline is a property of the wiring, and letting one event's payload move it
would make delivery timing an attacker-influenced value.

**Workspace providers gain `[inputs_schema]`**, the same JSON Schema document
task definitions carry, and workflows set it through a
`[workspace_provider_inputs]` table. Values reach `setup` and `cleanup` as
`.Inputs`. They are literal data, not templates: a workspace provider hook
runs before any workspace exists, so there is no node output for a parameter
to interpolate. A workflow that sets a parameter against a provider declaring
no schema is a load error rather than a silently discarded value, so a
misspelled key never reads as configured. The `subscribe` hook does not
receive them — it resolves a provider from the resource alone, with no
workflow in scope to have set one.

Schema defaults are deliberately not applied on the workspace provider side.
A hook reads an unset parameter through the `get` helper's default argument,
which keeps the fallback in the one place that can act on it — the plugin
executable behind the hook — rather than splitting it between a schema and an
implementation that must agree.

The naming parameters this enables (`issue_branch_template`,
`tagged_branch_suffix`, and the codex channel's `message_envelope`) use
single-brace placeholders expanded by the plugin's own executable. They are
values that plect renders before the executable ever sees them, so Go
template actions inside them would be consumed by the wrong pass; expanding a
fixed, author-declared placeholder set in the executable also keeps a
user-supplied format string from becoming a second template pass over event
data.

## Consequences

All ten parameters the shadow mapping names are now declarable, and the four
config kinds involved — tasks, channels, workspace providers, and resource
definitions — express author-declared variation the same way: a schema on the
definition, values on the workflow.

Three surfaces gain a knob whose only present consumer is one shipped plugin:
channel input defaults, channel timeout resolution, and workspace provider
inputs. Each is data-shaped by construction — a default is a literal, a
timeout parses as a duration or fails, and a provider parameter is validated
against a schema the author wrote — so none of them widens what a config can
make plect run.

A channel's `timeout` is now a string rather than a decoded duration. Existing
declarations keep parsing unchanged, so no migration is required.

The GitHub resource's `review_decision` state key costs one extra GraphQL read
per pull request observation. It is best-effort: a failure degrades to the
`NULL` sentinel rather than failing an observation whose checks and
mergeability state came from REST reads that succeeded.

## Alternatives considered

### Put the workspace provider's parameters in the global config layer

A `[workspace_provider_inputs.github]` table in `config.toml` would set the
parameters machine-wide without touching workflows. It splits the wiring of
one workflow across two files, and it cannot express two workflows on the same
provider wanting different layouts — which is the case the tagged-session
convention exists for.

### Make `message_envelope` a Go template rendered against the event

This is what the shipped channel's argument list already is, so exposing it as
an input reads as the smaller change. It is not: channel inputs are rendered
in a pass that has no event, so the value would have to be rendered a second
time, with event data, as a template a user supplied. That turns a formatting
parameter into a template-injection surface over the whole render context.

### Apply JSON Schema `default` to workspace provider inputs

Filling defaults from the schema would let a hook reference `.Inputs.<key>`
directly. It puts the default in a second place that must stay in step with
the executable's own fallback, and a schema default cannot express one that is
computed (`${TMPDIR:-/tmp}`). The `get` helper's default argument covers the
same ground without the duplication.

### Leave the six parameters unimplemented and fork the two config kinds

Whole-file shadowing already works for channels and workspace providers, and
the ladder keeps forking available. It is the outcome rung 1 exists to avoid:
a user who copies a shipped channel to change one timeout stops receiving
every later improvement to the rest of the file.
