# Plecture configuration language on TOML and CEL

## Context

Plecture configuration has grown beyond passive data.

Definitions describe tasks, workflows, channels, workspace providers, resource observers, environments, plugin-owned capabilities, task nesting, lifecycle actions, completion conditions, health, and cross-definition wiring. The config-declaration-identity design further makes the definition block, rather than its file or directory, the unit of meaning and gives definitions explicit kinds and statically resolvable references.

At the same time, dynamic configuration values currently rely heavily on Go `text/template`. Go templates are used for value projection, optional access, conditional formatting, plugin executable lookup, terminal-operation lookup, shell quoting, and references between workflow nodes. Some compiler behavior also inspects Go-template syntax to recover Plecture semantics, such as workflow dependencies and the distinction between a direct nested-task output projection and a computed output binding.

This has several costs:

* language semantics are coupled to a Go-specific template language;
* shell source and template evaluation are interleaved;
* simple projection and actual computation use the same syntax;
* Plecture-specific capabilities are exposed as template helper functions;
* some static checks require inspecting a general-purpose template AST after the fact;
* changing the implementation language or providing independent tooling would require reproducing Go-template behavior;
* authors and coding agents must reason simultaneously about TOML, Go templates, shell quoting, and Plecture semantics.

Plecture nevertheless does not need to own a new lexer, parser, formatter, or general-purpose expression language.

TOML already provides a stable serialization syntax. JSON Schema already provides the input, output, and locals contracts used by Plecture. CEL is designed as an embedded, side-effect-free expression language with parsing and type-checking independent from the host application's execution mechanisms.

The goal is therefore to treat Plecture config as a language of its own while building that language from established substrates:

```text
Plecture Configuration Language

  TOML
    structural representation

  JSON Schema
    data contracts

  CEL
    computation

  Plecture
    definitions, references, values, actions,
    lifecycle, layers, nesting, plugins, trust,
    validation, and composition semantics
```

This decision deliberately does not claim that the grammar described below is complete.

The design has been checked against representative configurations including the OKF goal bootstrap task, the Codex interactive runtime task, and the Codex terminal-submit channel, together with the existing task-nesting and plugin-packaging designs. Plecture has more configuration surfaces than those examples. Before implementation is considered complete, the repository's configuration definitions, template evaluation sites, helpers, validation rules, and runtime consumers must be inventoried against this model.

This ADR therefore ratifies the language direction and the core distinctions that subsequent syntax must preserve. It does not freeze every field or every allowed value form.

## Decision

### Plecture config is a language serialized as TOML

Plecture configuration remains valid TOML.

TOML is the serialization syntax, not the language semantics. Plecture defines which TOML structures form definitions, references, values, actions, bindings, schemas, and other domain objects.

Plecture does not introduce syntax that requires a custom TOML parser.

Definition identity, kinds, namespaces, layer behavior, reference resolution, plugin ownership, and trust continue to be Plecture semantics rather than CEL semantics.

Static topology must remain statically discoverable. Fields such as `kind`, `uses`, `workspace_provider`, and nested-task selection must not be computed by CEL.

### JSON Schema remains the contract language

Input, output, and locals contracts continue to use JSON Schema, inline in TOML or through schema files where supported.

For example:

```toml
[inputs_schema]
type                 = "object"
required             = ["owner"]
additionalProperties = false

[inputs_schema.properties]
owner     = { type = "string" }
assignees = { type = "string" }
```

JSON Schema remains authoritative for runtime data validation.

Plecture may project the structural portion of those schemas into CEL type information and other static-analysis structures. Constraints that do not map naturally into the CEL type system remain JSON Schema concerns.

Plecture-specific annotations such as output mutability may continue to extend a schema where doing so preserves interoperability with ordinary JSON Schema tooling.

### Values distinguish literals, projection, and computation

The common value model starts from three forms:

```toml
literal = "value"

required_projection = { from = "inputs.owner" }

optional_projection = { from = "inputs.model", default = "fable" }

computed = { expr = "inputs.left + inputs.right" }
```

A TOML literal is already a value and requires no wrapper.

`from` is a Plecture projection, not CEL syntax. It selects one statically identifiable value from the evaluation context.

A missing `from` source is an error unless `default` is declared. The projected value preserves its native type.

`expr` contains a CEL expression and is reserved for actual computation: conditionals, arithmetic, boolean operations, string construction, or other cases that cannot be represented as a direct projection.

A pure projection should use `from`, not `expr`.

