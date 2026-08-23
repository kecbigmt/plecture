# Retire the task frontmatter document class

## Context

A task declaration was the one kind with a body: `+++`-delimited TOML
frontmatter in a `.md` file, with the instruction as the body below the
closing `+++`. That shape read illegibly on GitHub. Frontmatter rendered as
literal body text rather than being recognized and hidden, and every `#`
comment inside the frontmatter's TOML rendered as a Markdown heading (`#`,
`##`, ... depending on the number of leading `#` characters) because GitHub's
renderer treats the file as Markdown from byte zero and does not know TOML
frontmatter delimited by `+++` is not part of the prose. Every shipped and
user-authored task document was affected, since config readability — for
review, for diffing, for a reader following a link from an issue — is a
property the language is supposed to have.

The frontmatter document class was also the one place the language forked its
own grammar by file class rather than by field: discovery had to special-case
`.md` files that opened with `+++`, `ParseTaskDocument` duplicated
`ParseDefinitionDocument`'s table-parsing rules for exactly one declaration,
and four diagnostic codes existed only to police that one file's shape
(`PLECTURE-CFG-TASK-FRONTMATTER-MISSING`, `PLECTURE-CFG-TASK-BLOCK-COUNT`,
`PLECTURE-CFG-TASK-IN-TOML-DOCUMENT`, `PLECTURE-CFG-BODILESS-IN-TASK-DOCUMENT`).

## Decision

A task is an ordinary TOML declaration: `[<id>] kind = "task"` in any
`tasks/*.toml` file, discovered and parsed exactly like every other kind. The
instruction moves to a value the declaration carries rather than a document's
body: `[[<id>.instructions]]`, an ordered array. Each element carries exactly
one of:

- `text = "..."` — inline, for a segment short enough to read as a string
  (a TOML triple-quoted string for a multi-line one).
- `file = "<relative path>"` — a sidecar Markdown file, resolved relative to
  the declaring TOML file. The sidecar carries no frontmatter of its own; it
  is plain prose, and it renders correctly wherever Markdown does. Its
  `{{ <path> }}` projections are validated at load exactly as the old body's
  were.

The elements' resolved texts join with a blank line, in declaration order, to
form the instruction. One element is the common case; the array shape exists
because a forthcoming task-extension design appends instruction segments to a
base document, and an array makes that append structural — a new element —
rather than string concatenation. This is a mid-flight amendment to the
initially ratified design, which specified a scalar `instruction` /
`instruction_file` pair; the array is a strict generalization; a
single-element array is the scalar design's exact behavior, restated.

An element declaring other than exactly one of `text` and `file` is a load
error (`PLECTURE-CFG-TASK-INSTRUCTION-ELEMENT`, structural — the schema can
see the key shape). A `file` must be a relative path — an absolute spelling
is rejected outright rather than joined onto the declaring directory, which
is the only alternative a path-join primitive offers and not what "relative
to the declaring file" means — and it must resolve within the same trusted
definition-root layer as the declaring file, after resolving symlinks to
their real target so a symlink planted inside the layer cannot smuggle a read
from outside it. The boundary is the layer, not the declaring file's own
directory: a task nested below a subdirectory of `tasks/` may reach a
sidecar beside its parent directory with `file = "../shared.md"`, same as a
plain relative path resolving anywhere else still inside the layer. A path
escaping the layer itself — by traversal, by symlink, or by being absolute
in the first place — (`PLECTURE-CFG-TASK-INSTRUCTION-FILE-CROSS-LAYER`) or
naming no readable file (`PLECTURE-CFG-TASK-INSTRUCTION-FILE-MISSING`) is a
load error. Both are semantic, not structural: neither is checkable from the
TOML shape alone, only from resolving the reference against the filesystem
and the layer boundary — the same reasoning that makes
`PLECTURE-CFG-REF-CROSS-PLUGIN` semantic rather than structural.

The four retired frontmatter-era codes are removed from the language
entirely — not deprecated, not kept as an accepted-invalid fixture — because
the rule they enforced (a `+++`-delimited document is the one shape a task
declaration may take) no longer exists to violate. `docs/language/README.md`'s
diagnostics table and `app/internal/lang`'s `Codes()`/`codeLayers` registry
drop them together, held to the same set by `TestCodesMatchDocumentedTable`.

