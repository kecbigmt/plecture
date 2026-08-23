# Config template vocabulary

Markdown instruction templates are Go `text/template` strings rendered against
a session's own variable bundle. This document specifies how a reference
resolves, how optional access is written, and how a template-bearing value is
quoted, for that one surface: every other configuration surface states its
values in the configuration language (`docs/language/values.md`), where a
projection is `{ from = "<root>.<path>" }` rather than a template action.

Decision record: [`docs/adr/2026-08-18-template-get-default-argument.md`](../adr/2026-08-18-template-get-default-argument.md).

## References are optional by default

An absent key renders as the literal `<no value>` rather than failing the
render, which is precisely why optional access matters here. The variable
bundle mirrors what a task's own surface offers — `.SessionName`,
`.ResourceID`, `.WorkspaceDirPath`, `.Workflow.outputs.<key>`,
`.SessionInputs.<key>`, and the declaring definition's own `.Inputs.<key>` —
so a template reads the same way as the definition that delivers it.

```markdown
Resolve the issue at {{.ResourceID}} in {{.WorkspaceDirPath}}.
```

## Optional access states its own default

`{{get m "key" "default"}}` is how a template guards a variable that may be
absent. All three arguments are required, so every optional access carries, at
the site, the value an absent key produces.

```markdown
Focus this pass on {{get .SessionInputs "focus" "whatever the issue asks for"}}.
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

```markdown
{{if get .SessionInputs "pr_url" ""}}Review {{.SessionInputs.pr_url}}.{{end}}
```

A call with any argument count other than three fails the render with an error
naming the template site.

## Control flow is unsettled

A template action holding anything but a dotted path — a conditional, a range,
a pipeline — is a transitional form. `docs/language/tasks.md` defers what
control flow an instruction body may express, and this pass survives until
that decision lands.
