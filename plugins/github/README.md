# github

The GitHub catalog plugin: a workspace provider and a resource observation
contract, plus watcher subscription wiring, reconciled from the production
GitHub configuration this repository was originally bootstrapped with.

plect core knows nothing about GitHub. Everything GitHub-shaped lives here:
URL parsing, branch resolution, worktree acquisition, check-run/status
rollup, and linked-PR discovery.

## Contents

- `plugin.toml` — declares the executables this plugin builds or ships:
  `github-worktree` (setup/cleanup), `github-issue-pr` (observe,
  query-pulls), `github-watcher` (subscribe/serve/gh-api),
  `github-webhook-receiver` (subscribe-pulls), and `gh-app-token`
  (mint/cache a GitHub App installation token), all built from `src/`, this
  plugin's own Go module (`src/cmd/github-worktree`,
  `src/cmd/github-issue-pr`, `src/cmd/github-watcher`,
  `src/cmd/github-webhook-receiver`, `src/cmd/gh-app-token`); plus
  `gh-guard` (`scripts/gh-guard`) and `gh-app-guard`
  (`scripts/gh-app-guard`), shipped shell shims, not build targets.
- `workspaces/worktree.toml` — declares the `worktree` provider: resolves a GitHub issue/PR URL to a session id
  and acquires/releases the git worktree it maps to.
- `resources/issue.toml`, `resources/pull_request.toml` — the standalone observation contracts, one per resource kind
  (`plect resource status`): resource kind, CI check rollup, issue
  completion, revision, linked PR, mergeability, and GitHub's own aggregate
  review decision. `pull_request.toml`'s observer also declares
  `[pull_request.query]`: a workflow population source with both `poll`
  and `subscribe` means (see "Query (population source)" below).
