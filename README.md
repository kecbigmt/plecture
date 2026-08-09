# Plect

**Weave work into structure.**

Plect exists to let individuals and teams compose their own systems for
autonomous work without committing to a particular agent, VCS, execution
environment, workspace technology, or communication tool. Plect owns the
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

```bash
go build ./...
```

## Test

```bash
go test ./...
```

## License

Apache License 2.0 — see [LICENSE](./LICENSE).
