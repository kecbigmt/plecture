# Removed contracts/event's provider-specific Source/Type constants

`contracts/event` exported `SourceSlack`, `SourceClaude`, `TypeSlackMessage`,
`TypeClaudeReply`, and `TypeClaudePermReq` — five named Go constants whose
values were plain strings (`"slack"`, `"claude"`, `"slack.message"`,
`"claude.reply"`, `"claude.permission_request"`). The package's own doc
comment already says provider-specific Source/Type constants belong in
that provider's own package, not in core's event contract, so these are
removed rather than kept as a compatibility shim.

## Backup

Before upgrading, back up `state.json` the same way every migration in
this directory does:

```bash
DATA_DIR="${XDG_DATA_HOME:-$HOME/.local/share}/plect"
BACKUP_DIR="$DATA_DIR/migration-backups/$(date -u +%Y%m%dT%H%M%SZ)"
mkdir -p "$BACKUP_DIR"
cp "$DATA_DIR/state.json" "$BACKUP_DIR/state.json"
```

## No config or persisted-data rewrite is needed

Unlike every other entry in this directory, this change touches neither a
user-owned `~/.config/plect/` (or repo-overlay) declaration nor an on-disk
data format:

- The removed identifiers were aliases for plain strings, not a schema.
  An `Event.Source` or `Event.Type` field already holds a free-form string
  (see `contracts/event`'s own doc comment: the bus core treats both as
  opaque). A previously-written event log with `"source":"slack"` or
  `"type":"claude.reply"` reads back exactly as before — nothing about the
  JSON shape or its interpretation changed.
- No declarative config file names these Go identifiers; they were
  Go-source-only.

There is accordingly no rewrite procedure to run after the backup above.

## What breaks, and the one-line fix

The only thing this breaks is Go source code that referenced one of these
five constants by name. Every such reference lived inside this repository
(`contracts/event`'s module pin in a plugin's `go.mod` is a local
`replace` directive, not a remote version) and is already updated in the
same change that removed them. If a plugin outside this repository
imported one of these constants directly, replace the reference with its
literal string value:

| Removed constant                 | Literal value                    |
|-----------------------------------|-----------------------------------|
| `event.SourceSlack`                | `"slack"`                        |
| `event.SourceClaude`               | `"claude"`                       |
| `event.TypeSlackMessage`           | `"slack.message"`                |
| `event.TypeClaudeReply`            | `"claude.reply"`                 |
| `event.TypeClaudePermReq`          | `"claude.permission_request"`    |

A provider that wants a named constant for its own Source/Type values
should declare it in its own package, per `contracts/event`'s doc comment.