- `tasks/gh_guard.toml` — produces a directory an agent-runtime plugin's
  task composes as a generic PATH-prepend input (see
  `docs/design/plugin-boundary-contracts.md`'s GitHub CLI Guard section),
  so every `gh` a session's shell resolves is `scripts/gh-guard` instead of
  the real binary — mechanically denying `pr merge`/`issue close`/`pr
  close` (and their `gh api` equivalents) so a session can't act on a
  forgotten or de-prioritized "don't merge" instruction. It authenticates
  as nothing itself, so the wrapped `gh` falls back to whatever `gh auth`
  is already ambient on the host — **operator-auth only**. An App-only
  deployment must not wire this task; it composes `gh_app_guard` instead.
  Opt-in: wire it only into workflows that want it. See
  `scripts/gh-guard_selftest.sh` at the repository root for its behavior
  tests.
- `tasks/gh_app_guard.toml` — the same PATH-prepend composition as
  `gh_guard`, except the generated `gh` wrapper (`scripts/gh-app-guard`)
  also authenticates: it mints and caches a GitHub App installation access
  token (via `gh-app-token`) and sets `GH_TOKEN` for the delegated `gh`
  child process only, before applying the same merge/close denial. A
  session composes one guard or the other, never both. See "App auth"
  below and `scripts/gh-app-guard_selftest.sh` at the repository root.
- `tasks/work.toml`, `tasks/review.toml` (+ `review.md`), `tasks/investigate.toml`,
  `tasks/respond.toml` — generic task documents: the mechanism-level
  instruction, `done_when`, and completion contract for working an issue,
  reviewing a pull request, investigating an issue, and addressing review
  comments. They ship no `[[chains]]` and no team-specific process (a
  project-board integration, a PR-description convention, a code-review
  house style, which reviewer workflow gates a work session) — a host
  composes those by declaring its own document that sets
  `extends = "official.github.work"` (etc.) and adds chains/instructions
  there. See `docs/language/tasks.md`'s Extension section.

Out of scope for this plugin: which agent CLI runs the session (a Claude or
Codex task/workflow pack is a separate plugin), and the workflow that wires
`workspace_provider = "official.github.worktree"` plus an agent pack together.

**Removed:** a GitHub Projects v2 item id (`PVTI_...`) is no longer an
accepted resource identifier. It was never reachable through shipped
dispatch — the `worktree` provider's `match` regex above only matches an
issue or pull request URL — so the `gh api graphql` resolution path that
used to back it (ambient-`gh`-authenticated, never App-aware) was dead
code, removed rather than kept behind a deprecation shim. See
`docs/migrations/github-worktree-pvti-removal-migration.md` for the rare
case (a custom `match` pattern, or a direct `github-worktree` invocation)
where this is a live change.

## Cleanup

`plect destroy` always releases the worktree it acquired. To also delete the
local branch, pass the workspace-provider-level cleanup intent this plugin
reads:

```bash
plect destroy <session> --input delete_branch=true
```

Left off, the branch decision falls through to the workspace provider's
`delete_branch_default` parameter below, which is itself off unless a
workflow says otherwise (the safer default for a review-only or
shared-branch session). Branch deletion uses a safe `git branch -d`, so it
still refuses a branch carrying unmerged commits regardless of this flag.

## Query (population source)

`resources/pull_request.toml`'s pull request observer declares
`[pull_request.query]`
(`docs/adr/2026-09-05-standing-session-dispatch.md`, "Query contract: one
purpose with poll and subscribe means"): the item source a workflow's
`[[workflow.populations]]` entry binds to keep one session present per
matching pull request. `query.poll` is the sole authority for presence and
absence; `query.subscribe` only accelerates admission of a pull request
poll would confirm anyway, so a population entry always sets `poll_every`
for this observer and never `expire_after`.

- **`github-issue-pr query-pulls`** — the poll means: one complete REST
  list-pull-requests scan per requested repository, paginated to
  exhaustion, filtered by `--labels` (every one must be present),
  `--state` (`open`/`closed`/`all`), and `--draft`. Prints one JSON array on
  stdout; any fetch or parse failure fails the whole snapshot rather than
  printing a partial one. It favors the same deployment-level GitHub App
  auth `github-watcher serve` reads (`GITHUB_WATCHER_APP_*`, see "App auth"
  below) over `--watcher-bin`'s shared rate budget, since a query means
  invoked by a resident evaluator has no per-call `app_id`/`private_key_path`
  input to select a token by.
- **`github-webhook-receiver subscribe-pulls`** — the subscribe means: an
  HTTP service that verifies each delivery's `X-Hub-Signature-256` against
  `GITHUB_WEBHOOK_RECEIVER_SECRET` (required; the process refuses to start
  without it) and, for each verified `pull_request` delivery whose current
  state matches the same `--repositories`/`--labels`/`--state`/`--draft`
  filter, writes one JSON item to stdout. An unverified delivery, a
  non-matching verified delivery, or a non-`pull_request` event yields
  nothing — subscribe only ever reports a match, never an absence; poll
  above stays the sole authority for that. Exposing this service's endpoint
  publicly and provisioning its secret are deployment infrastructure, not
  something this binary or `plugin.toml` does.

Both share the query's item shape: `resource` (the pull request URL,
required) plus optional `owner`/`repository` identity decomposition —
never a `state_schema` fact (`plugins/github/src/internal/pullquery`'s
self-test asserts the two never overlap).

## Parameters

Author-declared values a workflow sets to steer this plugin's configs
without replacing them (the parameterization rung of
`docs/design/task-nesting.md`'s customization ladder). The workspace
provider's parameters go in a workflow's `[workspace_provider_inputs]`
table:

| Parameter | Meaning |
|---|---|
| `workspace_layout_root` | Root the `github.com/<owner>/<repo>/<branch>` worktree containers are laid out under. Empty = the configured `workspace_dirs_root`. |
| `issue_branch_template` | Branch name an issue resource maps to. Placeholders: `{owner}`, `{repo}`, `{number}`. Default `issue/{number}`. A pull request's branch always comes from its head ref. |
| `tagged_branch_suffix` | Suffix appended for a tagged session, separator included. Placeholder: `{tag}`. Default `+{tag}`. |
| `delete_branch_default` | `"true"` to reclaim the branch on destroy when the caller expressed no `delete_branch` intent. Default off. |

```toml
workspace_provider = "official.github.worktree"

[workspace_provider_inputs]
workspace_layout_root = "~/worktrees"
issue_branch_template = "work/{number}"
delete_branch_default = "true"
```

## App auth

`gh_app_guard` is an alternative to `gh_guard` for a session that must
authenticate as a shared GitHub App installation instead of the operator's
own `gh auth login`: the wrapper it produces mints an installation access
token (App JWT → `POST /app/installations/{id}/access_tokens`), caches it
with an expiry skew so a long-running session doesn't have to re-mint on
every call, and refreshes it once that skew window is entered — with file
locking around the cache so two `gh` invocations racing an expired cache
mint exactly once. Merge/close stays denied under the App identity too,
checked before minting so a call about to be denied never costs a mint.

Inputs, operator-provisioned — none of these belong in committed config:

| Input | Meaning |
|---|---|
| `app_id` | The GitHub App's id. |
| `installation_id` | The installation id, if known. |
| `owner`, `repo` | Alternative to `installation_id`: the wrapper resolves the installation id for this repository on first mint. |
| `private_key_path` | Path to the App's PEM private key. Operator-provisioned, outside any repo, mode 0600 — this plugin never writes the key itself, only reads the path it's given. |

```toml
[[nodes]]
id   = "gh_app_guard"
uses = "official.github.gh_app_guard"

[nodes.gh_app_guard.inputs]
app_id            = "123456"
installation_id   = "789012"
private_key_path  = "/etc/plect/gh-app.pem"
```

The only on-disk copy of the minted token's plaintext is the cache file
inside the wrapper's own reported directory (`.token-cache.json`, mode
0600), removed on session cleanup along with the rest of that directory.
`gh-app-token`'s own errors — a missing/unreadable private key, a rejected
mint — never fold key or token bytes into their text, so the wrapper's
failure output is safe to surface as-is.

`official.github.worktree` can use the same App identity for raw git network
I/O and for fetching pull request/issue metadata (head branch, title,
state). A session sets the same task-level inputs `gh_app_guard` accepts:
`app_id`, optional `installation_id`, optional `owner`/`repo`, and
`private_key_path`. Setup reads those values from `session.inputs` and runs
git with process-local config that clears ambient credential helpers, installs
a scoped `https://github.com` helper backed by `gh-app-token credential`, and
rewrites GitHub SSH remote URLs to HTTPS for that git process. The metadata
fetch authenticates the same way, but directly over HTTP rather than through
`gh` — it replaces whichever of `gh api` (direct) or `github-watcher`'s
shared rate budget setup would otherwise use. When `installation_id` is empty
and `owner`/`repo` are not supplied, both the git credential helper and the
metadata fetch resolve the installation from the resource owner/repo. Both
also share the same locked token cache and expiry skew as `gh_app_guard`, so
whichever runs first mints the installation token and the other reuses it,
each minting or reusing independently if the other's call never happens (a
metadata-only run before any git operation, or vice versa). With those inputs
absent, worktree acquisition uses the host's ordinary git credential
configuration and gh-api client unchanged.

