# Resource kind migration

A declaration of an external thing is `kind = "resource"` and declares a
`name`. A task document names the resource it is written for with
`resource`. The loader rejects `kind = "resource_observer"` and a task
document's `resource_observer` field, so a configuration that still uses them
fails to load with a diagnostic that points here.

Only configuration you authored yourself needs this procedure. A
catalog-mounted plugin ships its own converted declarations — run
`plect plugin update` once the catalog has migrated.

A workflow population's `resource_observer` field is unchanged by this
migration.

## Backup

```bash
CONFIG_HOME="${PLECT_CONFIG_HOME:-$HOME/.config/plect}"
STAMP="$(date -u +%Y%m%dT%H%M%SZ)"
cp -r "$CONFIG_HOME" "$CONFIG_HOME.migration-backup.$STAMP"
```

Only configuration changes here; durable state is untouched.

## Convert each resource declaration

1. Change `kind = "resource_observer"` to `kind = "resource"`.
2. Add `name`, the resource instance's name, derived from the named captures
   of `match`. It reads no root other than `match.<capture>`, and a capture
   it names must be declared by `match`. Give `match` a named group for each
   part the name uses.

Before:

```toml
[issue]
kind  = "resource_observer"
match = '^https://forge\.example/(?P<project>[^/]+)/issues/(?P<number>\d+)$'
```

After:

```toml
[issue]
kind  = "resource"
match = '^https://forge\.example/(?P<project>[^/]+)/issues/(?P<number>\d+)$'
name  = { expr = "match.project + '-issue-' + match.number" }
```

## Convert each task document

Rename the `resource_observer` field to `resource`. The value is unchanged.

```toml
# before
resource_observer = "issue"
# after
resource = "issue"
```

An extension (`extends`) still declares no `resource`; it inherits its
base's.

## Verify

```bash
plect task show <task-id> --json
```

The output carries `resource` where it carried `resource_observer`. Every
declaration loads without a `PLECTURE-CFG-KIND-UNKNOWN` or
`PLECTURE-CFG-FIELD-UNKNOWN` diagnostic.

## Pinned workflow revisions

A session pins its workflow revision when it is created. A revision recorded
before this change holds the retired form and is not rewritten. Destroy such a
session with the binary that created it before upgrading.
