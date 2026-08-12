# channel-server — Developer Guide

## Responsibility

Generic message delivery to Claude Code. **It has no knowledge of message sources (Slack, Discord, etc.).**

- Receives messages from external adapters over a Unix socket
- Pushes messages to Claude Code via MCP `claude/channel`
- Replies via the `reply` tool; relays approve/deny via `claude/channel/permission`

## Dependency rules

- Must not import `slack-adapter` or `plecture` code
- Must not call the Slack API directly
- Protocol types are defined in the `plecture/contracts/channel-protocol` module and must stay source-independent

## Package exposure

- `server/` is a public package (not `internal/`). `slack-adapter`'s tests use `server.NewSocketListener` etc.
- Protocol types have already been split out into the `plecture/contracts/channel-protocol` module; they are not channel-server-specific.

## Protocol

Framing over the Unix socket is a 4-byte big-endian length prefix + JSON payload. Message types are defined in the `plecture/contracts/channel-protocol` module.

| Message type | Direction | Purpose |
|---|---|---|
| `register` | adapter → server | Connection registration |
| `message` | adapter → server | Text message delivery |
| `reply` | server → adapter | Reply |
| `permission` | server → adapter | Permission prompt |

## Testing

```bash
go test ./...
```

## Directory layout

```
cmd/channel-server/   entry point (CHANNEL_SOCKET_PATH required)
server/               MCP server, Unix socket listener, MessageSender interface
```
