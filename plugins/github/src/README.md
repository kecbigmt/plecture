# github/src

The Go source for the `plugins/github` catalog plugin's three executables:
`github-worktree` and `github-issue-pr` (this module's own README section
below) and `github-watcher` (subscribe/serve/gh-api — see its own `--help`).

This module is `plugins/github`'s build source, not something to install
directly — see `plugins/github/README.md` for the worktree-effect/resource
config that invokes these binaries and for install instructions. This README
covers `github-worktree`'s and `github-issue-pr`'s own commands, for
development and testing.

## Commands

- `github-worktree setup --resource <id> --session <name> [--workspace-dirs-root <root>] [--delete-branch true|false] [--watcher-bin <path>]`
  — acquires the worktree for a GitHub resource and prints the worktree
  effect's outputs contract (JSON, carrying the `workspace_dir` key and the
  `--delete-branch` value as `delete_branch`) on stdout. An empty `--workspace-dirs-root` means `$HOME/workspace_dirs`. `--watcher-bin`, when given, routes title/state fetches
  through a `github-watcher gh-api` call so they share its poll loop's rate
  budget; omitted, it calls `gh api` directly.
- `github-worktree cleanup --workspace-dir <path> --branch <branch> [--workspace-dirs-root <root>] [--force] [--delete-branch]`
  — releases what `setup` acquired. `--delete-branch` (the recorded
  `delete_branch` output) opts into reclaiming the branch; left off, the
  branch survives (the safer default for a
  review-only or shared-branch session). Reclaim uses a safe delete
  (`git branch -d`): a branch carrying unmerged commits survives as an
  orphan a later dispatch on the same resource reuses, rather than being
  discarded, regardless of `--delete-branch`.
- `github-issue-pr observe --resource <id> [--watcher-bin <path>]`
  — fetches and classifies a GitHub resource's current state: resource kind,
  CI check rollup, issue completion, revision, linked PR (for an issue
  resource), and mergeability. Backs `resources/issue.toml`'s and
  `resources/pull_request.toml`'s `observe` hook.

## Per-repository base branch override

A repository whose new work should start from a branch other than its
actual GitHub default (a git-flow repo branching from `develop`) can record
that choice at `<repoDir>/.plect/base-branch` — a plain file on the
repository container, not effect input, since an effect's inputs
don't cascade to individual repo layers.

## Requirements

- `git`
- the `gh` CLI, authenticated — used to resolve pull request/issue metadata
