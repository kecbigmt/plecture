# Config-language conformance fixtures

These fixtures are the executable specification of the Plecture configuration
language. Prose in [`../../docs/language/`](../../docs/language/) carries
meaning; this directory carries exact behavior at valid, invalid, and boundary
cases.

They are deliberately implementation-independent, so the same corpus can serve
the compiler, an editor server, migration tooling, schema-generation
verification, and an alternative implementation.

They are not a migration answer sheet. Every shipped plugin configuration was
translated to this shape once, to verify that each dynamic use maps onto a
class the audit defines; those translations were verification evidence rather
than fixtures, and migration tooling regenerates them against then-current
config when migration happens. The exercise's result is recorded on the audit
issue, and the residues it surfaced are owner calls on the specification PR.

## Layout

| Directory | Area |
|---|---|
| `work/` | The work document: its id, contracts, observation, and instruction body |
| `values/` | The five value forms and the tagged-value vocabulary |
| `references/` | Declaration identity, ids, and the dotted reference grammar |
| `actions/` | `exec` and `shell` actions, `bin` versus `command`, bindings |
| `expressions/` | The CEL profile |
| `nesting/` | The nesting joint and its output boundary |
| `tasks/` | The task kind: lifecycle, health, terminal, nesting |
| `workflows/` | The workflow kind |
| `channels/` | The channel kind |
| `providers/` | The workspace provider kind |
| `observers/` | The resource observer kind |
| `chains/` | Chains, a work-document construct |
| `plugins/` | Plugin and catalog manifests |
| `config/` | The reserved root files |

## Fixture grammar

Every fixture begins with an expectation header, then a `reason:` line stating
what it pins down. A definition document writes them as TOML comments:

```toml
# plect-fixture: result=invalid layer=structural diagnostic=PLECTURE-CFG-VALUE-FROM-AND-EXPR entry=definitions
# reason: a value declares both from and expr, so its form is ambiguous.
```

A work document writes them as HTML comments, because its own `+++`
frontmatter has to start the file once the header is stripped:

```markdown
<!-- plect-fixture: result=valid entry=work -->
<!-- reason: a work document's frontmatter is its completion contract and its body is the instruction. -->
```

| Field | Values |
|---|---|
| `result` | `valid`, `invalid`, `accepted-invalid` |
| `layer` | `structural`, `semantic`, `cel`, `instantiation` — required unless `result=valid` |
| `diagnostic` | The `PLECTURE-CFG-*` code, which must appear in the diagnostics table in [`../../docs/language/README.md`](../../docs/language/README.md) |
| `entry` | Which schema entry validates it: `definitions` (default), `work`, `config`, `catalogs`, `lock`, `plugin`, `catalog` |

`result=accepted-invalid` records a rule the language states but the
implementation is permitted not to enforce — the sanctioned static-typing cut
line. The fixture loads today and documents what a complete checker would
reject.

A fixture's filename carries `.invalid` or `.accepted-invalid` before `.toml`
so a reader sees the outcome without opening it.

## Running the checker

```bash
cd scripts/config-language-check && GOWORK=off go run .
```

The checker asserts that:

- every fixture declares a well-formed expectation, and decodes — as TOML for
  a definition document, or as TOML frontmatter for a work document;
- a `valid` fixture passes [`../../plecture.schema.json`](../../plecture.schema.json);
- an `invalid` fixture with `layer=structural` is rejected by that schema, and
  rejected by a rule the schema annotates with the declared diagnostic;
- an `invalid` fixture with `layer=semantic`, `layer=cel`, or
  `layer=instantiation`, and an `accepted-invalid` fixture, all pass the
  structural schema — which is what makes "something later rejects this" a
  claim about a later layer rather than an accident of shape;

`layer=instantiation` marks a rule that only a binding can break: a work
document declares the kind of resource it is written for, so its observed keys
are checked at load, but whether the resource an instance is actually bound to
matches that declaration is known only when the instance is created.
- every diagnostic code is both documented and exercised;
- every worked example in `docs/language/` is byte-identical to the fixture it
  names.

A fixture that does not decode at all is rejected before any schema rule can be
blamed, so a decode failure satisfies a structural expectation and its
diagnostic is taken on trust — `work/no-frontmatter.invalid.md` is the one such
case.

It is a one-time verification tool for the specification, not a standing check:
nothing wires it into CI, and it has no invariant to guard once the
implementation's own conformance suite reads these fixtures directly.
