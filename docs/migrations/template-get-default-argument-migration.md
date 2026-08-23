# `get` default-argument migration

The `get` template helper now takes exactly three arguments:
`{{get m "key" "default"}}`. The two-argument form is gone. This migration
covers user-owned config — task definitions, workflows, channels, resource and
workspace-provider files, and Markdown instruction templates — under
`~/.config/plect/`, a repository's `.plect/` overlay, or a user-owned plugin.

Decided in [`docs/adr/2026-08-18-template-get-default-argument.md`](../adr/2026-08-18-template-get-default-argument.md)
(superseded by [`docs/adr/2026-08-23-template-retirement.md`](../adr/2026-08-23-template-retirement.md),
which retires the `get` helper along with the rest of the template engine —
see [`template-retirement-migration.md`](template-retirement-migration.md)).
This repository's own shipped catalog is already migrated as part of the same
change.

The change is intentionally breaking. Plecture is pre-1.0, so config authors
migrate once instead of relying on a shim that keeps the invisible default
alive. A remaining two-argument call fails at render with an error naming the
template site — it does not silently render empty.

## Backup

Before editing anything, copy the config trees you are about to touch:

```bash
STAMP=$(date -u +%Y%m%dT%H%M%SZ)
cp -a ~/.config/plect "$HOME/.config/plect.migration-backup.$STAMP"
[ -d .plect ] && cp -a .plect ".plect.migration-backup.$STAMP"
```

## Find every two-argument call

```bash
grep -rnE 'get +[.$][A-Za-z0-9_.]+ +\\?"[^"]*\\?" *(\}\}|\||-\}\})' \
  ~/.config/plect .plect
```

The trailing group is what makes a call two-argument: the key literal is
followed directly by the end of the action (`}}` or `-}}`) or by a pipe,
rather than by a third argument.

## Rewrite each call

### A plain optional read

Add `""` — the empty string is what the two-argument form yielded, so the
render is unchanged:

```toml
# before
setup = '''
EXTRA='{{get .Inputs "instruction"}}'
'''

# after
setup = '''
EXTRA='{{get .Inputs "instruction" ""}}'
'''
```

A piped call takes the default before the pipe:

```toml
# before
setup = 'goal bootstrap --assignees {{get .Inputs "assignees" | shellQuote}}'

# after
setup = 'goal bootstrap --assignees {{get .Inputs "assignees" "" | shellQuote}}'
```

For the mechanical cases, `sed` handles the whole tree:

```bash
find ~/.config/plect .plect -type f \( -name '*.toml' -o -name '*.md' \) -print0 |
  xargs -0 sed -i -E \
    -e 's/(get +[.$][A-Za-z0-9_.]+ +"[^"]*")( *(\}\}|\||-\}\}))/\1 ""\2/g' \
    -e 's/(get +[.$][A-Za-z0-9_.]+ +\\"[^"]*\\")( *(\}\}|\||-\}\}))/\1 \\"\\"\2/g'
```

Run the `grep` above again afterwards; it must print nothing.

### The if/else default idiom

An optional read wrapped in a conditional to supply a default collapses to a
single call. `sed` does not do this one — rewrite it by hand:

```toml
# before
inputs.model = '{{if get .SessionInputs "model"}}{{get .SessionInputs "model"}}{{else}}fable{{end}}'

# after
inputs.model = '{{get .SessionInputs "model" "fable"}}'
```

The two forms render identically when the key is absent (`fable`) and when it
holds a real value (that value). They differ when the key is present and holds
the empty string: the conditional yielded `fable`, and the collapsed form
yields the empty string, because a present empty value is a value. If a
workflow deliberately relies on `--input model=` meaning "unset", drop that
input instead of passing it empty.

## Adopt the literal-string quoting style

A template action quotes its own arguments with double quotes, so a
template-bearing TOML value reads better as a TOML literal string:

```toml
# before
inputs.task = "{{get .SessionInputs \"task\" \"\"}}"

# after
inputs.task = '{{get .SessionInputs "task" ""}}'
```

This is style, not a breaking change — an escaped basic string keeps working.

## Verify

```bash
plect workflow list
plect template render <name> --session <session>
```

A surviving two-argument call fails the render with
`wrong number of args for get: want 3 got 2`, naming the template and the
position of the call.
