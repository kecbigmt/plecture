# Rename workflow `resume_notice` to `resume_instruction`

The workflow health setting is named `resume_instruction`. The old
`resume_notice` key is rejected when a workflow loads.

## Back up

Back up each configuration layer that contains workflow definitions before
editing it. This includes the config home and any project `.plect` directory
with a workflow overlay. For example:

```bash
CONFIG_HOME="${PLECT_CONFIG_HOME:-${XDG_CONFIG_HOME:-$HOME/.config}/plect}"
STAMP="$(date -u +%Y%m%dT%H%M%SZ)"
cp -a "$CONFIG_HOME" "$CONFIG_HOME.backup.$STAMP"
cp -a .plect ".plect.backup.$STAMP"
```

Run the second `cp` from each project with a `.plect` directory. Skip it when
that directory does not exist.

## Rename the key

Find workflow definitions in each backed-up layer:

```bash
rg -n 'resume_notice' "$CONFIG_HOME" .plect
```

Change each `[<workflow>.health]` entry from `resume_notice = "..."` to
`resume_instruction = "..."`. Preserve the value, including an empty string
that disables the instruction. Then run `plect workflow list` to verify the
edited definitions load. No session state or event log data needs migration.
