# Effects gain a `[setup.process]` sub-step for their long-lived process

## Context

Every agent-runtime effect repeats the same pane-launch kernel. Setting aside
resume/session-id branching, `plugins/claude/config/tasks/runtime.toml:113-132`
and `plugins/codex/config/tasks/exec_runtime.toml:79-102` each build the same
shell fragment: they shell-quote a `launch_env` JSON object into `export`
statements (rejecting any key that is not a valid environment-variable name),
compose `PATH` from a `path_prepend` input, and hand the result to `send_text`
followed by a `send_keys Enter` (`runtime.toml:164-165`,
`exec_runtime.toml:121-122`). `plugins/codex/config/tasks/codex.toml:81-100,
117-118` carries a third, materially identical copy for the interactive Codex
TUI. Every future agent-harness plugin would add a fourth.

The quoting in that kernel is a security boundary: it is what stops a
data-shaped input (`launch_env`, `path_prepend`) from becoming part of the
command line typed into the pane. A security boundary duplicated per plugin is
the worst place for it to live — a fix or a missed case in one copy does not
reach the others. With two shipped consumers already, a third in view, and the
duplication growing with every future harness, the boundary belongs in core.

The shape of the fix went through several rounds of owner dialogue before
landing:

1. **A `launch { command, env, path }` capability**, first proposed as a new
   verb on the Terminal Operation Surface
   ([`plugin-boundary-contracts.md`](../design/plugin-boundary-contracts.md)),
   alongside `attach`/`capture`/`send_text`/`send_keys`. Rejected: it adds a
   new word — `launch` — to core vocabulary for a concept the existing
   `[terminal]` surface already covers structurally.
2. **An action-level `[<id>.setup.terminal]` table**, spelled under the
   existing `terminal` word instead of a new one: effect-level `[terminal]`
   declares the endpoint, action-level `.terminal` operates on it. This kept
   the no-new-word constraint but read as an *operation* on the terminal
   surface rather than as what it actually is: the effect bringing up its own
   long-lived process.
3. **`[<id>.process]` as a lifecycle peer**, spelled `setup → process →
   cleanup`, matching the shape `[health]` and `[terminal]` already take as
   effect-level tables. This is where owner-ratified dialogue landed before
   this ADR was drafted.
4. **The correction this ADR records**: peer-phase placement is wrong,
   because it changes *when* the process starts relative to setup completing.
   A node's setup can depend on another node's agent process already being
   interactive — Claude and Codex's own initial-prompt delivery is exactly
   this: a downstream setup step sends the first prompt into the pane, and it
   needs the process already running to receive it. If `process` runs after
   `setup` returns, no ordering guarantee places the process before whatever
   in the plan wants to talk to it next. Today's shipped scripts avoid this
   race by sending the launch line and confirming the process is up *inside*
   `setup`, before setup's own output is produced. The replacement has to
   preserve that: the process must start mid-`setup`, not after it.

That correction is what this ADR decides.

## Decision

An effect's `setup` action gains an optional declarative sub-step,
`[<id>.setup.process]`, that submits the effect's one long-lived process
(the agent event loop) into the plan's declared `[terminal]` endpoint.
Effects with no long-lived process omit it.

### Sequencing

Within one `setup` action:

1. The action's own `script` or `exec` body runs to completion, exactly as it
   does today, and produces this layer's setup outputs (the socket path, the
   generated MCP config, the derived session id, and — new — the composed
   command line as a plain string output).
2. If the action declares `[setup.process]`, core resolves `command`, `env`,
   and `path`, composes them into one line, and submits that line into the
   plan's declared `[terminal]` endpoint via that endpoint's `send_text`
   followed by `send_keys` (`Enter`) operations.
3. `setup` succeeds once that submission succeeds. A later node in the same
   plan that depends on this node's setup output is scheduled only after step
   2 has run, so its own setup can safely assume the process has already
   received its launch line.

This is a change from "process starts after the script's shell source
finishes running its own send/poll logic" to "process starts as a declared,
core-executed step between the script finishing and the action returning."
The script no longer sends the launch line itself; it only produces the data
the launch line is made from.

### Shape

```toml
[runtime.setup.process]
command = { from = "self.outputs.command" }

[runtime.setup.process.env]
PLECT_SESSION_NAME = { from = "session.name" }

[runtime.setup.process.path]
prepend = { from = "inputs.path_prepend", optional = true }
```

- `command` is required: the exact text submitted to the pane. It is
  typically `{ from = "self.outputs.command" }`, which is structurally sound
  because the process sub-step runs after the setup script that produced
  `self.outputs.command`.
- `env` is an optional table of literal assignments. Each key matches
  `^[A-Za-z_][A-Za-z0-9_]*$`. Each value is resolved once and used exactly as
  resolved — never expanded — so a value containing `$PATH` types out as the
  literal three characters `$`, `P`, `A`, `T`, `H`, not as a shell expansion.
