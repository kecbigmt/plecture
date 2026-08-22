# Workspace provider surface migration

The workspace provider is the third runtime surface cut over to the ratified
configuration language (`docs/language/workspace-providers.md`,
`plecture.schema.json`). A `workspaces/*.toml` file is now a definition
document: a `[<id>]` table declaring `kind = "workspace_provider"`, whose
`setup`, `cleanup`, and `subscribe` are **actions** and whose `name` is a
**value** over the resolver's captures.

This surface backs session creation, so read the whole procedure before
running any of it. A pre-migration provider fails to load with a
`PLECTURE-CFG-KIND-MISSING` diagnostic, which fails `plect create`, `up`,
`destroy`, and `subscribe` alike — nothing half-runs.

Only configuration you authored yourself needs this procedure. A
catalog-mounted plugin ships its own converted providers, so run
`plect plugin update` once the catalog has migrated.

## Backup

```bash
CONFIG_HOME="${PLECT_CONFIG_HOME:-$HOME/.config/plect}"
STAMP="$(date -u +%Y%m%dT%H%M%SZ)"
cp -r "$CONFIG_HOME" "$CONFIG_HOME.migration-backup.$STAMP"
```

## A shipped id changed

The okf plugin's provider was `local-okf`; a definition id must now match
`^[A-Za-z_][A-Za-z0-9_]*$`, so it is **`local_okf`**. Any workflow naming it
has to follow:

```bash
CONFIG_HOME="${PLECT_CONFIG_HOME:-$HOME/.config/plect}"
grep -rln 'workspace_provider *= *"local-okf"' "$CONFIG_HOME/workflows" 2>/dev/null |
  xargs -r sed -i 's/workspace_provider = "local-okf"/workspace_provider = "local_okf"/'
```

The `local-okf://` resource scheme is unchanged — it is a resource
identifier, not a definition id.

## Convert each declaration

Before:

```toml
# workspaces/orchestrator.toml
match = '^owner:(?P<owner>[A-Za-z0-9-]+)$'
name  = '{{.owner}}/_orchestrator'

setup = '''
{{bin "okf-bundle"}} setup \
  --resource {{.ResourceID | shellQuote}} \
  --session {{.SessionName | shellQuote}} \
  --root {{.WorkspaceDirsRoot | shellQuote}} \
  --layout {{get .Inputs "layout_root" "" | shellQuote}}
'''

cleanup = '{{bin "okf-bundle"}} cleanup --workspace-dir {{.Self.workspace_dir | shellQuote}}'
```

After:

```toml
# workspaces/orchestrator.toml — the filename is no longer the id
[orchestrator]
kind  = "workspace_provider"
match = '^owner:(?P<owner>[A-Za-z0-9-]+)$'
name  = { expr = "match.owner + '/_orchestrator'" }

[orchestrator.setup]
type = "exec"
bin  = "okf-bundle"
args = [
  "setup",
  "--resource",
  { from = "resource.id" },
  "--session",
  { from = "session.name" },
  "--root",
  { from = "config.workspace_dirs_root" },
  "--layout",
  { from = "inputs.layout_root", default = "" },
]

[orchestrator.cleanup]
type = "exec"
bin  = "okf-bundle"
args = ["cleanup", "--workspace-dir", { from = "self.outputs.workspace_dir" }]
```

The same mechanical rules the earlier surface migrations state apply: the id
is the table name, every field moves under it, `{{bin "name"}}` becomes
`bin = "name"`, and the schemas keep their shape.

### Template variable to root

| Was | Becomes | Available in |
|---|---|---|
| `{{.ResourceID}}` | `{ from = "resource.id" }` | `setup`, `subscribe` |
| `{{.SessionName}}` | `{ from = "session.name" }` | `setup`, `cleanup`, `subscribe` |
| `{{.WorkspaceDirsRoot}}` | `{ from = "config.workspace_dirs_root" }` | `setup`, `cleanup` |
| `{{.Inputs.<key>}}` | `{ from = "inputs.<key>" }` | `setup`, `cleanup` |
| `{{get .Inputs "<key>" ""}}` | `{ from = "inputs.<key>", default = "" }` | `setup`, `cleanup` |
| `{{.SessionInputs.<key>}}` | `{ from = "session.inputs.<key>" }` | `setup` |
| `{{get .Prev "<key>" ""}}` | `{ from = "prev.<key>", default = "" }` | `setup` |
| `{{.Self.<key>}}` | `{ from = "self.outputs.<key>" }` | `cleanup` |
| `{{.CleanupInputs.<key>}}` | `{ from = "cleanup.inputs.<key>", default = "" }` | `cleanup` |
| `{{.Force}}` | `{ from = "force" }` | `cleanup` |
| `{{.owner}}` in `name` | `{ expr = "match.owner + …" }` | `name` |

