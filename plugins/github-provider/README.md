# github-provider

The GitHub provider: it maps GitHub issue and pull request URLs to session
ids, and owns the git worktree those sessions work in.

plect's core knows nothing about GitHub. Everything GitHub-shaped lives
here: parsing a resource identifier, resolving the branch a resource maps to,
and knowing that a repository's worktrees live under a `github.com/<owner>/<repo>`
path. The provider executable also creates and removes the worktree, reuses an
existing one, and finds the repository's primary checkout.

## Contents

- `providers/github.toml` — the shipped provider config. Its `[resolver]`
  pair (`match` / `name`) derives a session id offline; `setup` and `cleanup`
  invoke the executable below.
- `cmd/plect-github-provider` — the executable those hooks run. `setup`
  prints the provider outputs contract (a JSON object carrying the reserved
  `workdir` key) on stdout; `cleanup` releases what setup acquired.

## Install

Build the executable and put it on the `PATH` plect's hooks run with:

```bash
go build -o <bindir>/plect-github-provider ./cmd/plect-github-provider
```

Then add this directory to `plugin_dirs` in `~/.config/plect/config.toml` so
`providers/github.toml` is loaded:

```toml
plugin_dirs = ["/path/to/plect/plugins/github-provider"]
```

A workflow opts into it with `provider = "github"`.

## Outputs

`setup` emits `workdir` and `branch`, plus the resource facts a workflow's
templates may want: `url`, `owner_repo`, `owner`, `repo`, and `number`. Read
them in a task or template as `.Workflow.outputs.<key>`.

## Requirements

- `git`
- the `gh` CLI, authenticated — used to resolve a pull request's head branch
  and to resolve a Projects v2 item id to its issue or pull request