- `path` is an optional table with `prepend` and/or `append`, each a value
  producing a directory. Both may be present together; core applies them in a
  fixed order, prepend then append, so composition never depends on TOML key
  order.

Full worked examples for the shipped `claude` and `codex` effects, and the
validation rules for the new fields, are in
[`plugin-boundary-contracts.md`](../design/plugin-boundary-contracts.md) and
[`../language/effects.md`](../language/effects.md).

### Core owns the boundary

Core composes the exports, the `PATH` operations, and the command into one
string and submits it exactly once. This is a single-writer change: a script
no longer builds or sends a launch line itself for this purpose. Quoting and
key-name validation happen as data, before any keystroke is sent, the same
way `bind` values already never reach a shell action's argv or rendered
source (see [`../language/actions.md`](../language/actions.md)). What each
shipped effect deletes is exactly its `launch_env`/`path_prepend` splicing
and its own `send_text`/`send_keys` handoff line; everything else — resume
branching, MCP assembly, hook files, model/effort flags — stays where it is,
because none of it is part of the duplicated kernel.

### No renames

`[terminal]` is unchanged: it still declares the interactive endpoint, and
`attach`/`capture`/`send_text`/`send_keys` are still its verbs. The word
"launch" appears nowhere in config or core vocabulary. `process` is the only
new word this decision introduces, and it names a sub-step of `setup`, not a
new top-level table.

### At most one process sub-step per composed chain

A nesting chain (see
[`../design/task-nesting.md`](../design/task-nesting.md)) may declare
`[setup.process]` in at most one of its layers, mirroring the existing
per-chain rule for `[terminal]`. Two layers declaring it is a load error
naming both layers, for the same reason two layers may not both declare
`[terminal]`: plan assembly's terminal-count and process-count both need a
single answer per node, and a chain sits inside one node.

A `[setup.process]` declaration also requires some effect in the plan to
declare `[terminal]` — the endpoint it submits into — exactly like any other
`terminal` capability consumption.

### No implicit lifecycle symmetry

This decision adds no automatic process-teardown machinery. Process
termination guarantees stay exactly where they are today: `cleanup` kills the
process (`kill -TERM`, then `-KILL` on timeout) and tears down whatever
endpoint resources it owns, and cleanup still unwinds a nesting chain LIFO.
`setup` gaining a declarative sub-step does not obligate `cleanup` to gain a
matching one.

### Post-launch bookkeeping (proposed — owner sign-off pending)

Today's shipped scripts do more than send the launch line: right after it,
they poll for evidence the process actually started — `runtime.toml` polls
`~/.claude/sessions/*.json` for up to 30s and sends extra `Enter` keystrokes
to clear a startup warning banner; `exec_runtime.toml` polls for a
worker-written `ready` file. Both then derive and return `pid` (and, for
Claude, `session_id`) as setup outputs. With the launch line itself now sent
by core as the process sub-step rather than by the script, that polling logic
no longer has a place to run *before* the send — it needs a seat *after* it,
and the script itself finished (and returned its outputs) one full step
earlier.

Two candidates were named for that seat:

**(a) A post-process script seat within `setup`.** Add a second declarative
sub-step — an ordinary `exec`/`shell` action — that core runs after the
process submission and before `setup` returns, with the same binding surface
`setup`'s own script has plus access to whatever the submission produced.
This preserves today's synchronous fail-fast behavior exactly: `setup` still
does not return, and no downstream node is scheduled, until the process is
confirmed alive.

**(b) Probe-based discovery.** Do not add a seat at all. Let `setup` return as
soon as the process sub-step submits its line, without confirming the process
booted. The effect's `[health].alive` probe — already declared per effect —
performs the discovery instead. This is not a new mechanism: today's
`runtime.health.alive` already walks the pane's process tree and matches
`~/.claude/sessions/*.json` when a stale `pid` is found dead (the resume/crash
recovery path), and writes the rediscovered identity back via
`plect state set-output`. Making that the *only* discovery path — used for
initial launch too, not just recovery — needs no new field, no new action
type, and no new word.

**Proposed resolution: (b), probe-based discovery.** Two reasons:

- The owner has twice constrained this decision to add no vocabulary beyond
  `process` itself (first ruling out a new terminal verb, then correcting the
  peer-phase placement). Candidate (a) would need a name for the new seat —
  a second new word — for behavior that a currently-shipped mechanism
  already performs. Candidate (b) needs none.
- [`2026-08-18-health-declaration.md`](2026-08-18-health-declaration.md)
  already rejected a third, readiness-shaped probe as a non-goal: "nothing
  routes to a session on the strength of a readiness verdict." A dedicated
  post-process bookkeeping seat is readiness under a different name — a
  probe of "has this finished starting" distinct from "is this alive right
  now." Folding discovery into `alive` keeps the probe vocabulary at two,
  consistent with that earlier decision.

