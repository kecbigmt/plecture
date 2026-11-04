# Move delivery hooks to resource declarations

Workspace provider declarations no longer accept `subscribe` or `unsubscribe`.
Move each hook to the resource declaration that recognizes the same identifier.

## Backup

Before editing local configuration, copy its directory:

```bash
config_dir="${PLECT_CONFIG_HOME:-$HOME/.config/plect}"
stamp="$(date -u +%Y%m%dT%H%M%SZ)"
cp -a "$config_dir" "$config_dir.migration-backup.$stamp"
```

Back up any local plugin configuration you edit in the same way. No session
state format changes are needed.

## Update declarations

For each workspace provider `subscribe` or `unsubscribe` action, find or add a
`kind = "resource_observer"` declaration whose `match` recognizes the
resource identifiers. Move the action under that declaration's corresponding
table. Keep the hook's `resource.id`, `session.name`, and `session.branch`
projections. A resource declaration also requires an `observe` action.

Remove both delivery tables from the workspace provider. Keep its `setup` and
`cleanup` actions for workspace acquisition and release. The shipped GitHub
plugin already has the migrated issue and pull request declarations; update
any local copies of its workspace provider before loading them.

Run `plect subscribe <resource-id> --session <existing-session>` for each
resource type to verify that its new hook runs. For resources with an
`inputs_schema`, pass an object with `--inputs` or `--inputs-file`.
