# The GitHub plugin's workspace provider is `worktree`

`official/github` declared its workspace provider as `github`, repeating the plugin's own name where the id should say what the declaration does. It is now `worktree`, which is what the conformance corpus already spelled and what the provider actually acquires.

## Backup

```bash
CONFIG_HOME="${PLECT_CONFIG_HOME:-$HOME/.config/plect}"
STAMP="$(date -u +%Y%m%dT%H%M%SZ)"
cp -r "$CONFIG_HOME" "$CONFIG_HOME.migration-backup.$STAMP"
```

No session state changes, so `state.json` needs no backup for this rename —
see "Nothing else moves" below.

## Rewrite the reference

A workflow that names it takes the new address:

```toml
[my_workflow]
kind               = "workflow"
workspace_provider = "official.github.worktree"   # was "official.github.github"
```

Find every reference:

```bash
CONFIG_HOME="${PLECT_CONFIG_HOME:-$HOME/.config/plect}"
grep -rn "workspace_provider *= *['\"]" "$CONFIG_HOME"
```

TOML accepts either quote, so the pattern matches both — a reference written
`workspace_provider = 'official.github.github'` is as valid as the
double-quoted form and needs the same edit.

A hit naming your own provider stays as it is; only one selecting the GitHub plugin's changes. A reference that still names the old id resolves to nothing and says which address it meant, so `plect workflow list` will name any you miss.

## Nothing else moves

- **No session state.** A workspace provider is named only by the `workspace_provider` line that selects it. Unlike an effect, whose id becomes a workflow node's default id, a provider id is not a key anything is stored under, so no session's tasks or outputs are affected.
- **Every invocation is unchanged.** The golden record moves three headings and not one argument.
- **The effect id `tmux` is not renamed here.** The corpus spells it `pane`, and reconciling that is a separate change: an effect id defaults a node's id, so renaming it moves a state key that live sessions hold.