The honest cost: `setup` no longer blocks until the agent process is
confirmed booted, only until its launch line is submitted. A launch that
never actually starts (a crash right after the pane receives its command, for
instance) is no longer a `setup` failure the caller sees immediately; it
surfaces at the next `alive` evaluation instead, whatever that surface's
scheduling and reporting latency is. That is a real behavior change from
today's synchronous 30s poll-and-fail, and it is scoped to the implementation
PR to size and, if needed, mitigate — for instance, by scheduling a plan's
first health evaluation immediately after the setup that just completed
rather than waiting a full `[healthcheck].period`. This ADR does not mandate
a specific mitigation; it decides only that discovery moves to `alive` and
states why.

This resolution is proposed, not ratified: the owner signs off on it at PR
review, per the open decision the FINAL DESIGN ruling left for this ADR to
settle.

## Consequences

- `plugins/claude/config/tasks/runtime.toml` and
  `plugins/codex/config/tasks/exec_runtime.toml` are rewritten against
  `[setup.process]` in the implementation PR that follows this ADR. Each
  deletes its `launch_env` parse/validate/quote block, its `path_prepend`
  composition, and its `send_text`/`send_keys` handoff line; each keeps
  everything else (resume branching, MCP/hooks assembly, model/effort flags,
  the interactive Claude warning-banner Enter loop or the Codex ready-file
  poll — relocated per the resolution above).
- `plugins/codex/config/tasks/codex.toml` (the interactive Codex TUI) carries
  the same kernel duplication a third time. This ADR does not decide whether
  the implementation PR rewrites it alongside `exec_runtime.toml`; that scope
  call belongs to the implementation issue.
- Core needs: a `[setup.process]` grammar (`command`, `env`, `path`) on the
  effect `setup` action; validation for the env key pattern and literal
  value shape, the `path` key set, and deterministic prepend-then-append
  composition; per-chain at-most-one enforcement alongside the existing
  `[terminal]` count; and the compose-and-submit execution step itself,
  sequenced between the setup action's own execution and its return.
- Per the pre-1.0 compatibility policy, rewriting the shipped `claude` and
  `codex` effects is a breaking change to their setup shape. The one-time
  migration procedure for it belongs in `docs/migrations/`, authored in the
  implementation PR alongside the breaking change itself — not in this
  docs-only ADR, which changes no shipped configuration.
- The normative present-state shape lands in
  [`../language/effects.md`](../language/effects.md) (the `[setup.process]`
  surface and validation rules) and
  [`../design/plugin-boundary-contracts.md`](../design/plugin-boundary-contracts.md)
  (core owning the compose-and-submit boundary, plus the claude/codex worked
  examples), both updated by this same PR.

## Alternatives considered

### A new `launch` verb on the Terminal Operation Surface

Rejected by the owner's first vocabulary ruling. It solves the duplication
but adds a fourth terminal verb spelling a concept — starting the effect's
own process — that is not a raw terminal operation the way `send_text` and
`send_keys` are. It also introduces the word "launch" into core vocabulary,
which the ruling explicitly bars.

### An action-level `[<id>.setup.terminal]` step

Rejected by the peer-phase ruling that preceded this ADR's correction. It
satisfied the no-new-word constraint by reusing `terminal`, but it named the
step after the surface it operates on rather than after what the effect is
doing — bringing up its own long-lived process — and the owner judged that a
worse fit than a dedicated word once one was on the table.

### `[<id>.process]` as a lifecycle peer (setup → process → cleanup)

This was the ratified design immediately before this ADR's correction, and
it is why the correction exists. Peer placement makes `process` start after
`setup` returns, with no ordering guarantee relative to any other node's
setup that depends on this process already running. Claude and Codex's
initial-prompt delivery needs exactly that guarantee — a later setup step
sends the first prompt into an already-live pane — so peer placement would
have broken a race the current shipped scripts already avoid by sending and
confirming the launch line from inside `setup` itself.

### A dedicated post-process bookkeeping seat (candidate (a) above)

Considered as the direct answer to "where does the poll-for-pid logic go,"
and rejected in favor of probe-based discovery. See the rationale in the
Decision section above: it would add a second new word for behavior a
shipped mechanism (`health.alive`'s stale-pid recovery walk) already
performs, and it reintroduces a readiness-shaped probe the health-declaration
ADR already rejected as a non-goal.

### Renaming `[terminal]` to `pane` or `console`

Not raised as a live candidate in this decision, but considered and rejected
in the same dialogue that ruled out new vocabulary: `pane` is
multiplexer-concrete (a tmux/Herdr detail leaking into a provider-neutral
name), and `console` is a synonym that would rename a stable surface for no
semantic gain. `[terminal]` stays.

### Grouping `path` operations as separate top-level `path_prepend`/`path_append` fields

Rejected in favor of one `path` table with `prepend`/`append` keys. The two
are the same kind of operation — a read-modify-write of `PATH` — and grouping
them documents that relationship and fixes their composition order in one
place, rather than leaving two independently-ordered fields whose relative
effect would depend on documentation rather than structure.
