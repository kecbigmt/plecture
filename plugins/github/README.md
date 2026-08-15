# github

The GitHub catalog plugin: a resource provider, a resource observation
contract, watcher subscription wiring, and the work/review/respond/investigate
task pack, reconciled from the production GitHub configuration this repository
was originally bootstrapped with.

plect core knows nothing about GitHub. Everything GitHub-shaped lives here:
URL parsing, branch resolution, workdir acquisition, check-run/status rollup,
linked-PR discovery, and the four task instructions a GitHub-flavored coding
session runs through.

## Contents

- `plugin.toml` — declares the two executables this plugin builds:
  `plect-github-provider` (setup/cleanup/observe) and `github-watcher`
  (subscribe/serve/gh-api), built from the sibling `plugins/github-provider`
  and `plugins/github-watcher` Go modules.
- `providers/github.toml` — resolves a GitHub issue/PR URL to a session id
  and acquires/releases the git workdir it maps to.
- `resources/github.toml` — the standalone observation contract
  (`plect resource status`): resource kind, CI check rollup, issue
  completion, revision, linked PR, and mergeability.
- `tasks/{work,review,respond,investigate}.toml` — the task pack a
  GitHub-flavored workflow composes: each renders its own instruction
  template and reads resource state via `from_resource_status`.
- `templates/{work,review,respond,investigate}.md` — the default
  instructions for each task. Team-specific process (a project-board
  integration, a PR-description convention, a code-review house style) is
  intentionally not shipped here — see "Residual config" below.

Out of scope for this plugin: which agent CLI runs the session (a Claude or
Codex task/workflow pack is a separate plugin), and the workflow that wires
`provider = "github"` plus an agent pack together.

## Cleanup

`plect destroy` always releases the workdir it acquired. To also delete the
local branch, pass the provider-level cleanup intent this plugin reads:

```bash
plect destroy <session> --input delete_branch=true
```

Left off, the branch stays (the safer default for a review-only or
shared-branch session). Branch deletion uses a safe `git branch -d`, so it
still refuses a branch carrying unmerged commits regardless of this flag.

## Install

Register this repository as a catalog and enable the plugin:

```bash
plect catalog add official git+https://github.com/kecbigmt/plecture --dir plugins --revision main
plect plugin add official/github
```

`--dir plugins` scopes the catalog root (and the fetch/verify/mount trust
space) to this repository's `plugins/` subtree, where `plugins/catalog.toml`
lives — not the whole repository. This plugin's own config
(providers/resources) never encodes the alias you register under: it
resolves its executables through `{{bin "plect-github-provider"}}`'s
plugin-local bare-name reading, which resolves against the containing
plugin regardless of which alias you chose.

Running `github-watcher serve` as a background daemon is a separate,
deployment-specific step (a systemd unit, a launchd agent, or similar) —
this plugin ships the binary and the subscribe-side wiring, not a daemon
supervisor.

## Requirements

- `git`
- the `gh` CLI, authenticated
- a Go toolchain, to build the two executables at `plect plugin add`/`update`
  time (see `docs/design/plugin-packaging.md`'s Executable Build Model)

## Residual config

What stays in your own config after enabling this plugin:

- Resource allowlist entries (`resource_allowlist` in `config.toml`) for the
  owners/repositories you allow `plect create` to dispatch against.
- Workdir root choice (`workdirs_root` in `config.toml`), and a per-repository
  `<repoDir>/.plect/base-branch` file for a repository that branches from
  something other than its actual default (a git-flow repo branching from
  `develop`, say).
- Whether you compose the GitHub provider/tasks with a Claude, Codex, or
  other agent workflow pack.
- Project-board integration, PR-description conventions, or house review
  style — add these as your own overlay of `templates/work.md` /
  `templates/review.md` in a trusted config layer, since they are
  team-specific rather than durable GitHub behavior.
- Running `github-watcher serve` as a background daemon, and its delivery
  configuration (`PLECT_BUS_SOCKET` / `PLECT_BUS_TOKEN`).