This distinction is semantic rather than cosmetic. It allows Plecture to know directly that:

```toml
pid = { from = "inner.outputs.pid" }
```

is a projection, while:

```toml
label = { expr = '"pid-" + string(inner.outputs.pid)' }
```

is a computation.

That distinction is required by existing behavior such as dependency extraction and nested-task mutable-output routing.

The exact set of roots and paths accepted by `from` is surface-specific and must be specified after a complete inventory of the current render contexts.

### CEL is for computation, not Plecture capabilities

CEL must not become a second implementation of Plecture's domain model.

Plecture-specific concepts should be represented structurally in TOML where practical rather than accumulated as CEL custom functions.

The initial CEL profile should therefore use the standard CEL language with the smallest extension surface that the repository audit demonstrates is necessary.

In particular, ordinary Plecture concepts such as definition references, executable ownership, terminal capabilities, task nesting, and layer boundaries should not become arbitrary CEL functions merely because CEL can host custom functions.

Each CEL expression site defines:

* the variables visible at that site;
* the expected result type;
* any Plecture-specific static restrictions on references.

A surface should expose only the context it is allowed to observe. For example, an outer output binding may need only `inner` and `locals`; it should not receive a global context and then rely on a secondary validator to reject access to unrelated session or event state.

CEL performs expression parsing and type checking. Plecture performs Plecture-specific name resolution and semantic validation over the resulting expression.

### Actions become structured

Lifecycle execution should no longer require Plecture expressions to be interpolated directly into shell source.

Simple process execution is represented structurally. The goal-bootstrap task is representative:

```toml
scope = "run"

[setup]
type = "exec"
bin = "okf-goal"

args = [
  "task",
  "bootstrap",

  "--workspace-dir",
  { from = "workspace.dir" },

  "--owner",
  { from = "inputs.owner" },

  "--session",
  { from = "session.name" },

  "--assignees",
  { from = "inputs.assignees", default = "" },
]
```

This removes the need for shell quoting in the common case and makes the distinction between command structure and dynamic data explicit.

Plecture must still support shell actions. Existing tasks such as the interactive Codex runtime contain meaningful imperative logic that should remain inspectable and customizable in configuration rather than being hidden solely to avoid shell syntax.

A shell action therefore keeps literal shell source and declares the Plecture-side values or capabilities it needs outside that source:

```toml
[setup]
type = "shell"

[setup.bind]
session_name = { from = "session.name" }
send_text    = { terminal = "send_text" }
send_keys    = { terminal = "send_keys" }
capture      = { terminal = "capture" }

script = '''
# ordinary shell; no Plecture or CEL interpolation here
...
'''
```

`setup.bind` is an action binding: it binds values or capabilities across the Plecture/action boundary.

It is related to, but distinct in scope from, task-nesting wiring.

The exact mechanism by which shell-action bindings are exposed to the shell process is not fixed by this ADR. Environment variables, positional arguments, generated local assignments, or another transport must be evaluated against quoting, portability, security, and ergonomics before that part of the action contract is ratified.

The invariant fixed here is that shell source itself is literal. Plecture and CEL interpolation do not occur inside it.

### Task nesting becomes surface-oriented

Task nesting retains its existing semantic model: inner and outer tasks are homogeneous, nesting is additive, lifecycle execution is LIFO, private locals remain layer-local, and public inner outputs are exposed only through an explicit outer boundary.

The TOML shape should make the direction of each boundary clearer than the current `[bind.inputs]`, `[bind.outputs]`, and `[bind.env]` grouping.

The current preferred shape is:

```toml
[inner]
uses = "official.claude.runtime"

[inner.inputs]
tmux_session = { from = "inputs.tmux_session" }
model        = { from = "inputs.model", default = "fable" }
effort       = { from = "inputs.effort", default = "low" }
path_prepend = { from = "locals.guard_dir" }

[inner.env]
PLECT_TEAM_CONTEXT = { from = "session.name" }

[outputs.bind]
pid           = { from = "inner.outputs.pid" }
socket_path   = { from = "inner.outputs.socket_path" }
mcp_config    = { from = "inner.outputs.mcp_config" }
agent_session = { from = "inner.outputs.session_id" }
guard_dir     = { from = "locals.guard_dir" }
```

The structure reads from the surface being configured:

* `inner.inputs` defines the input object passed to the inner task;
* `inner.env` defines environment additions for the inner task;
* `outputs.bind` defines how the outer task's public output contract is projected or computed.

