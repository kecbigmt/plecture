# Definition addressing

A mounted plugin's declarations answer to their catalog address — `official.claude.runtime` for the `runtime` effect of the plugin enabled as `official/claude` — while a declaration you own answers to its bare id. That is what lets two plugins declare one id: their addresses differ.

Three things follow, and each can turn a config that loaded yesterday into one that does not:

- A reference from your own config to a plugin's declaration carries the alias. `uses = "runtime"` no longer reaches `official/claude`'s effect.
- Your own declaration sharing an id with a plugin's no longer replaces it. Both exist, at different addresses, and which one a reference selects is decided by how the reference is written.
- Nesting has no reference grammar of its own. `inner` used a slash-separated form; it now uses the same dotted grammar every other reference uses, and the slash grammar belongs to executable lookup alone.

## Backup

```bash
CONFIG_HOME="${PLECT_CONFIG_HOME:-$HOME/.config/plect}"
DATA_HOME="${XDG_DATA_HOME:-$HOME/.local/share}/plect"
STAMP="$(date -u +%Y%m%dT%H%M%SZ)"
cp -r "$CONFIG_HOME" "$CONFIG_HOME.migration-backup.$STAMP"
cp "$DATA_HOME/state.json" "$DATA_HOME/state.json.migration-backup.$STAMP"
```

## Qualify every reference that names a plugin's declaration

An address is the catalog alias, then the plugin path with each `/` written as `.`, then the definition id. A plugin enabled as `official/github` declares `work` at `official.github.work`; one enabled as `official/plugins/acme` declares `runtime` at `official.plugins.acme.runtime`.

Find every reference to review:

```bash
CONFIG_HOME="${PLECT_CONFIG_HOME:-$HOME/.config/plect}"
grep -rn 'uses *= *"\|workspace_provider *= *"\|resource_observer *= *"\|^ *workflow *= *"\|^ *inner *= *"' "$CONFIG_HOME"
```

Each hit is one of two cases, and the id alone does not say which:

- It names **your own** declaration — a file under your config home declares that id. It stays exactly as written.
- It names a **plugin's** declaration. It takes that plugin's address.

`plect plugin list` names the plugins you have enabled, and each plugin's own README lists what it declares. For the catalog this repository ships, the addresses are:

| Declaration | Kind | Address |
|---|---|---|
| `tmux` | effect | `official.tmux.tmux` |
| `runtime` | effect | `official.claude.runtime` |
| `claude_initial_prompt` | effect | `official.claude.claude_initial_prompt` |
| `codex` | effect | `official.codex.codex` |
| `codex_initial_prompt` | effect | `official.codex.codex_initial_prompt` |
| `exec_runtime` | effect | `official.codex.exec_runtime` |
| `gh_guard` | effect | `official.github.gh_guard` |
| `delivery` | channel | `official.claude.delivery` |
| `exec_delivery` | channel | `official.codex.exec_delivery` |
| `terminal_submit` | channel | `official.codex.terminal_submit` |
| `slack` | channel | `official.slack.slack` |
| `github` | workspace provider | `official.github.github` |
| `issue`, `pull` | resource observer | `official.github.issue`, `official.github.pull` |
| `okf_goal` | resource observer | `official.okf.okf_goal` |
| `work`, `review`, `investigate`, `respond` | task document | `official.github.work`, and so on |
| `pursue_goal`, `goal_review` | task document | `official.okf.pursue_goal`, `official.okf.goal_review` |

Substitute your own alias wherever you registered the catalog under something other than `official`.

**You do not have to find them all by reading.** A reference that resolves to nothing names the address it was reaching for:

```text
workflow "claude": node "runtime" references unknown effect "runtime"; an
enabled plugin declares it as "official.claude.runtime" — a reference to a
plugin's declaration carries its catalog address
```

So the practical procedure is to run a command that loads config, fix what it names, and repeat:

```bash
plect workflow list
```

**Node ids do not change.** A node that omits `id` takes the referenced definition's id, not the whole reference, so `uses = "official.claude.runtime"` still produces the node id `runtime`. Every live session's per-node state stays where it is, and no `nodes.<id>.outputs.*` projection moves.

## `inner` uses the dotted grammar

An `inner` reference written in the slash form takes the dotted form:

```toml
[wrapper.inner]
uses = "official.github.work"   # was "official/github/work"
```

The two forms addressed the same declaration, so this is a spelling change with no behavioural difference. Executable references — `bin` — keep the slash grammar and are not touched: selecting an executable has to split an arbitrary-depth plugin path from an executable name, which the dotted grammar cannot do.

## An id you also declare now means yours

This is the one change that is silent, because the reference still resolves — to something else.

Where you declared your own `work` and a plugin also declares `work`, a bare `work` used to reach the plugin's declaration whenever yours was of another kind, because each kind was looked up in its own map. Now yours is what `work` addresses, and the plugin's is `official.github.work`.

It matters most where an id is typed rather than stored — `plect task setup <id>` and a chain's `workflow`:

```bash
plect task setup official.github.work    # the plugin's task document
plect task setup work                    # your own declaration of that id
```

Check your own declarations against the table above before assuming a command still means what it did:

```bash
CONFIG_HOME="${PLECT_CONFIG_HOME:-$HOME/.config/plect}"
grep -rhoP '^\[\K[A-Za-z_][A-Za-z0-9_]*(?=\])' "$CONFIG_HOME" --include='*.toml' | sort -u
```

