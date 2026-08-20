# Reserved root files

Three files in the user config home are reserved. They carry machine-wide
settings and resolution state rather than definitions, and a definition table
cannot appear in one.

```text
config.toml
catalogs.toml
plect.lock
```

Because they are reserved, discovery skips them: a definition root's recursive
sweep reads every other `.toml` file as a definition document.

## config.toml

<!-- fixture: config/config.toml -->
```toml
workspace_dirs_root = "~/worktrees"
resource_allowlist  = ["^https://github\\.com/kecbigmt/"]
plugin_dirs         = ["~/.config/plect/plugins"]
detached            = true
channels            = ["notify"]

[inputs_schema]
type                 = "object"
additionalProperties = false

[inputs_schema.properties]
task = { type = "string" }
```

| Field | Meaning |
|---|---|
| `workspace_dirs_root` | Where workspace directories are created. |
| `resource_allowlist` | Patterns a resource identifier must match to be accepted. |
| `plugin_dirs` | Additional plugin mount directories, after the catalog-resolved ones. |
| `detached` | Whether dispatch detaches by default. |
| `channels` | Channel definitions delivering for every session. |
| `inputs_schema` | Contract for the session inputs this machine accepts. |

`workspace_dirs_root` is the value a workspace provider projects as
`config.workspace_dirs_root`.

## catalogs.toml

`catalogs.toml` registers catalog aliases and the plugins enabled under each.
An alias is user-local: it is what makes a catalog-qualified reference
resolvable on this machine, and it is why a plugin author can never write one.

<!-- fixture: config/catalogs.toml -->
```toml
schema_version = 1

[[catalogs]]
alias   = "official"
source  = "https://github.com/kecbigmt/plecture"
subdir  = "plugins"
plugins = ["tmux", "claude", "github"]
```

`subdir` bounds the fetch and verify trust space to a subtree of the source
rather than the whole repository.

## plect.lock

`plect.lock` records what resolution produced: each catalog's resolved
revision, and each enabled plugin's path, content hash, version, and minimum
`plect` version. Mounting verifies against it and never fetches, so a plugin
problem fails the whole config load rather than silently mounting nothing.

An `editable` plugin entry is exempt from content verification, which is what
makes local plugin development possible without rewriting the lock on every
edit.

## Validation rules

- A definition table in a reserved root file is a load error.
- An unknown field is a load error rather than being ignored.
- `schema_version` is required in `catalogs.toml` and `plect.lock`.
- A catalog alias matches `^[A-Za-z0-9][A-Za-z0-9_-]*$`.
- Every `channels` entry resolves to a definition of kind `channel`.
- A missing `catalogs.toml` means no catalogs are registered, which is not an
  error.
