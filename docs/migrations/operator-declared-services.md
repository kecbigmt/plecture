# Migrate to operator-declared services

Plugin manifests use schema version 3. The operator starts resident services from
`<config-home>/services/*.toml`; enabling a plugin alone starts no service.
Plugin `[[services]]` and `required_env` are retired. There is no fallback
reader for either field.

## Back up

Stop the resident daemon. Back up the active config home, including
`catalogs.toml`, `plect.lock`, and any existing service credentials. Back up
the data home before replacing a plugin with authoritative service state.
Keep the backups outside both homes until the migration is verified.

## Move declarations

Update each plugin manifest to `schema_version = 3`. For each executable that
may run as a service, add `service = true` and an inline
`[executables.config_schema]`. Remove every plugin `[[services]]` table and
`required_env` entry.

For each service the operator intends to run, create a file under
`<config-home>/services/`:

```toml
[[services]]
name = "worker"
executable = "local/worker/worker"
env_file = "/path/to/private/worker.env"
args = ["serve"]
restart = "on-failure"
health = { type = "process" }

[services.config]
region = "west"
```

The executable reference uses the enabled catalog alias, plugin path, and
executable name. Put secrets in the absolute-path env file as `KEY=VALUE`
lines and make it owner-readable only, for example mode `0600`. Move
non-secret options into `[services.config]` and validate them with the
plugin's inline schema. `PLECT_*` names are reserved for the runtime and
cannot be set in the env file. Service processes receive that config as their first
JSON line on stdin. Convert service code to use JSON-lines requests and
replies on stdio, and send logs to stderr. Remove any service-local listener,
local HTTP control calls, plect CLI calls, and old socket configuration.

Move authoritative per-resource service state to
`<data-home>/plugins/<plugin>/`. Service processes receive that path as
`PLECT_DATA_DIR`. Actions do not receive it. All plugin executable
invocations receive `PLECT_CACHE_DIR` at
`<cache-home>/plugins/<plugin>/`. Move disposable token and rate-limit caches
there; the cache may be deleted and rebuilt. See
[cache-home root migration](plugin-cache-home-root.md) for explicit cache-home
overrides.

## Verify

Start the daemon with the new plugin manifests and service declarations.
Check each declared service's status and stderr log. Check that invalid
`[services.config]` names its field and prevents startup. Publish a resource
event and confirm that a service receives a correlated `published` reply and
subscription snapshot/deltas. Confirm that an action reading `service.config`
without an operator declaration fails, while an action using the executable
as a helper receives only the plugin cache directory.

After verification, remove obsolete plugin-owned subscription files and
service listener configuration from the backed-up deployment. Keep the backup
for rollback with the pre-migration binary.