A `file` pointing at a missing file is a load diagnostic — fail loud, the
same posture the language takes everywhere else. The reverse — a `.md` file
under a definition root that no element's `file` names — has no load-time
moment to surface at, since nothing references it to begin with; it is
instead the repository's own concern: `app/internal/instructionorphans`
(invoked through `scripts/check-instruction-orphans.sh`, at the same CI tier
as `check-comment-references.sh`) decodes every `.toml` file's `instructions`
arrays and flags a `.md` under `plugins/*/config` or
`testdata/config-language` that none of them name. It decodes real TOML
rather than scanning source text, because "is this file referenced" is a
question about decoded values — a commented-out reference and an unusually
(but validly) quoted key are exactly the two cases a text scan answers
wrong, in opposite directions. It normalizes both shapes BurntSushi/toml
produces for an array of tables decoded into a generic map — `[]map[string]any`
for the canonical `[[<id>.instructions]]` form, `[]any` for the inline
`instructions = [{ ... }]` form — the same distinction
`app/internal/lang/discover.go`'s `asTableArray` already makes for the real
loader, so a reference written either way is recognized.

## Consequences

Every `tasks/*.md` file that opened with `+++` frontmatter stops loading:
`.md` is no longer a definition file class at all, so a `.toml` file
declaring `kind = "task"` — previously a load error
(`PLECTURE-CFG-TASK-IN-TOML-DOCUMENT`) — is now the only form. This is a
breaking change and ships with a one-time migration
(`docs/migrations/task-frontmatter-migration.md`), including a mechanical
shell conversion and a backup step, per the pre-1.0 compatibility policy. The
migration was run against a copy of a real host configuration's seven task
documents, plus a synthetic document nested below a subdirectory, declared
under an id different from its filename, spelling `kind = 'task'` with
single quotes, and quoting another document's `+++` frontmatter inside its
own body — exercising the recursive, non-basename, alternate-quoting, and
embedded-delimiter cases together — before this decision shipped; all eight
loaded, with instructions resolved unchanged from their sidecar files. The
conversion script's line-oriented delimiter scan only treats the first two
`+++` lines as syntax; a later line that happens to read `+++` — an
instruction quoting another document's frontmatter, which the language
chapter itself notes shipped instructions do — is body content, not a third
delimiter, and is preserved rather than silently dropped.

The shipped catalog carries no task documents as of this decision (an earlier
slice moved shipped process documents to host ownership), so the shipped side
of this migration is corpus and fixtures only — no plugin's own config
changes.

`app/internal/lang`'s `DiscoverRoot` drops its `.md`-frontmatter branch
entirely: every discovered definition now comes from a `.toml` file, and a
`.md` under a definition root is resolved, if at all, only when an
`instructions` element's `file` names it — during discovery, not as a second
discovery pass. `ParseTaskDocument` and the frontmatter delimiter constant are
deleted. `plecture.schema.json` drops its `#task` anchor (the frontmatter's
own single-declaration schema entry) along with the `not` clause that banned
`kind = "task"` from the ordinary `definition` entry; `taskDefinition` joins
`definition`'s `oneOf` like every other kind's schema does, and a new
`instructionElement` `$def` states the per-element XOR the same way
`execAction`'s `bin` / `command` pair already does.

The conformance corpus's task fixtures — and the `chains/`, `expressions/`,
`observers/`, and `values/` fixtures written as task documents to exercise a
task-only construct — convert from `.md` to `.toml`. Three retired-rule
fixtures (`tasks/bodiless-kind.invalid.md`, `tasks/no-frontmatter.invalid.md`,
`tasks/two-blocks.invalid.md`) have no replacement: the rules they pinned
down no longer exist to test. Six new fixtures cover the new surface: one
invalid element declaring both `text` and `file`
(`PLECTURE-CFG-TASK-INSTRUCTION-ELEMENT`; the mirror case, an element
declaring neither, is exercised as a native Go unit test rather than a
corpus fixture, since the schema cannot distinguish an intentionally-empty
element from one this rule should reject any more precisely than the "both"
case already proves the rule fires), a `file` naming no readable sidecar,
and two shapes of a `file` resolving outside the layer — one absolute, one
via `..` traversal to a target that exists but sits outside the corpus
root, proving the layer check runs before any existence check. Two more are
valid: a multi-element composition demonstrating the blank-line join, and a
fixture nested one directory below `tasks/` demonstrating the accepted
counterpart to the traversal case — a `..` reference that stays inside the
layer.

