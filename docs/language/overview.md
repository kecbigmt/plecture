# The Plecture configuration language

Plecture configuration is a language of its own, built from established
substrates. TOML is its serialization syntax — for a work document, as
frontmatter in a Markdown file — JSON Schema its contract language, and CEL its
computation sublanguage. Everything else — definitions,
identity, references, values, actions, lifecycle, layers, nesting, plugins,
trust, and composition — is Plecture semantics.

```text
TOML                structural representation
JSON Schema         data contracts
CEL                 computation
Plecture            definitions, references, values, actions, lifecycle,
                    layers, nesting, plugins, trust, validation, composition
```

Plecture introduces no syntax that requires a custom TOML parser, and no
general-purpose expression language of its own.

## The chapters

| Chapter | Subject |
|---|---|
| [`work.md`](work.md) | The work document: the completion contract and the instruction |
| [`chains.md`](chains.md) | Chains: what a work document spawns, and when |
| [`tasks.md`](tasks.md) | The task kind: lifecycle, health, terminal, and nesting |
| [`workflows.md`](workflows.md) | The workflow kind: nodes, event channels, display, and the clocks |
| [`channels.md`](channels.md) | The channel kind and its delivery primitives |
| [`workspace-providers.md`](workspace-providers.md) | The workspace provider kind |
| [`resource-observers.md`](resource-observers.md) | The resource observer kind |
| [`declarations.md`](declarations.md) | Definition blocks, discovery, namespaces, and the reference grammar |
| [`values.md`](values.md) | The five value forms, the tagged-value vocabulary, and the per-surface roots |
| [`expressions.md`](expressions.md) | The Plecture CEL profile |
| [`actions.md`](actions.md) | `exec` and `shell` actions, `bin` versus `command`, and the binding transport |
| [`plugins.md`](plugins.md) | Plugin and catalog manifests |
| [`config.md`](config.md) | The reserved root files |

Work comes first because work is what the system is for: Plecture gives
autonomous work a place to go, and tasks and workflows are the housing that
gives it somewhere to run.

## Layers of specification

Four layers carry the language, with deliberately different responsibilities.

| Layer | Authoritative for | Source of truth |
|---|---|---|
| Semantic specification | Meaning, invariants, composition rules, security properties | This directory |
| Structural schema | Accepted TOML shapes, field types, required fields, discriminated variants | [`../../plecture.schema.json`](../../plecture.schema.json) |
| Executable specification | Exact behavior at valid, invalid, and boundary cases | [`../../testdata/config-language/`](../../testdata/config-language/) |
| Rationale | Why a language decision was made | [`../adr/`](../adr/) |

The chapters here stay thin. A construct subtle enough to need a long
paragraph also has conformance fixtures that make its cases executable, and
every chapter's worked example is one of those fixtures quoted verbatim.

`plecture.schema.json` is hand-written and authoritative for structural shape
until the implementation lands a schema generator whose output is
conformance-tested against it. From that point generation is authoritative and
the hand-written copy is retired.

## Three validation layers

```text
TOML
  ↓
structural validation      the configuration schema
  ↓
Plecture semantic validation   the compiler
  ↓
CEL parsing and type checking   the expression site's environment
  ↓
compiled Plecture representation
```

Structural validation covers known fields, required fields, tagged-value
shapes, action variants, and mutually exclusive combinations. Plecture
semantic validation covers reference resolution, expected kinds, available
`from` roots, graph shape, nesting constraints, capability availability, and
layer and trust rules. CEL validation covers expression syntax and types in
the environment declared for that site.

The implementation may reorder these for efficiency; the responsibility
boundaries do not move.

Static resolution — references, kinds, and `from`-root existence — is never
dropped. Projecting JSON Schema into CEL type information, and checking an
expression's result type against its field, is the first thing dropped if
implementation complexity grows; a dropped check is recorded as an
accepted-invalid conformance fixture rather than forgotten.

## Diagnostics

Diagnostics are part of the tooling interface. A code identifies the language
rule that was broken and the layer that found it; the human-readable message
may improve over time.

Codes carry the `PLECTURE-` prefix because they belong to the language, not to
one program. Any implementation emits them — an editor server, an independent
checker, a generator — so they sit on the Plecture side of the project's naming
split: prose about the language and its specification is Plecture, while
`plect` names the command, its environment, and its state. A code prefixed with
the CLI's name would claim the language's rules for one of its consumers.