A direct `from = "inner.outputs.<key>"` remains distinguishable from a computed expression. Existing write-through behavior for mutable directly projected outputs can therefore be preserved without recovering that distinction from a template AST.

Whether a computed CEL output binding continues the current string-only behavior or may produce a typed CEL value validated against the output schema is not decided here. That question changes task-nesting semantics rather than merely syntax and requires a separate review.

### Plecture-specific tagged values are allowed where they represent domain semantics

TOML inline tables may represent discriminated Plecture values such as:

```toml
{ from = "inputs.owner" }
{ expr = "inputs.left + inputs.right" }
```

and, on surfaces that genuinely consume a capability:

```toml
{ terminal = "send_text" }
```

The language should prefer tagged values for non-computational Plecture semantics when the containing field does not already communicate that type.

This must not grow into an expression language encoded as TOML.

Arithmetic, comparison, boolean logic, conditionals, and string computation belong in CEL. New tagged forms require a Plecture-specific semantic reason; they are not introduced as substitutes for ordinary CEL operators.

Conversely, a dedicated field should be preferred where its position already determines its meaning. For example, an exec action may use:

```toml
bin = "okf-goal"
```

rather than wrapping the same fact in an additional generic value layer.

The complete tagged-value vocabulary is not ratified by this ADR. It must be derived from the config-surface audit rather than designed speculatively.

### Static validation is layered

The language should support static validation at three levels:

```text
TOML
  ↓
structural validation
  ↓
Plecture semantic validation
  ↓
CEL parsing and type checking
```

Structural validation covers such things as known fields, required fields, tagged-value shapes, and mutually exclusive variants.

Plecture semantic validation covers such things as:

* definition and plugin reference resolution;
* expected target kinds;
* valid `from` roots and fields;
* workflow node/output resolution;
* graph dependencies and cycles;
* task-nesting constraints;
* projection compatibility;
* capability availability;
* layer and trust rules.

CEL validation covers expression syntax and type correctness in the context made available to that expression site.

A machine-readable structural schema should be available to editors, tooling, and coding agents. It should be generated from the implementation's configuration model where practical rather than maintained as a second hand-written field reference.

Detailed language behavior should be exercised by conformance fixtures and compiler tests. Markdown documentation should focus on stable semantic rules and rationale rather than duplicating every decoder field.

## Scope

This decision governs TOML configuration definitions.

It does not by itself replace Go templating in Markdown instruction-template assets. Those assets may later share CEL expression semantics, but their interpolation and authoring model are a separate decision.

This decision also does not redefine the existing runtime semantics of plugins, task nesting, lifecycle, completion, health, chains, or trust except where an explicit follow-up decision says otherwise. The initial migration should preserve those semantics while changing their configuration representation.

## Required follow-up

Before the language shape is considered complete, implementation work must inventory:

1. every TOML configuration kind and field;
2. every Go-template evaluation site used by TOML config;
3. every template helper and its current semantics;
4. every render context and missing-value policy;
5. every place the compiler currently extracts references from template syntax;
6. every shell boundary where template rendering currently affects quoting or injection safety;
7. every shipped plugin configuration, including nested tasks, channels, workspace providers, resource observers, health probes, terminal operations, dynamic outputs, and chains;
8. user-owned and workspace-overlay configuration rules affected by the new value representation.

That inventory must classify each existing dynamic use as one of:

* a literal;
* a static Plecture reference;
* a `from` projection;
* a CEL computation;
* a Plecture capability;
* an action-local binding;
* behavior that should remain literal shell;
* behavior requiring a separate language or semantic decision.

The audit must specifically settle:

* the complete `from` path vocabulary for each surface;
* an absence-propagating optional projection (a missing source omits the key instead of supplying an empty-string sentinel), consistent with absent-source bindings in task nesting;
* the CEL version and Plecture CEL profile;
* whether any CEL custom functions are actually necessary;
* the complete tagged-value vocabulary;
* action binding transport and quoting semantics;
* exec `command` versus plugin-owned `bin` rules;
* static typing derived from JSON Schema;
* computed nested-output typing;
* the dissolution of dynamic outputs into the value model. The audit
  classifies every existing `[[outputs]]` entry as a projection, a
  computation, or an action residue; if the residue is empty, no
  outputs-action construct is introduced, and re-evaluation semantics ride
  on root liveness rather than on a separate syntax group. The two shipped
  variants are expected to dissolve as follows.

  The `from_resource_status` bulk copy becomes per-key projections:

  ```toml
  # current
  [[outputs]]
  produces             = ["resource_kind", "checks_status", "revision"]
  from_resource_status = true

  # after
  [outputs.bind]
  resource_kind = { from = "resource.status.resource_kind" }
  checks_status = { from = "resource.status.checks_status" }
  revision      = { from = "resource.status.revision" }
  ```

  A script-computed output becomes a CEL computation over live roots:

  ```toml
  # current: a shell script comparing plect resource status output
  # against {{.Self.verdict_revision}} and echoing true/false

  # after
  [outputs.bind]
  verdict_current = { expr = "self.verdict_revision == resource.status.revision" }
  ```

  A value whose `from` or `expr` reads a live root (`resource.*`,
  `self.*`) is current as of each evaluation; a direct projection of an
  inner output keeps write-through semantics. Nothing about being
  "dynamic" needs its own declaration form;
