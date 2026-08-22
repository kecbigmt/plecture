# Resource observer surface migration

The resource observer is the first runtime surface cut over to the ratified
configuration language (`docs/language/`, `plecture.schema.json`). A
`resources/*.toml` file is now an ordinary definition document: a
`[<id>]` table declaring `kind = "resource_observer"`, whose `observe` and
`finalize` are **actions** rather than shell strings rendered through Go
templates.

The change is intentionally breaking. Plecture is pre-1.0, so operators
migrate once instead of relying on a compatibility shim that reads both
forms. A pre-migration observer fails to load with a
`PLECTURE-CFG-KIND-MISSING` diagnostic, so nothing runs against a
half-converted config.

Only configuration you authored yourself needs this procedure. A
catalog-mounted plugin ships its own converted observers — run
`plect plugin update` once the catalog has migrated.

## Backup

```bash
CONFIG_HOME="${PLECT_CONFIG_HOME:-$HOME/.config/plect}"
STAMP="$(date -u +%Y%m%dT%H%M%SZ)"
cp -r "$CONFIG_HOME" "$CONFIG_HOME.migration-backup.$STAMP"
```

Only configuration changes here; `state.json` is untouched, so no
`legacy-migration` run is involved.

## Convert each declaration

Before:

```toml
# resources/github-pull.toml
match   = '^https://github\.com/[^/]+/[^/]+/pull/\d+'
observe = '{{bin "github-issue-pr"}} observe --resource {{.ResourceID | shellQuote}} --workspace-dir-path {{.WorkspaceDirPath | shellQuote}}'

[state_schema]
type = "object"

[state_schema.properties]
checks_status = { type = "string" }
```

After:

```toml
# resources/github-pull.toml — the filename is no longer the id
[github_pull]
kind  = "resource_observer"
match = '^https://github\.com/[^/]+/[^/]+/pull/\d+'

[github_pull.observe]
type = "exec"
bin  = "github-issue-pr"
args = [
  "observe",
  "--resource",
  { from = "resource.id" },
  "--workspace-dir-path",
  { from = "workspace.dir", default = "" },
]

[github_pull.state_schema]
type = "object"

[github_pull.state_schema.properties]
checks_status = { type = "string" }
```

Four rules cover the mechanical part:

1. **The id is the table name, not the filename.** It must match
   `^[A-Za-z_][A-Za-z0-9_]*$`, so an observer that used to be
   `local-okf.toml` becomes `[local_okf]`. The filename and directory carry
   no identity, and one document may declare several observers.
2. **Every field moves under that table**, including `state_schema` and
   `state_schema_file`.
3. **`observe` and `finalize` become action tables.** Prefer `type = "exec"`:
   there is no shell, so no quoting question arises and `shellQuote`
   disappears. Keep `type = "shell"` where the logic is genuinely
   imperative — the script is then *literal* (no `{{ }}` anywhere in it) and
   every value it needs is declared in a `bind` table, reaching the script
   through a private binding file rather than through rendered source.
4. **`{{bin "name"}}` becomes `bin = "name"`** on an exec action, or
   `{ bin = "name" }` in an argv element or a bind value.

### Template variable to root

| Was | Becomes | Where |
|---|---|---|
| `{{.ResourceID}}` | `{ from = "resource.id" }` | `observe`, `finalize` |
| `{{.WorkspaceDirPath}}` | `{ from = "workspace.dir" }` | `observe` |
| `{{.Branch}}` | `{ from = "workspace.branch" }` | `observe` |
| `{{.SessionName}}` | `{ from = "session.name" }` | `finalize` |
| `{{.Revision}}` | `{ from = "resource.revision" }` | `finalize` |
| `{{.JudgesJSON}}` | `stdin = { json = { from = "judges" } }` | `finalize` |
| `{{.Instance}}` | *retired, no replacement* | `finalize` |

A root that has nothing to report is now **absent** rather than rendered as
the empty string. A standalone `plect resource status` call has no session,
so `workspace.dir` and `workspace.branch` are absent there: a projection of
either fails unless it declares `default` (usually `default = ""`, to keep
passing the flag) or `optional = true`, which propagates the absence — in an
`args` list by dropping that element, and in a `bind` table by unsetting the
shell variable, which a script tells from empty with `${var+set}`. The
binding file unsets it explicitly rather than merely skipping the
assignment, so an ambient variable of the same name in Plecture's own
environment cannot reach the script through a binding that declared no
value.

`{{.Instance}}` is gone because the finalize surface is about the resource
and the evidence, not about which instance happened to reach completion; an
observer that needs to record an instance name has to be given it by the
resource identifier itself.

### Judge evidence

Judge evidence no longer arrives as a quoted heredoc inside rendered shell.
An exec action declares it as standard input:

```toml
[goal.finalize]
type = "exec"
bin  = "okf-goal"
args = [
  "resource",
  "finalize",
  "--resource",
  { from = "resource.id" },
  "--revision",
  { from = "resource.revision" },
]
stdin = { json = { from = "judges" } }
```

The serialized array is byte-identical to what `{{.JudgesJSON}}` produced,
minus the trailing newline the heredoc added, so an executable that JSON-
decodes its standard input needs no change. An empty judge set still
arrives as `[]`.

### Two rules that tightened

- **A field outside the observer surface is now a load error**
  (`PLECTURE-CFG-FIELD-UNKNOWN`) rather than a silently ignored key. The
  surface is exactly `match`, `observe`, `finalize`, `state_schema`, and
  `state_schema_file`. The long-retired `execution` key is caught by this
  rule now.
- **Shipped plugin config cannot name another plugin's executable.** Inside
  a plugin, `bin` takes a bare name resolved against that plugin's own
  `[[executables]]`. The catalog-qualified
  `<alias>/<plugin-path>[/<executable-name>]` form stays available to
  user-authored config, which is the only layer that knows the alias.

## Verification

Load the converted config and observe a real resource:

```bash
plect resource status <resource-id> --json
```

A successful run prints the observer's id and its observed state. Confirm no
pre-migration observer is left behind:

```bash
CONFIG_HOME="${PLECT_CONFIG_HOME:-$HOME/.config/plect}"
for f in "$CONFIG_HOME"/resources/*.toml; do
  [ -e "$f" ] || continue
  grep -q 'kind *= *"resource_observer"' "$f" || echo "unconverted: $f"
done
```

That should produce no output. Every converted document declares its kind;
a pre-migration one declares none, which is exactly the diagnostic the
loader reports.

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

Then use a plect binary built before this change — the restored declarations
are invisible to a post-migration loader.
