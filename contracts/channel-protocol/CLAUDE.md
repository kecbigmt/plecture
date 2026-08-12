# channel-protocol — Developer Guide

## Responsibility

Shared message type definitions between channel-server and adapters. Zero external dependencies (standard library only).

## Dependency rules

- Must not import other tools (`channel-server`, `slack-adapter`, `plecture`)
- Must not depend on external libraries (standard library only, e.g. `encoding/json`)

## Impact of changes

Changing these types affects the build of:
- `channel-server`
- `slack-adapter`
- future adapters

After a change, verify `go build ./...` and `go test ./...` for all dependent modules.

## Type naming convention

Types must be source-independent. Do not use Slack-specific names (e.g. `SlackMessagePayload`).
