# Sennit

**Weave work into structure.**

Sennit exists to let individuals and teams compose their own systems for
autonomous work without committing to a particular agent, VCS, execution
environment, workspace technology, or communication tool. Sennit owns the
durable structure of work: identity, lifecycle, relationships, observation,
verification, and handoff.

This identity is a target, not a description of the current code: the first
release ships with a concrete set of providers (tmux, git worktrees, GitHub,
Slack) baked in. Independence is the boundary this project is extracted
around, not a feature gate for v1.

## Layout

```
app/         CLI + MCP server: session lifecycle, task DAG, state, dispatch
contracts/   Shared data contracts between the CLI and plugins
plugins/     Standalone processes (channel relay, GitHub watcher, Slack adapter)
```

`app`, `contracts/*`, and `plugins/*` are independent Go modules wired
together by the repository's `go.work`.

## Build

`app` and each package under `contracts/` and `plugins/` is its own Go
module — `go.work` at the repository root wires them together for editor
tooling, but a bare `go build ./...` from the root doesn't resolve (the root
itself isn't a module). Build per module instead:

```bash
go build ./app/...
go build ./contracts/atomicfile/...
go build ./contracts/channel-protocol/...
go build ./contracts/event/...
go build ./contracts/state/...
go build ./plugins/channel-server/...
go build ./plugins/github-watcher/...
go build ./plugins/slack-adapter/...
```

## Test

```bash
go test ./app/...
go test ./contracts/atomicfile/...
go test ./contracts/channel-protocol/...
go test ./contracts/event/...
go test ./contracts/state/...
go test ./plugins/channel-server/...
go test ./plugins/github-watcher/...
go test ./plugins/slack-adapter/...
```

## License

Apache License 2.0 — see [LICENSE](./LICENSE).
