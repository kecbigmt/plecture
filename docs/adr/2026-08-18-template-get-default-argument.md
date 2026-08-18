# Mandatory default argument on the `get` template helper

## Context

Plect renders config templates with `missingkey=error`. A direct field
reference (`{{.SessionInputs.model}}`) is therefore the strict form: an absent
key fails loud. That is the right default for a wiring language, where
required-by-default is the honest marking.

`get` is the optional-access escape hatch. Its two-argument form returned the
empty string when the key was absent, which produced two defects.

The first is a recurring three-action idiom for one intent. Expressing "use the
supplied model, otherwise `fable`" required naming the lookup twice and
threading it through a conditional:

```toml
inputs.model = '{{if get .SessionInputs "model"}}{{get .SessionInputs "model"}}{{else}}fable{{end}}'
```

This idiom is the observed need: it recurs in shipped catalog config and in
user-owned workflows.

The second is that empty-on-missing is itself a default — an invisible one. A
reader at the call site could not tell what an absent key would produce; the
rule lived in the helper, not in the template.

Every modern language that ships a map accessor makes the default-bearing form
first class: Elixir `Map.get/3`, Python `dict.get(k, default)`, Swift
`dict[key, default:]`, Kotlin `getOrDefault`, Rust `unwrap_or`, TypeScript `??`,
Zig `orelse`. `get` as the name of the safe accessor is likewise the mainstream
convention, so the name is not in question.

Those languages all keep a no-default form as well, but only because it returns
a reified absence — `None`, `Option`, `null` — that the caller can distinguish
from a present value. Plect templates have no option type: the two-argument
`get` collapsed absence into an empty string, indistinguishable from
present-but-empty.

## Decision

`get` takes exactly three arguments: `{{get m "key" "default"}}`. It yields the
value when the key is present and non-nil, and the default otherwise. There is
no two-argument form; a two-argument call fails at render with an error naming
the failing template site.

A mandatory default is not an imitation of the cited precedent — it is the
compensation for the missing reified absence. Without an option type, the only
honest way to handle absence is to make the caller state, at the site, what
absence produces.

Edge semantics are fixed as follows:

- **Absent key** yields the default.
- **Present but nil** yields the default. A template has no rendering for nil
  other than the `<no value>` literal this helper exists to avoid.
- **Present but empty string** yields the empty string, not the default. An
  empty value is a value; a default that also fired on `""` would reintroduce
  the invisible rule this change removes.
- **Non-string values** — the map value and the default alike — are returned
  unchanged and rendered by the template engine's own formatting, so
  `{{get .Inputs "pid" 0}}` is well defined.

Presence testing stays expressible with an explicit empty default:
`{{if get .Prev "sent" ""}}`.

Arity is enforced at render time. Go's `text/template` resolves function arity
during execution, not during parse, so a parse-only load-time pass cannot catch
a two-argument call without a template-walking validator that does not exist
today. That validator is not worth its maintenance cost for one helper: the
repository's shipped catalog is already rendered end to end by
`TestShippedCatalog_TasksRender`, which fails on a two-argument call in shipped
config.

Template-bearing TOML values are written as TOML literal strings
(single-quoted), so the double quotes a template action needs around its key
and default arguments require no escaping:

```toml
inputs.model = '{{get .SessionInputs "model" "fable"}}'
```

When a value must itself contain a single quote, it falls back to a TOML basic
string with the necessary escapes.

## Consequences

Every optional access reads its own failure mode at the site. The shipped
idiom above collapses to one action:

```toml
inputs.model = '{{get .SessionInputs "model" "fable"}}'
```

The two forms render identically for the defaulting cases they were written
for — absent key yields `fable`, present key yields its value. They diverge on
present-but-empty, where the conditional's truthiness test yielded `fable` and
the three-argument form yields the empty string. That divergence is the point:
the collapsed form says what it does.

Pre-1.0 compatibility policy applies. There is no two-argument shim. Every
two-argument call in this repository's shipped plugin config, templates, and
docs is rewritten in the same change, and
`docs/migrations/template-get-default-argument-migration.md` carries the
procedure for user-owned config.

`docs/design/template-vocabulary.md` specifies the resulting helper.

## Alternatives considered

### Keep the two-argument form as a compatible subset

Rejected. It preserves the hidden default the change exists to remove, and
leaves two ways to write one intent. The pre-1.0 policy prefers a direct
migration over a compatibility shim, and the same one-way-to-do-it principle
already retired the `expose` shorthand.

### Introduce a reified absence value (nil) and keep a two-argument form

Rejected. This is the mechanism the cited languages actually rely on, but
importing an option type into a wiring language fails the benefit/complexity
bar: every consumer of `get` would have to learn to test and unwrap it. Go
template nil in a string context also creates fresh traps — `<no value>` in a
rendered shell command is worse than a wrong-but-visible default.

### Rename the helper to `optional`

Rejected. `get` as the name of the safe map accessor is the mainstream
convention across the cited languages; renaming would cost every reader's
existing intuition and buy nothing that the mandatory default does not already
buy.

### A sprig-style `default` pipeline (`{{get m "k" | default "d"}}`)

Rejected. It introduces a second vocabulary for one intent, and it reads
value-first: the reader meets the lookup before learning that a default
applies at all. The three-argument call keeps both facts in one action, in
reading order.

### An operator form (`??`, `orelse`)

Unavailable. Go templates have no infix operator syntax and no mechanism to add
one.
