# Expressions

Plecture embeds CEL as an external language. It specifies the profile CEL is
embedded in, not CEL's grammar or semantics.

The target is the CEL language as specified by
[cel-spec](https://github.com/cel-expr/cel-spec).

## The profile

- Standard CEL syntax and operators.
- Standard primitive, list, map, object, string, numeric, boolean, and null
  values.
- The standard macros: `has`, `all`, `exists`, `exists_one`, `map`, `filter`.
- No Plecture custom functions.

Each expression site defines the variables visible at it, the expected result
type, and any Plecture-specific restriction on references. An expression is
parsed and checked ahead of evaluation against that site's environment.

<!-- fixture: expressions/macros.toml -->
```toml
[notify]
kind    = "channel"
type    = "exec"
command = "curl"
args = [
  "-d",
  { json = { text = { expr = "has(event.metadata.url) ? event.summary + ' (' + event.metadata.url + ')' : event.summary" } } },
]
```

The macros need no Plecture extension to be useful:

<!-- fixture: expressions/comprehension.toml -->
```toml
[team_runtime]
kind = "task"

[team_runtime.setup]
type = "exec"
bin  = "agent-runtime"
args = ["launch"]

[team_runtime.outputs.bind]
mcp_server_names = { expr = "inputs.mcp_servers.map(s, s.name).join(',')" }

[team_runtime.outputs_schema]
type = "object"

[team_runtime.outputs_schema.properties]
mcp_server_names = { type = "string" }

[team_runtime.inputs_schema]
type                 = "object"
additionalProperties = false

[team_runtime.inputs_schema.properties.mcp_servers]
type = "array"

[team_runtime.inputs_schema.properties.mcp_servers.items]
type     = "object"
required = ["name"]

[team_runtime.inputs_schema.properties.mcp_servers.items.properties]
name = { type = "string" }
```

## What stays out of CEL

Plecture concepts are structural in TOML, not CEL APIs: definition references,
executable ownership, terminal capabilities, task nesting, layer boundaries,
`from` projections, action bindings, and JSON serialization. CEL hosting
custom functions is not a reason to move any of them there.

An expression calling a function the profile does not define is
`PLECTURE-CFG-CEL-CUSTOM-FUNCTION` — including one that looks like a Plecture
capability:

<!-- fixture: expressions/custom-function.invalid.toml -->
```toml
[notify]
kind    = "channel"
type    = "exec"
command = "curl"
args    = [{ expr = "bin('codex-exec-enqueue')" }]
```

## Typing

Plecture may project the structural portion of a JSON Schema contract into CEL
type information, and check an expression's result type against the expected
type of its field.

That projection is the sanctioned first feature to drop if implementation
complexity grows. Static resolution of roots, references, and kinds is never
dropped. A check that is dropped becomes an accepted-invalid conformance
fixture, so what the language would reject stays recorded even while the
implementation accepts it:

<!-- fixture: expressions/type-mismatch.accepted-invalid.md -->
```markdown
+++
[review]
kind              = "work"
description       = "A work document whose computed leaf does not type-check"
resource_observer = "issue_pr"

[review.state_schema]
type = "object"

[review.state_schema.properties]
verdict_revision = { type = "string" }

[review.done_when]
all = [{ expr = "self.state.verdict_revision + 1" }]
+++
Review {{ resource.id }} and record a verdict.
```

Constraints that do not map naturally into the CEL type system — patterns,
enums, cross-field requirements — remain JSON Schema concerns and are enforced
at runtime validation.

## Computed nested outputs

A computed nested output binding produces a string. Typed computed nested
outputs are deferred; a computed observation in a work document may be typed
and validated against its declared key.
