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
body, in one of two mutually exclusive spellings:

- `instruction = "..."` — inline, for a body short enough to read as a
  string (a TOML triple-quoted string for a multi-line one).
- `instruction_file = "<relative path>"` — a sidecar Markdown file, resolved
  relative to the declaring TOML file. The sidecar carries no frontmatter of
  its own; it is plain prose, and it renders correctly wherever Markdown does.
  Its `{{ <path> }}` projections are validated at load exactly as the old
  body's were.

Declaring both is a load error (`PLECTURE-CFG-TASK-INSTRUCTION-AND-FILE`,
structural — the schema can see both keys present). `instruction_file` must
resolve within the same trusted definition-root layer as the declaring file;
a path escaping it (`PLECTURE-CFG-TASK-INSTRUCTION-FILE-CROSS-LAYER`) or
naming no readable file (`PLECTURE-CFG-TASK-INSTRUCTION-FILE-MISSING`) is a
load error. Both are semantic, not structural: neither is checkable from the
TOML shape alone, only from resolving the reference against the filesystem
and the layer boundary — the same reasoning that makes
`PLECTURE-CFG-REF-CROSS-PLUGIN` semantic rather than structural.

The four retired codes are removed from the language entirely — not
deprecated, not kept as an accepted-invalid fixture — because the rule they
enforced (a `+++`-delimited document is the one shape a task declaration may
take) no longer exists to violate. `docs/language/README.md`'s diagnostics
table and `app/internal/lang`'s `Codes()`/`codeLayers` registry drop them
together, held to the same set by `TestCodesMatchDocumentedTable`.

An `instruction_file` pointing at a missing file is a load diagnostic — fail
loud, the same posture the language takes everywhere else. The reverse — a
`.md` file under a definition root that no `instruction_file` names — has no
load-time moment to surface at, since nothing references it to begin with;
it is instead the repository's own concern:
`scripts/check-instruction-orphans.sh`, at the same CI tier as
`check-comment-references.sh`, flags one over `plugins/*/config` and the
`testdata/config-language` corpus.

## Consequences

Every `tasks/*.md` file that opened with `+++` frontmatter stops loading:
`.md` is no longer a definition file class at all, so a `.toml` file
declaring `kind = "task"` — previously a load error
(`PLECTURE-CFG-TASK-IN-TOML-DOCUMENT`) — is now the only form. This is a
breaking change and ships with a one-time migration
(`docs/migrations/task-frontmatter-migration.md`), including a mechanical
shell conversion and a backup step, per the pre-1.0 compatibility policy. The
migration was run against a copy of a real host configuration's seven task
documents before this decision shipped; all seven loaded, with instructions
resolved unchanged from their sidecar files.

The shipped catalog carries no task documents as of this decision (an earlier
slice moved shipped process documents to host ownership), so the shipped side
of this migration is corpus and fixtures only — no plugin's own config
changes.

`app/internal/lang`'s `DiscoverRoot` drops its `.md`-frontmatter branch
entirely: every discovered definition now comes from a `.toml` file, and a
`.md` under a definition root is resolved, if at all, only when a task's
`instruction_file` names it — during discovery, not as a second discovery
pass. `ParseTaskDocument` and the frontmatter delimiter constant are deleted.
`plecture.schema.json` drops its `#task` anchor (the frontmatter's own
single-declaration schema entry) along with the `not` clause that banned
`kind = "task"` from the ordinary `definition` entry; `taskDefinition` joins
`definition`'s `oneOf` like every other kind's schema does.

The conformance corpus's task fixtures — and the `chains/`, `expressions/`,
`observers/`, and `values/` fixtures written as task documents to exercise a
task-only construct — convert from `.md` to `.toml`. Two retired-rule
fixtures (`tasks/bodiless-kind.invalid.md`,
`tasks/no-frontmatter.invalid.md`, `tasks/two-blocks.invalid.md`) have no
replacement: the rules they pinned down no longer exist to test. Three new
fixtures exercise the new diagnostics.

## Alternatives considered

### Keep frontmatter, fix only the rendering

GitHub's Markdown renderer has no configuration knob for recognizing `+++`
frontmatter as TOML rather than prose, and no amount of internal escaping
changes what a reader sees on the hosting platform config is reviewed and
linked from. The illegibility is a property of the file class itself, not of
how this repository authored any particular document.

### A single `instruction` field, inline only

Considered and rejected: the reason a task document existed as a separate
Markdown file in the first place was that a real instruction — with fenced
code blocks, multi-paragraph steps, and quoted TOML/shell examples — breaks
TOML triple-quote escaping and reads badly as a string literal even when it
technically parses. `instruction_file` keeps that document-shaped body
available without resurrecting a second file class: the sidecar is inert
prose, not a declaration.

### A single `instruction_file` field, no inline form

Rejected as needless ceremony for the common case of a one-line instruction,
which every effect-kind field with a `_file` counterpart (`inputs_schema` /
`inputs_schema_file`, `state_schema` / `state_schema_file`) already avoids by
offering both spellings.