```bash
plect up https://github.com/acme/widgets/issues/42 --inputs '{
  "app_id": "123456",
  "installation_id": "789012",
  "private_key_path": "/etc/plect/gh-app.pem"
}'
```

**`github-watcher serve` can authenticate as one deployment-level GitHub App
installation instead of the operator's own `gh auth`.** It is a single
resident service shared across sessions (`[[services]]` in `plugin.toml`),
not a per-session task in a plan, so there is no per-call installation
context to select a token by — this is deliberately one App id/installation
per deployment, set via environment rather than `gh_app_guard`'s per-session
task inputs. `github-watcher gh-api`, the cross-process helper the workspace
provider and resource observation call into, shares the same auth (same env,
same token cache), so both the poll loop and config-layer `gh api` calls
authenticate as the one installation. Set these on the `github-watcher serve`
process (and anywhere `gh-api` runs standalone):

| Env var | Meaning |
|---|---|
| `GITHUB_WATCHER_APP_ID` | The GitHub App's id. Unset means no App auth at all — ambient `gh auth` behavior is unchanged. |
| `GITHUB_WATCHER_APP_INSTALLATION_ID` | The installation id, if known. |
| `GITHUB_WATCHER_APP_OWNER`, `GITHUB_WATCHER_APP_REPO` | Alternative to `GITHUB_WATCHER_APP_INSTALLATION_ID`: resolves the installation id for this repository on first mint. |
| `GITHUB_WATCHER_APP_PRIVATE_KEY_PATH` | Path to the App's PEM private key. Required once `GITHUB_WATCHER_APP_ID` is set. |
| `GITHUB_WATCHER_APP_BASE_URL` | GitHub API base URL override (default `api.github.com`). |
| `GITHUB_WATCHER_APP_CACHE_PATH` | Token cache path override (default `<data-dir>/.app-token-cache.json`, alongside the shared rate-budget file). |

A deployment configured this way fails loud: `serve` mints once at startup
and exits non-zero if the private key is unreadable or the mint is rejected,
instead of staying process-healthy while every subsequent fetch dies quietly.
Multi-installation watcher auth (a per-session or per-repository identity)
stays out of scope until a concrete consumer needs it.