## Rewrite stored task ids

A dynamic instance records which declaration it runs, so an instance of a plugin's task document holds that document's id and now needs its address. Instances of workflow nodes hold no task id at all — they are keyed by node id, which does not change — so they need nothing.

Write the mapping for what your own state holds, one `<stored id><TAB><address>` per line:

```bash
DATA_HOME="${XDG_DATA_HOME:-$HOME/.local/share}/plect"
python3 - "$DATA_HOME/state.json" <<'PY'
import collections, json, sys
state = json.load(open(sys.argv[1]))
held = collections.Counter()
for session in (state.get("sessions") or {}).values():
    for task in (session.get("tasks") or {}).values():
        if task.get("task_id"):
            held[task["task_id"]] += 1
for task_id, count in sorted(held.items()):
    print(f"{task_id}\t{count} instance(s)")
PY
```

Then rewrite. The script refuses to write anything unless every stored id is mapped, so a missed row is a stop rather than a half-migrated store:

```bash
cat > /tmp/plect-address-map <<'MAP'
work	official.github.work
review	official.github.review
investigate	official.github.investigate
respond	official.github.respond
pursue_goal	official.okf.pursue_goal
goal_review	official.okf.goal_review
MAP

DATA_HOME="${XDG_DATA_HOME:-$HOME/.local/share}/plect"
python3 - "$DATA_HOME/state.json" /tmp/plect-address-map <<'PY'
import json, sys

state_path, map_path = sys.argv[1], sys.argv[2]
addresses = {}
for line in open(map_path):
    line = line.rstrip("\n")
    if not line or line.startswith("#"):
        continue
    stored, address = line.split("\t")
    addresses[stored] = address

state = json.load(open(state_path))
unmapped, rewritten = set(), 0
for session in (state.get("sessions") or {}).values():
    for task in (session.get("tasks") or {}).values():
        stored = task.get("task_id")
        if not stored or stored in addresses.values():
            continue
        if stored not in addresses:
            unmapped.add(stored)
            continue
        task["task_id"] = addresses[stored]
        rewritten += 1

if unmapped:
    sys.exit("unmapped stored task ids, nothing written: " + ", ".join(sorted(unmapped)))

with open(state_path, "w") as out:
    json.dump(state, out, indent=2)
    out.write("\n")
print(f"rewrote {rewritten} stored task id(s)")
PY
```

A stored id that is already an address is left alone, so the script is safe to run twice.

## Rewrite a session frozen to a plugin's workflow

A session records which workflow it runs so a later command can reload its plan, and that record is the workflow's address. A session frozen to a workflow you own needs nothing — its address is its id — but one frozen to a plugin's workflow would look for a name no layer answers to.

Check before assuming there is nothing to do:

```bash
DATA_HOME="${XDG_DATA_HOME:-$HOME/.local/share}/plect"
python3 -c '
import collections, json, sys
state = json.load(open(sys.argv[1]))
held = collections.Counter(s.get("workflow", "") for s in (state.get("sessions") or {}).values())
for workflow, count in sorted(held.items()):
    label = workflow if workflow else "(none)"
    print(label + "\t" + str(count) + " session(s)")
' "$DATA_HOME/state.json"
```

Any name in that list which a plugin declares — rather than your own `workflows/` directory — takes that plugin's address, the same way a reference does. Pass `old=new` pairs:

```bash
DATA_HOME="${XDG_DATA_HOME:-$HOME/.local/share}/plect"
python3 -c '
import json, sys
state_path, pairs = sys.argv[1], sys.argv[2:]
addresses = dict(pair.split("=", 1) for pair in pairs)
state = json.load(open(state_path))
rewritten = 0
for session in (state.get("sessions") or {}).values():
    workflow = session.get("workflow")
    if workflow in addresses:
        session["workflow"] = addresses[workflow]
        rewritten += 1
if rewritten:
    with open(state_path, "w") as out:
        json.dump(state, out, indent=2)
        out.write("\n")
print(f"rewrote {rewritten} session workflow(s)")
' "$DATA_HOME/state.json" shared=official.acme.shared
```

The session's **name** does not change: a workflow id is a segment of every session name it produced, and renaming a live session is not what this is. Two plugin workflows sharing an id therefore produce distinct sessions only when they were created under different `--tag`s — addressing tells the two declarations apart, and tagging is what tells their sessions apart.

The catalog this repository ships declares no workflows, so on a machine using only it this section has nothing to do. Run the check anyway rather than assuming which catalogs are enabled.

## What did not change

- **Every shipped invocation.** Addressing changes which declaration a lookup finds, not what any declaration runs: the golden invocation records are byte-identical.
- **Node instance keys.** See the node-id note above.
- **Two plugin layers with no catalog identity.** A hand-authored `plugin_dirs` entry has no alias, so it has no address; two of them declaring one id is still a load error, because nothing could tell them apart.
- **Expected kinds.** A reference site still requires its kind, unchanged.

## Verification

```bash
plect workflow list
plect ls
```

The first fails on any reference that still names a plugin's declaration without its address, and names the address to write. The second reads every session's stored task ids, so a dynamic instance that was missed shows up as a task whose declaration is gone.
