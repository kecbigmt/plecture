# Channel surface migration

The channel is the second runtime surface cut over to the ratified
configuration language (`docs/language/channels.md`,
`plecture.schema.json`). A `channels/*.toml` file is now an ordinary
definition document: a `[<id>]` table declaring `kind = "channel"`, whose
delivery fields carry **values** rather than Go templates.

The change is intentionally breaking, and a pre-migration channel fails to
load with a `PLECTURE-CFG-KIND-MISSING` diagnostic. Only configuration you
authored yourself needs this procedure — a catalog-mounted plugin ships its
own converted channels, so run `plect plugin update` once the catalog has
migrated.

## Backup

```bash
CONFIG_HOME="${PLECT_CONFIG_HOME:-$HOME/.config/plect}"
STAMP="$(date -u +%Y%m%dT%H%M%SZ)"
cp -r "$CONFIG_HOME" "$CONFIG_HOME.migration-backup.$STAMP"
```

Only configuration changes; `state.json` is untouched.

## Convert each declaration

Before:

```toml
# channels/slack_thread.toml
type = "exec"
command = "curl"
args = [
  "-sf", "-X", "POST",
  '{{.Inputs.base_url}}/messages',
  '-d', '{"thread_ts":{{json .Inputs.thread_ts}},"text":{{if .Event.body}}{{json .Event.body}}{{else}}{{json .Event.summary}}{{end}}}',
]
timeout = '{{.Inputs.post_timeout}}'

[input_schema]
base_url     = { type = "string", required = true }
thread_ts    = { type = "string", required = true }
post_timeout = { type = "string", default = "10s" }
```

After:

```toml
# channels/slack_thread.toml — the filename is no longer the id
[slack_thread]
kind    = "channel"
type    = "exec"
command = "curl"
args = [
  "-sf",
  "-X",
  "POST",
  { expr = "inputs.base_url + '/messages'" },
  "-d",
  { json = { thread_ts = { from = "inputs.thread_ts" }, text = { expr = "event.body != '' ? event.body : event.summary" } } },
]
timeout = { from = "inputs.post_timeout" }

[slack_thread.input_schema]
base_url     = { type = "string", required = true }
thread_ts    = { type = "string", required = true }
post_timeout = { type = "string", default = "10s" }
```

The same four mechanical rules the resource observer migration states apply
here: the id is the table name (matching `^[A-Za-z_][A-Za-z0-9_]*$`), every
field moves under that table, `{{bin "name"}}` becomes `bin = "name"`, and
`input_schema` keeps its per-key shorthand unchanged.

### Template action to value

| Was | Becomes |
|---|---|
| `{{.Event.<field>}}` | `{ from = "event.<field>" }` |
| `{{index .Event.metadata "url"}}` | `{ from = "event.metadata.url" }` |
| `{{with index .Event.metadata "url"}}{{.}}{{end}}` | `{ from = "event.metadata.url", default = "" }` |
| `{{.Inputs.<key>}}` | `{ from = "inputs.<key>" }` |
| `{{ json .Event }}` | `{ json = { from = "event" } }` |
| `{{json .Inputs.x}}` inside a hand-built JSON string | one `{ json = { ... } }` operand for the whole document |
| `{{terminal "send_text"}}` | `{ terminal = "send_text" }` |
| `{{if .Event.body}}…{{else}}…{{end}}` | `{ expr = "event.body != '' ? event.body : event.summary" }` |
| `"{{.Inputs.base_url}}/messages"` | `{ expr = "inputs.base_url + '/messages'" }` |

A projection preserves its value's type; only a `{ json = … }` operand
serializes. So a JSON payload is now **one** operand whose leaves are
projections, rather than a string with `{{json …}}` holes: the serializer
quotes and escapes, and the document's shape is no longer a template that
could be malformed by an unexpected value.

CEL has no `has()` in this profile. Test a metadata key with the `in`
operator — `'url' in event.metadata ? … : …` — or project it with a
`default`.

### The new `shell` primitive

A delivery that is genuinely imperative — splitting a keystroke burst,
polling a readiness predicate, retrying with backoff — declares
`type = "shell"` instead of wrapping itself in `command = "bash"` with the
script as an `args` entry:

```toml
[terminal_submit]
kind    = "channel"
type    = "shell"
timeout = "45s"

script = '''
sh -c "$send_text" terminal-send-text "$message"
sleep 1
sh -c "$send_keys" terminal-send-keys Enter
'''

[terminal_submit.bind]
send_text = { terminal = "send_text" }
send_keys = { terminal = "send_keys" }
message   = { expr = "'[' + event.type + '] ' + event.body" }
```

The script is literal and its values arrive through a private binding file,
so the positional-argument juggling (`text="$4"`) that a `bash -c` channel
needed goes away, and the event is data rather than an operand of the
command.

### Two rules that tightened

- **A field outside the channel surface is now a load error**
  (`PLECTURE-CFG-FIELD-UNKNOWN`). The surface is `type`, `input_schema`,
  `timeout`, plus `path`/`body` for `unix_socket` and the action fields
  (`bin`/`command`/`args`/`stdin`, or `script`/`bind`) for a process
  delivery. Mixing the two sets is `PLECTURE-CFG-ACTION-VARIANT`.
- **`timeout` reads `inputs.*` only**, enforced at load
  (`PLECTURE-CFG-CHANNEL-TIMEOUT-ROOT`). A literal deadline is also parsed
  at load now, so a typo like `"5 seconds"` fails there rather than on the
  first event the channel is asked to deliver.

An `exec` channel gained `stdin`, for a payload that should not be visible
in the process table.

## Verification

```bash
plect workflow show <workflow-id>
```

That loads every channel a workflow references and cross-checks its inputs
against each `input_schema`. Confirm nothing is left in the old form:

```bash
CONFIG_HOME="${PLECT_CONFIG_HOME:-$HOME/.config/plect}"
for f in "$CONFIG_HOME"/channels/*.toml; do
  [ -e "$f" ] || continue
  grep -q 'kind *= *"channel"' "$f" || echo "unconverted: $f"
done
```

## Rollback

```bash
CONFIG_HOME="${PLECT_CONFIG_HOME:-$HOME/.config/plect}"
rm -rf "$CONFIG_HOME"
mv "$CONFIG_HOME.migration-backup.$STAMP" "$CONFIG_HOME"
```

Then use a plect binary built before this change.
