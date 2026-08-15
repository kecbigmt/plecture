# github-provider

The `plect-github-provider` executable: it maps GitHub issue and pull
request URLs to session ids, owns the git worktree those sessions work in,
and observes a GitHub resource's state (CI check rollup, issue completion,
linked PR, mergeability).

This module is a build dependency of the `plugins/github` catalog plugin,
not something to install directly — see `plugins/github/README.md` for the
provider/resource config that invokes this binary and for install
instructions. This README covers the executable's own commands, for
development and testing.

## Commands

- `plect-github-provider setup --resource <id> --session <name> [--workdirs-root <root>] [--watcher-bin <path>]`
  — acquires the working directory for a GitHub resource and prints the
  provider outputs contract (JSON, carrying the reserved `workdir` key) on
  stdout. `--watcher-bin`, when given, routes title/state fetches through a
  `github-watcher gh-api` call so they share its poll loop's rate budget;
  omitted, it calls `gh api` directly.
- `plect-github-provider cleanup --workdir <path> --branch <branch> [--workdirs-root <root>] [--force]`
  — releases what `setup` acquired. Branch reclaim uses a safe delete
  (`git branch -d`): a branch carrying unmerged commits survives as an
  orphan a later dispatch on the same resource reuses, rather than being
  discarded.
- `plect-github-provider observe --resource <id> [--workdir-path <path>] [--watcher-bin <path>]`
  — fetches and classifies a GitHub resource's current state: resource kind,
  CI check rollup, issue completion, revision, linked PR (for an issue
  resource), and mergeability. Backs `resources/github.toml`'s `observe`
  hook.

## Per-repository base branch override

A repository whose new work should start from a branch other than its
actual GitHub default (a git-flow repo branching from `develop`) can record
that choice at `<repoDir>/.plect/base-branch` — a plain file on the
repository container, not provider config, since providers don't cascade to
individual repo layers.

## Requirements

- `git`
- the `gh` CLI, authenticated — used to resolve pull request/issue metadata