`github-issue-pr observe` accepts a bare-`gh`, no-App-auth mode (omitting
`--watcher-bin`), which the shipped `resources/issue.toml`/`pull_request.toml`
configs never select — they always pass `--watcher-bin` so calls route
through the shared rate budget above. It has no App-auth inputs of its
own, so invoking it directly without that flag is operator-auth only and
out of scope for an App-only deployment. (`github-issue-pr query-pulls`,
covered under "Query" above, is different again: it favors
`GITHUB_WATCHER_APP_*` over `--watcher-bin` outright, with no per-call
App-auth flags of its own — there is no session to carry them.)
`github-worktree` is different:
its `--app-id`/`--private-key-path` App-auth inputs (see above) win
outright over its own `--watcher-bin` selection, so a direct
`github-worktree setup` invocation authenticates as the App whenever
those inputs are set, `--watcher-bin` or not. Without them, the metadata
fetch falls back to whatever `--watcher-bin` selects: ambient `gh auth`
if omitted, or `github-watcher gh-api`'s own credential source if
passed — which is itself ambient `gh auth` unless that deployment's
`GITHUB_WATCHER_APP_*` env (see above) is configured.

### One credential story for an App-only deployment

An App-only deployment (a GitHub App installation only — no operator
personal or shared `gh auth login` anywhere) wires all three call sites
below to that same App identity, and never wires `gh_guard`:

| Call site | Wire this | Not this |
|---|---|---|
| Session `gh` calls (agent runtime) | `gh_app_guard`, composed as the runtime's `path_prepend` input | `gh_guard` — operator-auth only, see above |
| `official.github.worktree` git operations and metadata fetch | The same `app_id`/`private_key_path` (etc.) session inputs, passed at `plect up` time | Leaving those inputs unset: git operations fall back to the host's ambient git credentials, and the metadata fetch falls back to ambient `gh auth` (or the watcher's own App auth, if that's separately configured) |
| `github-watcher serve` and its `gh-api` helper (resident, cross-session) | The `GITHUB_WATCHER_APP_*` deployment env vars | Ambient `gh auth login` on the host running `plect serve` |

With all three wired, no `gh auth login` needs to run anywhere in the
deployment — each call site independently mints or reuses its own GitHub
App installation token, on its own schedule, with no shared process state
between them beyond the on-disk token cache each was given.

## Install

Register this repository as a catalog and enable the plugin:

```bash
plect catalog add official git+https://github.com/kecbigmt/plecture --subdir plugins --revision main
plect plugin add official/github
```

`--subdir plugins` scopes the catalog root (and the fetch/verify/mount trust
space) to this repository's `plugins/` subtree, where `plugins/catalog.toml`
lives — not the whole repository. This plugin's own config
(workspaces/resources) never encodes the alias you register under: it
resolves its executables through `{ bin = "github-worktree" }`'s plugin-local
bare-name reading, which resolves against the containing plugin regardless of
which alias you chose.

Running `github-watcher serve` as a background daemon is a separate,
deployment-specific step (a systemd unit, a launchd agent, or similar) —
this plugin ships the binary and the subscribe-side wiring, not a daemon
supervisor.

## Requirements

- `git`
- the `gh` CLI on PATH — authenticated via the operator's own `gh auth
  login` by default. An App-only deployment replaces every use of that
  ambient auth instead of running `gh auth login` at all — see "One
  credential story for an App-only deployment" above.
- a Go toolchain, to build this plugin's Go executables at `plect plugin
  add`/`update` time (see `docs/design/plugin-packaging.md`'s Executable
  Build Model)

## Residual config

What stays in your own config after enabling this plugin:

- Resource allowlist entries (`resource_allowlist` in `config.toml`) for the
  owners/repositories you allow `plect create` to dispatch against.
- Workspace dirs root choice (`workspace_dirs_root` in `config.toml`), and a
  per-repository `<repoDir>/.plect/base-branch` file for a repository that
  branches from something other than its actual default (a git-flow repo
  branching from `develop`, say).
- Whether you compose the GitHub workspace provider/tasks with a Claude,
  Codex, or other agent workflow pack.
- Team-specific process on top of `work` / `review` / `respond` /
  `investigate` — project-board integration, PR-description conventions,
  house review style, and which reviewer workflow (if any) gates a work
  session — these are yours: declare your own document extending the
  plugin's (`extends = "official.github.work"`, and so on) in a trusted
  config layer. See `docs/language/tasks.md`'s Extension section for what
  an extension may add (`[[instructions]]`, `[[chains]]`, `done_when`
  leaves).
- Running `github-watcher serve` as a background daemon, and its delivery
  configuration (`PLECT_BUS_SOCKET` / `PLECT_BUS_TOKEN`).
