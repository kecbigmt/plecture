# Standing rules for coding agents

Plecture lets individuals and teams compose their own systems for autonomous
work without committing to a particular agent, VCS, execution environment,
workspace technology, or communication tool. Plecture owns the durable
structure of work: identity, lifecycle, relationships, observation,
verification, and handoff.

## Placement: core vs. plugin

Durable structure of work (identity, lifecycle, relationships, observation,
verification, handoff) belongs in `app/` and `contracts/` (core).
Commitments to a particular technology (a VCS, an agent CLI, a terminal
multiplexer, a chat service) belong in `plugins/` and shipped config.

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

## Repository map and dependencies

| Directory     | Responsibility                                                |
|---------------|----------------------------------------------------------------|
| `app/`        | CLI + MCP server: session lifecycle, task DAG, state, dispatch |
| `contracts/`  | Shared data contracts between the CLI and plugins               |
| `plugins/`    | Standalone processes (channel relay, GitHub provider and watcher, Slack adapter) |

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

### Test placement and speed

Prefer table-driven tests where idiomatic to the case being tested.
Per-module tests must stay fast enough to run on every CI matrix job;
push slow or environment-dependent cases behind an explicit opt-in rather
than letting them slow down the default run. Integration-style tests live
next to the module they exercise, not in a separate top-level tree.

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
including a backup step), not a compatibility shim.

## Self-containedness

Never introduce references to any specific person's environment, machines,
or other repositories, including in test fixtures and sample data.
