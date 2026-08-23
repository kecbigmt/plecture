# The tmux plugin's effect is `pane`

The tmux plugin declared its interactive-endpoint effect as `tmux`, repeating the plugin's own name. It is now `pane`, which is what the effect actually creates. The examples below assume the catalog is registered under the alias `official`, the alias this repository's own catalog uses by convention; substitute your own alias wherever you registered it under something other than `official`.

## The node-id hazard

A workflow node's id defaults to the referenced definition's id (`docs/language/declarations.md`'s node-id defaulting). A node that writes `uses = "official.tmux.tmux"` with no `id` defaults to node id `tmux`; the same node rewritten to `uses = "official.tmux.pane"` defaults to node id `pane` instead. State is keyed by node id, so an unpinned node's id moves with the rename, and:

- the session's stored `tmux` node state is orphaned — a fresh `pane` instance starts with no history, and the old entry is never read again;
- every `{ from = "nodes.tmux.outputs.*" }` projection elsewhere in the workflow stops resolving, because the node it names no longer exists.

**Pin the id to keep the node key**, and no state migration is needed at all:

```toml
[[my_workflow.nodes]]
id   = "tmux"                     # was defaulted; now stated, so it survives the rename
uses = "official.tmux.pane"       # was "official.tmux.tmux"
```

With `id` pinned, the node key stays `tmux`, existing session state for that node is read normally, and every `nodes.tmux.outputs.*` projection keeps resolving.

## Backup

```bash
CONFIG_HOME="${PLECT_CONFIG_HOME:-$HOME/.config/plect}"
DATA_HOME="${XDG_DATA_HOME:-$HOME/.local/share}/plect"
STAMP="$(date -u +%Y%m%dT%H%M%SZ)"
cp -r "$CONFIG_HOME" "$CONFIG_HOME.migration-backup.$STAMP"
cp "$DATA_HOME/state.json" "$DATA_HOME/state.json.migration-backup.$STAMP"
```

## Rewrite the reference

Find every reference:

```bash
CONFIG_HOME="${PLECT_CONFIG_HOME:-$HOME/.config/plect}"
grep -rn "uses *= *['\"]" "$CONFIG_HOME"
```

TOML accepts either quote, so the pattern matches both. A hit naming your own declaration stays as it is; only one selecting the tmux plugin's effect changes — that is, one whose value is the tmux plugin's address under whatever alias you registered it (`official.tmux.tmux` for the `official` alias). For each such node:

1. Add `id = "<the id the node currently defaults to>"` if the node has no `id` already.
2. Change `uses` from `<alias>.tmux.tmux` to `<alias>.tmux.pane`.

A reference that still names the old id resolves to nothing and says which address it meant, so `plect workflow list` will name any you miss.

## Nothing else moves

- **No effect behavior changed.** The rename touches only the table name the plugin declares under; every setup/cleanup/health/terminal action is unchanged.
- **The invocation record moved headings only.** `plugins/tmux/testdata/effect-invocations.txt` renamed its `== tmux / ...` headings to `== pane / ...`; every invocation line stayed byte-identical.