Each hook observes only its own roots, so a projection of the wrong one is a
load error rather than an empty value. `subscribe` in particular reads only
`session.name` and `resource.id`: it resolves the provider from the resource
alone, with no workflow in scope to have set a parameter.

`name` is a computation over the resolver's captures, so it uses `expr`
rather than a projection — `{{.owner}}/{{.repo}}-{{.number}}` is string
construction, which is what `expr` is for.

### `shellQuote` is gone, and so is the hazard it patched

An `exec` action passes each value as one argv element, so nothing is spliced
into a command line. A `shell` action's values arrive through a private
binding file, so they are not command text either. Drop every `| shellQuote`
as you convert.

Keep an imperative hook as `type = "shell"` when it genuinely sequences
steps — the shipped github provider's cleanup does, because it releases the
worktree and then unsubscribes the session unconditionally while preserving
the first step's exit status:

```toml
[github.cleanup]
type   = "shell"
script = '''
"$worktree_bin" cleanup --workspace-dir "$workspace_dir" --force="$force"
status=$?
"$watcher_bin" unsubscribe --session "$session_name"
exit $status
'''

[github.cleanup.bind]
worktree_bin  = { bin = "github-worktree" }
watcher_bin   = { bin = "github-watcher" }
session_name  = { from = "session.name" }
workspace_dir = { from = "self.outputs.workspace_dir" }
force         = { from = "force" }
```

A boolean flag that must be spelled `--flag=value` (Go's `flag` package will
not consume a following argument for a bool) is one reason to prefer a shell
action: an exec action's argv cannot join a literal to a value. The
alternative is `{ expr = "'--force=' + string(force)" }`.

### One rule that tightened

A field outside the provider surface is now a load error
(`PLECTURE-CFG-FIELD-UNKNOWN`). The surface is `match`, `name`, `setup`,
`cleanup`, `subscribe`, `inputs_schema`, `inputs_schema_file`,
`outputs_schema`, and `outputs_schema_file`.

## Verification

**`plect workflow show` will not tell you.** It treats a workspace provider's
details as best-effort and swallows a load failure, so it prints a workflow
happily while that workflow's provider is unloadable. Do not read it as an
all-clear.

Two checks that do work. First, structural — every converted document
declares its kind:

```bash
CONFIG_HOME="${PLECT_CONFIG_HOME:-$HOME/.config/plect}"
for f in "$CONFIG_HOME"/workspaces/*.toml; do
  [ -e "$f" ] || continue
  grep -q 'kind *= *"workspace_provider"' "$f" || echo "unconverted: $f"
done
```

Second, the real load, without creating anything: ask for a resource whose
shape no resolver matches. Provider loading happens before name resolution,
so reaching the resolver error proves every provider loaded.

```bash
plect up "does-not-match://x" --workflow <workflow-id>
# want: resource "does-not-match://x" does not match workspace provider ... resolver (...)
# a PLECTURE-CFG-* error instead means a provider is still unconverted
```

A converted provider's `setup` is first genuinely exercised by a session
create, and that is the one hook whose failure leaves a session with no
workspace. Do it against a throwaway resource before relying on it.

## Rollback

```bash
CONFIG_HOME="${PLECT_CONFIG_HOME:-$HOME/.config/plect}"
BACKUP="$CONFIG_HOME.migration-backup.$STAMP"

# CONFIG_HOME comes from the environment, so the restore refuses anything
# that is not recognizably a plect config home rather than acting on a stray
# or empty value.
[ -n "$CONFIG_HOME" ] && [ "$CONFIG_HOME" != "/" ] ||
  { echo "refusing: CONFIG_HOME is \"$CONFIG_HOME\"" >&2; exit 1; }
[ -f "$CONFIG_HOME/config.toml" ] || [ -f "$CONFIG_HOME/catalogs.toml" ] ||
  { echo "refusing: $CONFIG_HOME is not a plect config home" >&2; exit 1; }
[ -d "$BACKUP" ] || { echo "refusing: no backup at $BACKUP" >&2; exit 1; }

# Move the current tree aside instead of deleting it, so a mistake here is
# still recoverable, then put the backup back.
mv "$CONFIG_HOME" "$CONFIG_HOME.rolled-back.$STAMP"
mv "$BACKUP" "$CONFIG_HOME"
```

Once the restored tree is confirmed good, `$CONFIG_HOME.rolled-back.$STAMP`
is the only thing left to remove, by hand.

Then use a plect binary built before this change.
