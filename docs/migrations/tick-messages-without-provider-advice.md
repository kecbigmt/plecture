# Tick messages carry no provider advice

Three messages `plect tick` sends changed shape. No stored state changes,
so nothing in the session database needs rewriting. What needs updating is
anything you wrote that reads these messages: an orchestrator script, a
channel filter, or an agent instruction.

| Message | Before | After |
|---|---|---|
| Kick to an already-active chain target (`user.emit`, source `plect.tick`) | metadata `revision`, `judge_ids`, `pr_url` picked by name; body lines `PR:`, `Revision:`, `Pending judge ids:`; summary `Re-evaluate <session> at revision <rev>` | metadata `input.<key>` for every declared chain input, next to `work_session`, `chain_id`, `instance`, `chain_dedup_key`; body lists each input as `- <key>: <value>` in key order; summary `Re-evaluate <session>` |
| `done_when` kick body | could end with a `Note: mergeable_state=dirty …` rebase line | no such line; a conflict reaches the session as the resource plugin's own event (`github.mergeable` from the github plugin) |
| `review_required` | `reviewer_command` field in `plect tick --json` and `plect status --json`, a `reviewer:` line in `plect tick` text output, and a `Dispatch reviewer:` section in the body | none of these; the body keeps the pending judge leaves and the `plect judge` commands |

## Backup

Back up whatever you are about to edit, for example:

```bash
CONFIG_HOME="${PLECT_CONFIG_HOME:-$HOME/.config/plect}"
STAMP="$(date -u +%Y%m%dT%H%M%SZ)"
cp -r "$CONFIG_HOME" "$CONFIG_HOME.migration-backup.$STAMP"
```

Back up any orchestrator scripts kept outside the config directory the same
way.

## Find the readers

```bash
CONFIG_HOME="${PLECT_CONFIG_HOME:-$HOME/.config/plect}"
grep -rnE 'reviewer_command|Dispatch reviewer|mergeable_state=dirty|metadata\.(revision|judge_ids|pr_url)' "$CONFIG_HOME" <your-script-dirs>
```

For each hit:

1. **`reviewer_command`.** Choose the reviewer workflow in your own
   configuration, typically as a `[[chains]]` entry on the task document. A
   chain spawns the reviewer itself, so nothing has to copy a command out
   of `review_required`.
2. **Kick metadata `revision` / `judge_ids` / `pr_url`.** Read
   `input.<name>` instead, where `<name>` is the key under your chain's
   `[chains.inputs]`. An input you did not declare is no longer present,
   so declare every value a reader needs, for example
   `revision = { from = "resource.state.revision" }`.
3. **The rebase note.** Subscribe the work session to its resource's own
   conflict event. A task bound to a resource is subscribed automatically.
   With the github plugin, update the plugin to this release as well: it
   reports a conflict that already exists when a subscription starts.

## One extra kick after the upgrade

The deduplication key now covers every chain input, losslessly, so it never
equals a key written before the upgrade. Each already-active chain target
therefore receives one more kick for inputs it has already seen. Nothing
needs to be done about it.