* all migration interactions with the config-declaration-identity change
  and with the symmetric per-layer evaluation direction (#192);
* whether existing config can be migrated mechanically and which cases require manual review.

Representative configs used during this ADR are evidence for the direction, not evidence of completeness.

## Consequences

Plecture owns a configuration language without owning a general-purpose parser or expression engine.

TOML remains familiar and widely tooled. JSON Schema remains reusable outside the Plecture implementation. CEL removes Go `text/template` from the computational semantics of TOML config.

Simple configuration should become simpler rather than more expression-heavy. Most wiring is expected to use literals and `from`; CEL appears only when a value is actually computed.

Plecture-specific semantics become more explicit. A projection is represented as a projection rather than inferred from a general template expression. Action capabilities can be declared at the action boundary rather than spliced into shell text. Static topology remains available before evaluation.

Shell remains available where it is genuinely useful, including inspectable plugin behavior such as interactive-runtime startup and readiness logic. The language does not force such behavior into opaque helper executables merely to avoid template syntax.

The implementation gains new compilation work: tagged-value decoding, CEL parsing/checking, surface-specific evaluation contexts, structural schema generation, and migration tooling. It loses the need to treat Go template syntax as Plecture's implicit expression language and should progressively remove template-AST-specific semantic analysis from TOML config compilation.

The language reference should remain thin. The implementation model, generated structural schema, conformance fixtures, and compiler tests are the detailed executable specification; ADRs and design documents record stable semantics and rationale.

## Alternatives considered

### Keep TOML and Go templates

This has the smallest migration cost.

It keeps Plecture's expression semantics coupled to Go, continues to mix shell and template evaluation, and requires Plecture to recover domain semantics from Go-template syntax. It does not meet the portability and static-analysis goals.

### Use HCL

HCL provides a strong native configuration and expression model and would make some configuration more concise.

It also introduces another complete structural and expression language where Plecture already has mature TOML configuration, JSON Schema contracts, and substantial TOML-oriented semantics. Shell-heavy configuration must also coexist with HCL interpolation syntax.

TOML plus a deliberately constrained CEL sublanguage provides the required computation model while changing less of Plecture's representation and ecosystem.

### Create a custom textual syntax

A purpose-built syntax could make tasks, schemas, references, and bindings more concise.

It would make Plecture responsible for parsing, formatting, editor support, syntax evolution, and another language corpus for human and model authors. The current requirements do not justify that cost before TOML plus typed Plecture structures and CEL have been exhausted.

### Represent every dynamic value as CEL

For example:

```toml
model   = { expr = "inputs.model" }
command = { expr = 'bin("okf-goal")' }
```

This gives one uniform expression mechanism but makes simple wiring verbose and hides distinctions Plecture needs to understand statically.

A projection is not merely a trivial computation. It carries useful semantics for dependency analysis, native type preservation, and task-nesting write-through. Plecture capabilities are likewise not ordinary computations.

CEL is therefore reserved for computation, with simpler Plecture structures used for non-computational semantics.

### Hide complex terminal behavior in plugin executables

Moving Codex terminal submission, startup, and readiness logic into dedicated executables would reduce configuration complexity.

It would also make important plugin behavior less inspectable and less customizable, despite the existing terminal abstraction allowing that behavior to compose independently of a concrete multiplexer.

Plecture instead keeps a structured way for actions to consume terminal capabilities while removing Plecture interpolation from shell source.

### Hand-maintain a complete prose language reference

A complete prose mirror of every accepted field and validation rule would quickly compete with the implementation as a source of truth.

Plecture instead records stable language semantics in design documents, exposes a machine-readable structural schema from the implementation where practical, and uses conformance fixtures and compiler tests as the detailed executable specification.

