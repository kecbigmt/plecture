# No-channel-server interactive Claude migration

This migration covers a consequence of the plugin split decided in
`docs/adr/2026-08-17-plugin-boundary-contracts.md`: Claude Code delivery
uses the `channel-server` structured transport as its supported path. A
no-channel-server interactive Claude configuration — one that instead
relied on typing events into the tmux pane, the way an interactive Codex
session does — is outside the supported surface after this split.

## What changed

Before the split, the merged `session/runtime` plugin shipped one
`tmux_send_keys` channel usable by any interactive pane, regardless of
which agent CLI ran there — including a Claude Code pane with no
`channel-server` installed. The split plugins package that channel inside
`codex` (renamed `terminal_submit`, `config/channels/terminal_submit.toml`)
because the burst-split/retry/readiness composition it performs describes
the Codex TUI contract specifically (`docs/design/plugin-boundary-contracts.md`'s
Codex Terminal Submit) — it is no longer bundled with `claude`.

The `claude` plugin's own delivery path is `config/channels/claude.toml`,
which requires `channel-server` to be installed and reachable on the
session's `PATH`. A workflow using only the `claude` plugin, with no
`channel-server` binary available, now has no shipped fallback channel for
delivering later events into that Claude Code pane.

## Who this affects

Only a workflow that:

1. uses the `claude` plugin's `claude` task, **and**
2. does not have `channel-server` built/installed (so `claude.toml`'s
   setup never registers the MCP delivery path), **and**
3. previously relied on the merged plugin's `tmux_send_keys` channel to
   deliver later events into that Claude Code pane anyway.

A workflow with `channel-server` installed is unaffected — this was
already the supported, recommended path, and nothing about it changed.

## Migration paths

### Recommended: install channel-server

Build and install `channel-server` (the `claude` plugin builds it
automatically via `plect plugin add`/`update`; see the Package format
section of `docs/design/plugin-packaging.md`). Once it is on `PATH`,
`claude.toml`'s setup registers it as an MCP server automatically — no
workflow change needed.

### Alternative: adopt a raw terminal-submit channel yourself

If you have a specific reason to keep a no-channel-server interactive
Claude configuration, copy `codex`'s `config/channels/terminal_submit.toml`
into your own trusted config overlay (`~/.config/plect/channels/` or a
repo overlay's `.plect/channels/`) under a name of your choosing, and wire
it into your Claude workflow's `[[event.channel]]`. The channel is already
provider-neutral — it composes `{{terminal "send_text"}}`/`{{terminal
"send_keys"}}`/`{{terminal "capture"}}` against whichever task in the plan
declares `[terminal]`, so it works unmodified against a Claude Code pane
the same way it works against a Codex one. No `{{bin ...}}` references or
`codex`-specific config are involved, so a straight copy is sufficient.

## Verification

Confirm delivery reaches the pane after migrating:

```bash
plect up <session>
plect event publish <session> --type user.emit --body "test delivery"
plect capture <session>
```

The captured pane output should show the delivered text.

## Rollback

There is no config to roll back — this migration only concerns which
plugin ships a channel definition. Reverting to a plect binary built
before this change restores the merged `session/runtime` plugin's shared
`tmux_send_keys` channel, if you are also reverting the plugin directory
layout itself.
