# Standing rules for coding agents

Plecture lets individuals and teams compose their own systems for autonomous
work without committing to a particular agent, VCS, execution environment,
workspace technology, or communication tool. Plecture owns the durable
structure of work: identity, lifecycle, relationships, observation,
verification, and handoff.

## Placement: core vs. plugin

Durable structure of work (identity, lifecycle, relationships, observation,
verification, handoff) belongs in `app/` and `contracts/` (core).

A plugin is a distributable package: executable adapters (0+) + config
resources (0+ — providers, resources, tasks, workflows, templates,
channels) + metadata. A plugin with only configuration and no executables
is still a plugin. Commitments to a particular technology (a VCS, an agent
CLI, a terminal multiplexer, a chat service) belong in `plugins/`, whether
expressed as code or as shipped config. (Packaging mechanics — the package
format, lockfile, and reference-resolution model — are covered by a
separate design in progress, not by this section.)

Core must not know any specific provider exists. No provider names in core
identifiers, imports, help text, or error strings. This is a blocking review
dimension.

## Comments

- Never put issue, PR, or ADR numbers in code comments.
- Every comment must be a self-contained, complete sentence understandable
  without external context.

## Language

All prose — comments, docs, identifiers, messages — is English only. The
sole exception is non-English literals used as test data that specifically
exercises multibyte/unicode behavior, labeled as such by an adjacent English
comment.

For which project name to use where (Plecture vs. `plect`), see
[`docs/naming.md`](docs/naming.md); it is the decision record for all
prose naming.

## Repository map and dependencies

| Directory     | Responsibility                                                |
|---------------|----------------------------------------------------------------|
| `app/`        | CLI + MCP server: session lifecycle, task DAG, state, dispatch |
| `contracts/`  | Shared data contracts between the CLI and plugins               |
| `plugins/`    | Distributable packages: executable adapters and config resources for a particular technology (channel relay, GitHub provider and watcher, Slack adapter) |

Core (`app/`, `contracts/*`) never imports `plugins/*`.

`app`, each package under `contracts/`, and each package under `plugins/`
are independent Go modules, wired together by the repository's `go.work`
for editor tooling. A bare `go build ./...` from the repository root does
not resolve, because the root itself is not a module. Build and test per
module, as documented in the README:

```bash
go build ./app/...
go test ./app/...
```

and likewise for each module under `contracts/` and `plugins/`.

## Agent configuration

Claude and Codex sessions operate under the same rules and skills, so the
configuration has one source of truth per surface:

- `AGENTS.md` is a symlink to `CLAUDE.md`. Edit `CLAUDE.md`; never edit
  `AGENTS.md` directly or let the two diverge into separate files.
- Skills live in `.claude/skills/<name>/SKILL.md`, with `name:` and
  `description:` frontmatter fields.
- `.agents/skills/<name>` is a relative symlink to
  `../../.claude/skills/<name>`, one per skill, so a Codex session sees the
  same skill set as a Claude session.

`scripts/check-agent-config.sh` (wired into CI) validates these invariants:
the `AGENTS.md` symlink target, and, for every directory under
`.claude/skills/`, the presence of `SKILL.md` with both frontmatter fields
and a matching `.agents/skills/` symlink.

## Development workflow

This section is the source of truth for how changes get made. If the
repository later adopts agent skills or checklists, they must derive from
these rules, not replace them.

### TDD as the default flow

For any behavior change or bug fix, write the failing test first (red),
make it pass with the minimal implementation (green), then refactor. A bug
fix PR must contain a regression test that fails against the pre-fix code
and passes after the fix.

### Refactoring discipline

Refactoring commits are behavior-neutral and must not be mixed with
behavior changes in the same commit. Before refactoring code that lacks
test coverage, add characterization tests first so the existing behavior
is protected before it is moved or restructured. The required order is:
characterization tests, then refactor, then behavior changes as separate,
later commits.

Coverage means error branches and failure paths are tested, not just the
happy path. A change that only exercises success cases is incomplete.

### Design principles: YAGNI and SOLID

Stated operationally — testable rules an agent can apply and a reviewer
can block on, not name-drops.

- **YAGNI.** Build only what a present, concrete consumer needs. No
  speculative flags, config keys, or extension points. An abstraction
  needs at least two real consumers — in this change, or already in the
  tree (rule of three preferred) — otherwise write the concrete code
  twice first.
- **SOLID, Go-idiomatically.** Single responsibility per package/type,
  statable in one sentence. Small consumer-side interfaces: accept
  interfaces, return structs; define an interface where it is used, not
  where it is implemented. No interface with exactly one implementation
  and one caller.
- Speculative complexity that ships anyway must carry an explicit removal
  condition (a deadline or a metric) — the same discipline this repo's PR
  process already requires when complexity is introduced ahead of proven
  benefit.

### Test placement and speed

Prefer table-driven tests where idiomatic to the case being tested.
Per-module tests must stay fast enough to run on every CI matrix job;
push slow or environment-dependent cases behind an explicit opt-in rather
than letting them slow down the default run. Integration-style tests live
next to the module they exercise, not in a separate top-level tree.

### Installability invariant

`go install github.com/kecbigmt/plecture/app/cmd/plect@latest` must keep
working without local replace directives. A change that touches an
inter-module version pin (a `require` line in one module's `go.mod`
pointing at another module in this repository, e.g. `app`'s pin on
`contracts/*`) must verify that the commit embedded in the new pseudo-version
is reachable from `main` before merging — an unreachable commit breaks
resolution for every downstream `go install`.

### Definition of done

A PR is done when:

- CI is green, and CI is the mechanism that enforces this, not manual
  confirmation.
- New or changed behavior is covered by tests that were written before
  the implementation that satisfies them.
- There is no formatting or vet debt (`gofmt`, `go vet` clean) in any
  module touched by the change.

## Compatibility policy

Plecture is pre-1.0. Do not add backward-compatibility code paths. Breaking
changes ship with a one-time migration (script or documented procedure,
including a backup step), not a compatibility shim. The migration
procedure lives in `docs/migrations/`, added or updated in the same PR as
the breaking change — not as follow-up work.

## Design and ADR documentation

- **Decision records** live in `docs/adr/NNNN-<slug>.md` — a zero-padded
  sequence number and a kebab-case slug (e.g. `docs/adr/0001-plugin-packaging-format.md`).
  Each ADR has the sections Status (`Proposed`, `Accepted`, or `Superseded
  by NNNN`), Context, Decision, Consequences, and Alternatives considered.
  An ADR is immutable once `Accepted`: a change of mind produces a new,
  superseding ADR, never an edit to the accepted one.
- **Evolving design documents** (proposals still being worked out, broader
  than a single decision) live in `docs/design/<slug>.md`. Each one states
  its current status in the document and links the ADRs it implements or
  that were produced from it.
- **Migration procedures** stay in `docs/migrations/` (see Compatibility
  policy above) — they are not ADRs or design documents.
- All of the above is English-only prose; use `docs/naming.md` for project
  naming.
- Design and ADR documents are owner-gated: a design or ADR PR is never
  merged by automation, regardless of CI status.

## Self-containedness

Never introduce references to any specific person's environment, machines,
or other repositories, including in test fixtures and sample data.
