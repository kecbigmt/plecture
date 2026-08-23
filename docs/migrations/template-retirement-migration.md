# Template engine retirement migration

The transitional Go-template render pass is gone. A task document's
instruction body now supports exactly one `{{ ... }}` form: a bare
`{{ dotted.path }}` projection, resolved and validated the same way any other
`from` projection in the configuration language is. Conditional blocks
(`{{if}}`/`{{end}}`), the `get` default-argument helper, and `$var` bindings
are no longer executed. A document that still carries one of those forms now
fails to load with `PLECTURE-CFG-TASK-INSTRUCTION-CONTROL-FLOW`, naming the
document and instruction field — it does not silently render the literal
template source or drop the block.

Decided in [`docs/adr/2026-08-23-template-retirement.md`](../adr/2026-08-23-template-retirement.md)
(approach 1 of the linked report: full retirement, no replacement
control-flow language for instruction bodies) and specified by
[`docs/language/tasks.md`](../language/tasks.md). This repository's own
shipped catalog (`plugins/github`, `plugins/okf`) is already migrated as part
of the same change.

Also retired: the `plect template render`/`plect template list` commands,
the `plect_template_list` MCP tool, and the `app/internal/template` package
that backed them. Nothing in the running system reads a `templates/`
directory (`~/.config/plect/templates/`, a repository's `.plect/templates/`
overlay, or a plugin's `config/templates/`) any more.

The change is intentionally breaking. Plecture is pre-1.0, so config authors
migrate once instead of relying on a shim that keeps the retired engine
alive.

## Backup

```bash
STAMP=$(date -u +%Y%m%dT%H%M%SZ)
cp -a ~/.config/plect "$HOME/.config/plect.migration-backup.$STAMP"
[ -d .plect ] && cp -a .plect ".plect.migration-backup.$STAMP"
```

## Find every task document still carrying Go-template control flow

```bash
grep -rnE '\{\{-?\s*(if|else|end|range|with|define|template|block)\b|\{\{-?\s*get\b|\{\{-?\s*\$' \
  ~/.config/plect .plect
```

## The optional-instruction append idiom

The pattern every shipped task document carried is the one most user-owned
task documents will have too: a paragraph that only appears when a caller
supplied free-form extra instructions.

```toml
# before
instructions = [{ text = """
Do the work.
{{- if get .Inputs "instruction" ""}}

Additional instructions: {{get .Inputs "instruction" ""}}
{{- end}}
""" }]

[review.inputs_schema.properties]
instruction = { type = "string" }
```

```toml
# after
instructions = [{ text = """
Do the work.

Additional instructions from the dispatcher (may be empty): {{ inputs.instruction }}
""" }]

[review.inputs_schema.properties]
instruction = { type = "string", default = "" }
```

The two forms do not render identically. The old form omitted the whole
paragraph — including its label — when no instruction was supplied. The new
form always renders the label, with nothing after the colon when the input
is absent. A dispatcher that reads this instruction text programmatically
(rather than as agent prompt prose) must account for the label always being
present.

Giving the schema property a `default` is what makes the plain projection
resolve instead of erroring: `{{ inputs.<key> }}` has no syntax of its own
for "render as this when absent," so a property that needs that behavior
declares it once, in its own schema.

## `claude_initial_prompt`/`codex_initial_prompt`'s `template` input

These two shipped effects used to resolve their `template` input (a name) by
shelling out to `plect template render "$TEMPLATE" --session "$session_name"`
from inside their own setup script. That subprocess call is gone along with
the command it called. The input is renamed to `prompt`, and now takes the
already-resolved text to type into the pane directly — no name-to-text
resolution happens inside the effect any more.

```toml
# before
[[node]]
task   = "official/claude.claude_initial_prompt"
inputs = { template = "work", terminal_ready = "..." }
```

```toml
# after
[[node]]
task   = "official/claude.claude_initial_prompt"
inputs = { prompt = { from = "<wherever the resolved text now lives>" }, terminal_ready = "..." }
```

Wire `prompt` to wherever the text that used to live in the named template
file now lives — most likely a task document's own instruction, if the
prompt text used to come from `work.md`/`review.md`/`investigate.md`/
`respond.md` under the old `templates/` directory (see the optional-instruction
section above for how those documents render their own instruction text
now); the exact root path depends on how the wiring around this node
delivers that text, which this migration does not prescribe. A workflow that
stops passing `template` and passes nothing in its place sends no initial
prompt at all, silently — `prompt` behaves the same way an absent/empty
`template` did: no text, no send.

## Other control-flow shapes

A conditional block that chose between two variants of prose (a gate-specific
review vs. a generic one, an owner-specific paragraph, and similar) has no
single-document replacement — instruction bodies do not gain a replacement
control-flow construct. Split the document instead: one document per variant,
with `extends` composing shared fields, and let the enabled workflow or chain
choose which variant's task id to dispatch. See
[`docs/language/tasks.md`](../language/tasks.md)'s Extension section and
`plugins/github/config/tasks/review.toml`/`review.md` for a worked instance of
a document already split this way.

## Verify

```bash
plect workflow list
plect task show <id>
```

A remaining Go-template construct fails at config load with
`PLECTURE-CFG-TASK-INSTRUCTION-CONTROL-FLOW`, naming the task document and
instruction field — before any session ever dispatches against it.