Byte-identity with the pre-migration shape is a golden test, not only a claim:
`app/internal/lang`'s conformance suite loads `tasks/document.toml`'s
single-element `instructions` array through the sidecar path and asserts the
result equals the retired frontmatter document's body, literal byte for byte.

## Alternatives considered

### Keep frontmatter, fix only the rendering

GitHub's Markdown renderer has no configuration knob for recognizing `+++`
frontmatter as TOML rather than prose, and no amount of internal escaping
changes what a reader sees on the hosting platform config is reviewed and
linked from. The illegibility is a property of the file class itself, not of
how this repository authored any particular document.

### A scalar `instruction` / `instruction_file` pair, no array

The design this ADR originally ratified. Superseded mid-flight: a forthcoming
task-extension design needs to append an instruction segment to a base
document without string concatenation, which a scalar pair cannot express
without inventing a second mechanism later. Making the body an array now —
while the surface is already being cut over — is the one gap this decision
had a natural chance to close.

### A single `instruction` field, inline only

Considered and rejected: the reason a task document existed as a separate
Markdown file in the first place was that a real instruction — with fenced
code blocks, multi-paragraph steps, and quoted TOML/shell examples — breaks
TOML triple-quote escaping and reads badly as a string literal even when it
technically parses. A `file` segment keeps that document-shaped body
available without resurrecting a second file class: the sidecar is inert
prose, not a declaration.

### A single `file`-only segment shape, no inline `text`

Rejected as needless ceremony for the common case of a one-line instruction,
which every effect-kind field with a `_file` counterpart (`inputs_schema` /
`inputs_schema_file`, `state_schema` / `state_schema_file`) already avoids by
offering both spellings.

### Lexical path containment only, no symlink resolution

Rejected: a symlink planted inside the trusted layer, pointing outside it,
would pass a purely lexical `..`-free check while still handing the reader
outside content — the containment boundary has to be a claim about the real
file being read, not about the path string naming it. Resolution follows
symlinks (falling back to the parent directory's real path when the leaf
itself does not exist yet) before the containment check runs.

### `filepath.IsLocal` rejects any `..` in `file`

A first cut used `filepath.IsLocal` to reject an absolute or escaping `file`
value in one call. Rejected: `IsLocal` treats "leaves the declaring
directory" as disqualifying, which is stricter than the ratified rule
("must stay within the same layer") — a task nested below a subdirectory of
`tasks/` legitimately reaches a sidecar beside its parent directory with
`file = "../shared.md"`, and `IsLocal` rejected that valid declaration before
the real containment check ever ran. The fix rejects only what a relative
path can never be — empty, or absolute — up front, and leaves `..` handling
entirely to the existing root-relative, symlink-aware containment check,
which already tells a same-layer parent reference apart from a real escape
correctly.

### The orphan check scans TOML source text

The first cut wrote `check-instruction-orphans.sh` as a self-contained shell
script scanning source lines for `file = "..."`. Rejected once it was clear
what that gets wrong on both sides: a commented-out reference
(`# file = "orphan.md"`) reads as a real one, and a validly-quoted key TOML
itself treats as equivalent (`'file'`, `"file"`, a single-quoted value) reads
as no reference at all — a text scan cannot distinguish either case from its
genuine counterpart without reimplementing a TOML parser badly. The check is
`app/internal/instructionorphans`, a small Go package decoding real TOML
(the module already depends on `BurntSushi/toml`), invoked by the same
wrapper script so the CI job and its selftest are unchanged in shape.
