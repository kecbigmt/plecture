# github

The GitHub catalog plugin: a workspace provider and a resource observation
contract, plus watcher subscription wiring, reconciled from the production
GitHub configuration this repository was originally bootstrapped with.

plect core knows nothing about GitHub. Everything GitHub-shaped lives here:
URL parsing, branch resolution, worktree acquisition, check-run/status
rollup, and linked-PR discovery.

## Contents

- `plugin.toml` — declares the executables this plugin builds or ships:
  `github-worktree` (setup/cleanup), `github-issue-pr` (observe), and
  `github-watcher` (subscribe/serve/gh-api), all built from `src/`, this
  plugin's own Go module (`src/cmd/github-worktree`, `src/cmd/github-issue-pr`,
  `src/cmd/github-watcher`); plus `gh-guard` (`scripts/gh-guard`), a shipped
  shell shim, not a build target.
- `workspaces/worktree.toml` — declares the `worktree` provider: resolves a GitHub issue/PR URL to a session id
  and acquires/releases the git worktree it maps to.
- `resources/issue.toml`, `resources/pull.toml` — the standalone observation contracts, one per resource kind
  (`plect resource status`): resource kind, CI check rollup, issue
  completion, revision, linked PR, mergeability, and GitHub's own aggregate
  review decision.
- `tasks/gh_guard.toml` — produces a directory an agent-runtime plugin's
  task composes as a generic PATH-prepend input (see
  `docs/design/plugin-boundary-contracts.md`'s GitHub CLI Guard section),
  so every `gh` a session's shell resolves is `scripts/gh-guard` instead of
  the real binary — mechanically denying `pr merge`/`issue close`/`pr
  close` (and their `gh api` equivalents) so a session can't act on a
  forgotten or de-prioritized "don't merge" instruction. Opt-in: wire it
  only into workflows that want it. See `scripts/gh-guard_selftest.sh` at
  the repository root for its behavior tests.
Task instructions (`work`, `review`, `respond`, `investigate`) and
team-specific process (a project-board integration, a PR-description
convention, a code-review house style) are intentionally not shipped here —
see "Residual config" below.

Out of scope for this plugin: which agent CLI runs the session (a Claude or
Codex task/workflow pack is a separate plugin), and the workflow that wires
`workspace_provider = "official.github.worktree"` plus an agent pack together.

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
- the `gh` CLI, authenticated
- a Go toolchain, to build the three executables at `plect plugin add`/`update`
  time (see `docs/design/plugin-packaging.md`'s Executable Build Model)

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
- The `work` / `review` / `respond` / `investigate` task instructions
  themselves, project-board integration, PR-description conventions, and
  house review style — these are yours now: declare your own
  `tasks/work.md`, `tasks/review.md`, and so on, in a trusted config layer.
  See `docs/language/tasks.md` for what a task document needs (`done_when`,
  `resource_observer`, `[[chains]]`).
- Running `github-watcher serve` as a background daemon, and its delivery
  configuration (`PLECT_BUS_SOCKET` / `PLECT_BUS_TOKEN`).