| Code | Layer | Rule |
|---|---|---|
| `PLECTURE-CFG-KIND-MISSING` | structural | A definition table declares no `kind`. |
| `PLECTURE-CFG-KIND-UNKNOWN` | structural | `kind` is outside the vocabulary. |
| `PLECTURE-CFG-ID-INVALID` | structural | A definition id does not match `^[A-Za-z_][A-Za-z0-9_]*$`. |
| `PLECTURE-CFG-FIELD-UNKNOWN` | structural | A field is not part of the containing kind's surface. |
| `PLECTURE-CFG-FIELD-REQUIRED` | structural | A required field is absent. |
| `PLECTURE-CFG-FIELD-TYPE` | structural | A field's TOML shape is not the one its surface accepts. |
| `PLECTURE-CFG-VALUE-FROM-AND-EXPR` | structural | One value declares both `from` and `expr`. |
| `PLECTURE-CFG-VALUE-DEFAULT-AND-OPTIONAL` | structural | `default` and `optional` are mutually exclusive. |
| `PLECTURE-CFG-VALUE-TAG-UNKNOWN` | structural | A tagged value uses a key outside the vocabulary. |
| `PLECTURE-CFG-VALUE-TAG-SURFACE` | structural | A capability tag appears on a surface that consumes data only. |
| `PLECTURE-CFG-ACTION-TYPE-UNKNOWN` | structural | An action's `type` is neither `exec` nor `shell`. |
| `PLECTURE-CFG-ACTION-VARIANT` | structural | An action carries a field belonging to the other variant. |
| `PLECTURE-CFG-ACTION-BIN-AND-COMMAND` | structural | An exec action names its executable through `bin` or `command`, exactly once. |
| `PLECTURE-CFG-SHELL-INTERPOLATION` | structural | Shell source is literal; it carries no Plecture or CEL interpolation. |
| `PLECTURE-CFG-REF-DYNAMIC` | structural | A statically discoverable field carries a computed value. |
| `PLECTURE-CFG-CHANNEL-TIMEOUT-ROOT` | structural | A channel `timeout` reads author-declared parameters only. |
| `PLECTURE-CFG-WORK-FRONTMATTER-MISSING` | structural | A work document does not open with `+++` frontmatter. |
| `PLECTURE-CFG-WORK-BLOCK-COUNT` | structural | A work document's frontmatter holds other than exactly one declaration. |
| `PLECTURE-CFG-WORK-IN-TOML-DOCUMENT` | structural | A TOML definition document declares a kind whose declaration needs a body. |
| `PLECTURE-CFG-UNKNOWN-REF` | semantic | A reference resolves to no definition. |
| `PLECTURE-CFG-KIND-MISMATCH` | semantic | A reference site's expected kind differs from the target's declared kind. |
| `PLECTURE-CFG-ID-DUPLICATE` | semantic | One layer declares the same definition id twice. |
| `PLECTURE-CFG-REF-ALIAS-REQUIRED` | semantic | A user-owned reference to catalog content omits its catalog alias. |
| `PLECTURE-CFG-REF-CROSS-PLUGIN` | semantic | A plugin-owned reference names a catalog alias or another plugin's ownership segment. |
| `PLECTURE-CFG-FROM-ROOT` | structural / semantic | A projection names a root the containing surface does not observe. It is structural where a surface's roots are a fixed prefix set, as on a task's `outputs.bind` and a work document's `observe`, and semantic otherwise. |
| `PLECTURE-CFG-FROM-PATH` | semantic | A projection names a field the resolved contract does not declare. |
| `PLECTURE-CFG-RESOURCE-MISMATCH` | instantiation | An instance's resource does not resolve to the resource definition its work document declares. |
| `PLECTURE-CFG-REQUIRES-UNDECLARED` | semantic | A `done_when` check, or a `requires` entry, names a key no contract declares. |
| `PLECTURE-CFG-BIN-UNKNOWN` | semantic | An executable reference resolves to no declared executable. |
| `PLECTURE-CFG-TERMINAL-UNAVAILABLE` | semantic | A terminal capability is consumed where no task in the plan declares that verb. |
| `PLECTURE-CFG-NESTING-CYCLE` | semantic | A nesting chain reaches itself. |
| `PLECTURE-CFG-NESTING-OUTPUT-MUTABLE` | semantic | A computed nested output is marked mutable. |
| `PLECTURE-CFG-NESTING-PROJECTION-MISMATCH` | semantic | A direct nested projection disagrees with the inner output's type or mutability. |
| `PLECTURE-CFG-WORKFLOW-CYCLE` | semantic | The dependencies derived from node projections form a cycle. |
| `PLECTURE-CFG-CEL-SYNTAX` | cel | An expression does not parse as CEL. |
| `PLECTURE-CFG-CEL-UNKNOWN-NAME` | cel | An expression names a variable not visible at its site. |
| `PLECTURE-CFG-CEL-TYPE` | cel | An operation, or a result type, does not satisfy the site's expected type. |
| `PLECTURE-CFG-CEL-CUSTOM-FUNCTION` | cel | An expression calls a function the profile does not define. |

## Scope

This language governs configuration definitions in two file forms: TOML
documents, whose top-level tables are definition blocks, and work documents,
which are Markdown files with TOML frontmatter.

A Markdown file that carries no `+++` frontmatter is a template asset rather
than a definition. Template assets keep their own interpolation model, and a
work document's instruction body is the one place where that model and this
language meet — see [`work.md`](work.md).
