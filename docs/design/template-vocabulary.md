# Config template vocabulary

Plect config is a wiring language: task hooks, node inputs, channel arguments,
and instruction templates are Go `text/template` strings rendered against a
session's own render context. This document specifies how a reference
resolves, how optional access is written, and how a template-bearing value is
quoted. Which helpers a surface registers varies, so availability is stated
here only for the helper this document specifies.

Decision record: [`docs/adr/2026-08-18-template-get-default-argument.md`](../adr/2026-08-18-template-get-default-argument.md).

## References are required by default

Rendering uses `missingkey=error`, so a direct field reference is the strict
form: an absent key fails the render rather than producing an empty string.
Required-by-default is the correct marking for wiring — a typo in a reference
is a config bug, and it surfaces at the site that made it.

```toml
setup = 'launch --workspace-dir {{.WorkspaceDirPath | shellQuote}}'
```

Cleanup hooks — a task's and a workspace provider's — render with
`missingkey=zero` instead, because a partial setup must still be torn down. So
do a workflow's `[display]` templates, which read persisted outputs that may
not exist yet. Markdown instruction templates are the third departure: an
absent key renders as the literal `<no value>` rather than failing, which is
precisely why optional access matters there.

## Optional access states its own default

`{{get m "key" "default"}}` is the escape hatch from strictness. All three
arguments are required, so every optional access carries, at the site, the
value an absent key produces.

```toml
inputs.model = '{{get .SessionInputs "model" "fable"}}'
```

| Map entry | Result |
|---|---|
| Key absent | The default |
| Key present, value nil | The default |
| Key present, value `""` | `""` — an empty value is a value |
| Key present, any other value | The value, unchanged |

The default is returned unchanged too, so a non-string default is well
defined: `{{get .Inputs "pid" 0}}`.

Presence testing is written with an explicit empty default:

```toml
setup = '''
if [ '{{get .Prev "sent" ""}}' = "true" ]; then exit 0; fi
'''
```

A call with any argument count other than three fails the render with an error
naming the template site.

## Where `get` is available

`get` is registered for task hooks and node inputs, workspace provider hooks,
resource and subscribe hooks, a workflow's `[display]` templates, and Markdown
instruction templates.

Three surfaces render without it, and a config author reaching for optional
access there has to restructure instead: channel argument templates, chain
input templates, and a workspace provider's session-name resolver.

## Quoting a template-bearing value

A template action quotes its own string arguments with double quotes, so a
template-bearing TOML value is written as a TOML literal string
(single-quoted) and needs no escaping:

```toml
inputs.task = '{{get .SessionInputs "task" ""}}'
send_text   = 'tmux send-keys -t {{.Self.session_name}} -- "$1"'
```

A value that must itself contain a single quote falls back to a TOML basic
string with the escapes that requires. A multi-line hook body uses the
multi-line literal form (`'''`), which is why shipped `setup` scripts can
splice `'{{get .Inputs "model" ""}}'` into shell single quotes directly.
