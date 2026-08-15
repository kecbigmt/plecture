# Plugin packaging, reference resolution, lockfile, and trust model

Status: evolving design.

This document defines the plugin model for distributing reusable Plecture
configuration and executable adapters. The goal is to reduce bootstrap cost
without moving provider, tool, or domain knowledge into core.

The design keeps two concepts: catalog and plugin. A catalog is a trusted
distribution unit: a subtree of a source repository marked by `catalog.toml`.
A plugin is a distributable package inside a catalog. It may contain executable
adapters, config resources, templates, and metadata. A plugin with only
configuration is still a plugin.

## Goals and constraints

- Core owns generic mechanics: fetch, verify, mount, resolve, and fail-loud
  compatibility checks.
- Plugins own technology or domain commitments: provider adapters, resource
  observation, channels, agent/runtime tasks, workflow packs, and templates.
- User-owned config remains the final authority.
- No provider/resource/task/workflow contract changes are required by this
  design. Any discovered gap must become a later contract issue, not an
  assumption in the package format.
- No auto-update. Every add and update is pinned and explicit.

## Package format

A catalog is a directory with a `catalog.toml` manifest at its root. The
manifest's directory bounds the trust space: Plecture does not fetch, verify,
or mount files outside that subtree for plugins in the catalog.

`catalog.toml` is hand-authored catalog metadata, not a generated index:

```toml
schema_version = 1
description = "Example Plecture plugin catalog."

plugins = [
  "github",
  "agent/runtime",
  "agent/claude-tasks",
  "agent/codex-tasks",
]
```

Catalog fields:

| Field | Required | Meaning |
|---|---:|---|
| `schema_version` | yes | Catalog file-format version. Unknown values fail loud. |
| `plugins` | yes | Explicit catalog-relative paths to directories that directly contain `plugin.toml`. |
| `description` | no | Display-only summary for list/show commands. |

Catalogs have no upstream identity field. A catalog name exists only as the
user's local alias in the catalog registration.

Every path listed in `plugins` must resolve inside the catalog trust space
after symlink resolution. `..` escapes and symlink escapes are load errors. A
listed path without `plugin.toml` is a validation error. A `plugin.toml` under
the catalog subtree that is not listed is also a validation error. This strict
rule is intentional: reviewers can audit the exact published plugin set from
one manifest, and catalogs that need fixture manifests can avoid the reserved
filename.

A plugin is a directory with metadata at the root and zero or more standard
subdirectories that are already understood by the config loaders:

```text
plugin.toml
providers/
resources/
tasks/
workflows/
templates/
channels/
environments/
bin/
README.md
LICENSE
```

`plugin.toml` is the only required plugin-local file:

```toml
schema_version = 1
version = "0.3.0"
plect_min_version = "0.8.0"
description = "GitHub resource provider and workflow support."

[[executables]]
name = "plect-github-watcher"
path = "bin/plect-github-watcher"
build = "go build -o bin/plect-github-watcher ./cmd/plect-github-watcher"

[[executables]]
name = "plect-github-provider"
path = "bin/plect-github-provider"
build = "go build -o bin/plect-github-provider ./cmd/plect-github-provider"
```

Plugin metadata fields:

| Field | Required | Meaning |
|---|---:|---|
| `schema_version` | yes | Plugin metadata file-format version. Unknown values fail loud. |
| `version` | no | Informational plugin package version for diagnostics and list/show commands. |
| `plect_min_version` | yes | Minimum plect version required to load the plugin. |
| `description` | no | Human-readable summary for list/show commands. |
| `executables` | no | Relative paths for scripts or binaries shipped by the plugin. |

The catalog-relative path listed in `catalog.toml` is the plugin identity. Full
identity is `<catalog-alias>/<relative-path>`, such as `official/github` or
`official/agent/claude-tasks`. Intra-catalog uniqueness comes from the
filesystem. There is no plugin-owned identity field and no metadata identity
check.

Executable entry fields:

| Field | Required | Meaning |
|---|---:|---|
| `name` | yes | Stable executable identity inside the plugin. |
| `path` | yes | Relative executable path. In the primary script case, this points at a file already present in the source tree. |
| `build` | no | Command run at add/update time to produce a compiled executable from plugin source. |

Build-less script executables are the primary v1 form. They must carry their
interpreter through a shebang. Built binaries are the additional case for
plugins that need compilation.

The standard subdirectories with plugin-layer loader behavior are:

| Directory | Mount behavior |
|---|---|
| `providers/` | Trusted base layer only. Same-id conflicts between plugin layers are load errors; global user definitions replace plugin definitions. |
| `resources/` | Trusted base layer only. Same-id conflicts between plugin layers are load errors; global user definitions replace plugin definitions. |
| `environments/` | Trusted base layer only. Same-id conflicts between plugin layers are load errors; global user definitions replace plugin definitions. |
| `channels/` | Trusted base layer only. Same-id conflicts between plugin layers are load errors; global user definitions replace plugin definitions. |
| `tasks/` | Trusted layer plus trusted ancestor overlay. Same-id conflicts between plugin layers are load errors; user-owned layers replace whole definitions. |
| `workflows/` | Trusted layer plus ancestor overlay. Same-id plugin-layer conflicts are load errors; user-owned layers can add nodes and channels, with singleton fields guarded against accidental redeclaration. |
| `templates/` | Read-only plugin base layer plus current user-owned template layers. Same-id conflicts between plugin layers are load errors. |

The template loader gains one generic read-only plugin layer. The current
loader searches only the workdir ancestor overlays and
`~/.config/plect/templates/`; plugin template support extends that lookup with
mounted plugin `templates/` directories below user-owned template directories.
Install-time materialization is rejected because it copies plugin content into
user config, and those copies drift away from the plugin revision recorded in
the lockfile.

Executable adapters are invoked from TOML hooks through normal command lines.
Core does not gain a plugin-specific process protocol in this design. A plugin
may ship a provider/resource/task config that calls a binary in `bin/`; the
loader resolves those executable paths and injects a small hook-template helper
that expands to stable absolute paths.

Hook syntax before plugin executable references relies on the command being on
`PATH`:

```toml
scope = "run"
setup = 'agent-runtime launch --workdir {{.Session.WorkdirPath | shellQuote}}'
cleanup = 'agent-runtime stop --id {{.Self.runtime_id | shellQuote}}'
```

The same hook against a plugin-shipped executable names the catalog-qualified
plugin reference at the command position:

```toml
scope = "run"
setup = '{{bin "official/agent/runtime/agent-runtime"}} launch --workdir {{.Session.WorkdirPath | shellQuote}}'
cleanup = '{{bin "official/agent/runtime/agent-runtime"}} stop --id {{.Self.runtime_id | shellQuote}}'
```

`{{bin "<catalog-alias>/<plugin-path>/<executable>"}}` is the full form. To
parse it, plect compares the reference against enabled plugin identities in the
same catalog and selects the longest matching plugin path; the remaining
segment is the executable name. `{{bin "<catalog-alias>/<plugin-path>"}}` is
shorthand only when the whole reference exactly matches one enabled plugin and
that plugin declares exactly one executable. A reference is a load error if the
same string can also be read as a shorter plugin plus executable, because no
slash-based syntax can disambiguate that collision under arbitrary-depth plugin
paths. A shorter reference that omits the catalog alias is a load error because
plugin paths are unique only inside a catalog. An executable segment that is
ambiguous inside the selected plugin is also a load error. For locked plugins,
the helper returns an absolute path inside the read-only mounted plugin cache.
For editable path catalogs, it returns an absolute path inside the directly
mounted development tree. It never writes generated paths back into user
config.

The same template-variable reference and read-only-mount rules apply to scripts
and built binaries for locked plugins; editable path catalogs keep the same
executable lookup but use the development-mode mount exception described in the
resolution flow. Scripts are executed through their shebangs; hooks still
reference them through `{{bin ...}}`.

Alternatives considered:

- Environment-variable injection would require command strings like
  `$PLECT_PLUGIN_AGENT_RUNTIME_BIN launch ...`, which is longer, less readable,
  and introduces shell-specific quoting and naming rules into every hook.
- A symlink farm would make the TOML look like ordinary commands, but it creates
  another mutable filesystem surface to manage and debug.
- Adding plugin `bin/` directories to `PATH` preserves the old syntax, but
  same-name executables across plugin layers would become declaration-order
  behavior unless another conflict system wrapped `PATH`.

### Executable Build Model

Executable delivery is staged:

1. Stage 1, v1: build-less scripts are used directly from trusted plugin
   source, and compiled executables are built on the user's machine at
   add/update time. When `build` is absent, `path` must point at a script or
   executable file already present in the source tree. When an executable entry
   declares `build`, plect runs that command inside the resolved plugin
   directory and places the resulting executable in the plugin's cache bin
   location.
2. Stage 2, future direction: plect release artifacts may bundle
   Plecture-maintained plugin binaries as separate plugin packages that ride the
   release. They are not embedded in core, and activation still requires the
   user's declaration. Bundling is only a bootstrap seed: out-of-band
   git-reference updates must remain possible and take precedence when declared,
   because agent CLI churn must not wait for plect releases.
3. Stage 3, future signing story: per-plugin signed prebuilt release assets
   return with the archive source scheme. This removes the local toolchain
   requirement for third-party plugins at scale.

A Go toolchain is an explicit v1 requirement for plugins with Go build commands,
consistent with plect itself being installable through `go install` and with
Go-tooling norms such as user-built language servers and static-analysis tools.
The lock pins the plugin content hash, so a locally built binary is a
derivative of exactly the trusted content; the Git commit SHA is provenance for
where that content was acquired. That is a stronger verification chain than
unsigned prebuilt distribution. Running a build command is within the trust
already granted by confirming the catalog registration, because plugin config
can run shell during normal operation.
Plecture-maintained plugins also remain available through the existing Nix path.

## Reference resolution

Users register catalogs and enable selected plugins in trusted user-owned
config, not inside cloned workdir content. The global declaration file lives at:

```text
~/.config/plect/catalogs.toml
```

Example:

```toml
schema_version = 1

[[catalogs]]
alias = "official"
source = "git+https://github.com/example/plect-plugins"
plugins = ["github", "agent/codex-tasks"]

[[catalogs]]
alias = "team"
source = "git+ssh://git@example.com/team/plect-catalog"
plugins = ["agent/runtime"]

[[catalogs]]
alias = "local"
source = "path:///home/user/src/plect-catalog"
plugins = ["okf"]
```

Declaration file fields:

| Field | Required | Meaning |
|---|---:|---|
| `schema_version` | yes | Declaration file-format version. Unknown values fail loud. |

Catalog entry fields:

| Field | Required | Meaning |
|---|---:|---|
| `alias` | yes | User-chosen local catalog alias. It is the trust name and the first segment of plugin identities. |
| `source` | yes | Exact catalog source, not a prefix. Supported v1 schemes are `git+https`, `git+ssh`, `path`, and `path+editable`. The source value never carries a revision. |
| `plugins` | yes | Catalog-relative plugin paths enabled from this catalog. Each path forms a catalog-qualified plugin identity with the alias, `<catalog-alias>/<relative-path>`. |

A `catalogs` entry is the trust act. Each registration binds a user-chosen alias
to one exact source and the plugin paths enabled from that source. It does not
pin selected plugin content; per-plugin lock entries carry the resolved
acquisition coordinates and content hashes. Default alias suggestions belong in
provider README install snippets, not in any manifest. Core contains no official
host, owner, registry, provider list, or prefix policy.

Git is the default transport for reference distribution, not a requirement of
the resolution model. The model needs only fetch, revision or hash
verification, and read-only mount; the `path` scheme is the git-free floor. Core
invokes git as an external command and does not take a git library dependency.
Environments without git lose only the git schemes, with a clear error.

Resolution flow:

1. Read catalog registrations and nested plugin selections from global user
   config.
   The first implementation does not read them from ancestor overlays.
2. For each referenced catalog, resolve the registration to concrete content:
   - Git catalog: fetch the source and resolve the requested revision supplied by
     the add or update command to a commit SHA. `--revision` accepts tags and
     branches as input, but the lockfile records only the resolved commit SHA,
     never the symbolic ref. The source in `catalogs.toml` remains revision-free.
   - Locked path catalog: resolve symlinks and verify selected plugin
     directories against their per-plugin tree SHA-256 hashes. Path sources have
     no catalog revision.
   - Editable path catalog: resolve symlinks and mount the path directly. It
     records neither a catalog revision nor a content hash and is therefore
     non-reproducible.
3. Find `catalog.toml` at the resolved catalog root and fail if
   `schema_version` is unknown.
4. Validate every listed plugin path with realpath containment inside the
   catalog root, and fail on unlisted `plugin.toml` files inside the catalog.
5. For each enabled plugin id, split the catalog alias from the relative path
   and fail if the alias is not registered or the path is not listed by that
   catalog.
6. Verify the plugin's lock entry against the catalog acquisition coordinate
   and plugin content hash, except editable path catalogs. For Git catalogs, the
   acquisition coordinate is the resolved commit SHA and the content hash is
   computed over the catalog trust-space subtree actually mounted for the
   plugin. The SHA identifies the repository snapshot; the content hash verifies
   the trusted bytes. Recording both also avoids relying on Git SHA-1 collision
   resistance alone. Path catalogs hash each selected plugin directory, not the
   whole catalog subtree, so changing one local plugin does not force unrelated
   sibling plugins to move.
7. Read the plugin's `plugin.toml` and fail if `schema_version` is unknown.
8. Check `plect_min_version` against the running plect version.
9. Mount the plugin directory as a read-only base config layer, except editable
   path catalogs, which are an explicit local development escape hatch.

Core should materialize resolved catalog snapshots under a cache owned by the
user, for example:

```text
~/.cache/plect/catalogs/<source-digest>/<lock-coordinate>/
```

The source digest is derived from the exact registered source, not from the
user-local alias. Reusing an alias for a different catalog therefore cannot
reuse the old catalog's cache namespace. The lock coordinate is the resolved
commit SHA for Git catalogs and the plugin content hash for locked path
catalogs. Editable path catalogs are mounted directly and are not cached.

The existing `plugin_dirs` setting becomes an implementation detail for mounted
resolved directories. During migration, users can move from hand-authored
`plugin_dirs` to catalog registrations, plugin selections, and `plect.lock`.
Because Plecture is pre-1.0, the migration is one-time and documented rather
than a compatibility shim.

Reference declarations are global-only in the first implementation. Ancestor
overlays can still customize tasks and workflows under the existing loader
rules, but they cannot register catalogs or select new plugins. The workdir's
own `.plect/` directory is also excluded because cloned content must not fetch
or run code.

Alternatives considered: `archive+https` is rejected from v1. Current adopters
are a single user plus a planned team, and both source plugins from Git. The
`path` scheme remains the git-free floor for local development. Archive
distribution returns with the signed prebuilt-asset story, not as an unsigned v1
transport.

## Lockfile

The lockfile records exactly what was mounted. The default path is:

```text
~/.config/plect/plect.lock
```

Because catalog registrations and nested plugin selections are global-only in the
first implementation, this lockfile is global-only too. A workdir-owned
lockfile is ignored for plugin resolution.

Example:

```toml
schema_version = 1

[[plugins]]
id = "official/github"
catalog_alias = "official"
catalog_source = "git+https://github.com/example/plect-plugins"
catalog_resolved_revision = "4f2db5e4c2b4b4a8c6c0f6c0d4d2d2ecf0c1a0b3"
path = "github"
content_hash = "sha256:..."
version = "0.3.0"
plect_min_version = "0.8.0"
editable = false

[[plugins]]
id = "official/agent/codex-tasks"
catalog_alias = "official"
catalog_source = "git+https://github.com/example/plect-plugins"
catalog_resolved_revision = "4f2db5e4c2b4b4a8c6c0f6c0d4d2d2ecf0c1a0b3"
path = "agent/codex-tasks"
content_hash = "sha256:..."
plect_min_version = "0.8.0"
editable = false

[[plugins]]
id = "local/okf"
catalog_alias = "local"
catalog_source = "path:///home/user/src/plect-catalog"
path = "okf"
content_hash = "sha256:..."
plect_min_version = "0.8.0"
editable = false
```

Recorded fields:

| Field | Meaning |
|---|---|
| `id` | Catalog-qualified plugin identity, `<catalog-alias>/<relative-path>`. |
| `catalog_alias` | User-chosen catalog alias from the registration. |
| `catalog_source` | Catalog source from `catalogs.toml`. It never includes a symbolic Git revision. |
| `catalog_resolved_revision` | Git-only immutable catalog revision. It is always the resolved commit SHA. |
| `path` | Plugin path relative to `catalog.toml`. |
| `content_hash` | Digest of the mounted plugin directory after checkout or path resolution. For Git catalogs, this verifies the trust-space subtree content independently of the commit SHA. For locked path catalogs, this tree SHA-256 is the only pin. Editable path catalogs omit it. |
| `version` | Optional plugin package version read from metadata when present. |
| `plect_min_version` | Compatibility floor read from metadata. |
| `editable` | Whether the catalog was mounted in explicit local development mode. |

Add and update commands:

- `plect catalog add <alias> <source> [--revision <rev>]` resolves the catalog,
  normalizes the source into an exact registered source, reads `catalog.toml`,
  shows the exact source, resolved lock coordinate, manifest description, and
  plugin paths, and asks for interactive confirmation. On consent, it writes a
  new `[[catalogs]]` entry with `alias`, revision-free `source`, and an empty
  `plugins` list. When `--revision` is supplied for a Git source, it affects the
  trusted snapshot used by subsequent plugin add commands; `catalogs.toml` still
  records no revision, and plugin lock entries record only the resolved commit
  SHA.
- `plect catalog update <alias> [--revision <rev>]` fetches the newest matching
  catalog snapshot and repoints every enabled plugin from that catalog to the
  new snapshot with fresh per-plugin lock entries. When `--revision` is
  supplied for a Git catalog, it is a catalog-level intent change: after
  confirmation, plect resolves the requested revision to a commit SHA and
  records that SHA in each updated plugin lock entry. It does not write the
  requested revision to `catalogs.toml`.
- `plect catalog remove <alias>` shows the enabled plugins that would be
  disabled, requires confirmation in interactive contexts, then removes the
  catalog registration and those plugin selections and lock entries.
- `plect catalog list` shows registered aliases, sources, catalog validation
  state, and a summary of enabled plugin lock coordinates grouped by catalog. It
  does not imply that one resolved Git commit applies to every enabled plugin in
  the catalog.
- `plect plugin add <alias>/<path>` enables one plugin path from a registered
  catalog, resolves the registered source or most recently trusted catalog
  snapshot to a commit SHA for Git catalogs, adds the path to that catalog
  entry's `plugins` list, and writes or updates that plugin's lock entry.
- `plect plugin update <alias>/<path> [--revision <rev>]` fetches the newest
  matching catalog snapshot, validates the catalog, and repoints only that
  plugin's lock entry. When `--revision` is supplied, it affects only that
  plugin's lock entry and does not rewrite the catalog registration. Tags and
  branches are accepted as input for Git catalogs, but the lock records the
  resolved commit SHA. Other plugins from the same catalog keep their previous
  locked coordinates; cache snapshots coexist by source digest and lock
  coordinate.
- `plect plugin remove <alias>/<path>` disables that plugin and removes its lock
  entry.
- `plect plugin verify` re-hashes cached plugin directories and fails if any
  differ from the lockfile.
- `plect plugin list` shows selected, resolved, locked, and compatibility state.

Non-interactive contexts fail instead of prompting for first-seen catalog
registrations. Any override flag must be explicit and visible in command
history.

No command silently advances a plugin. A missing or mismatched lock entry is a
load error unless the user is running an explicit add or update command.
Editable path catalogs are the only exception: they are marked non-reproducible
in `plect plugin list`, excluded from `plect plugin verify --locked`, and meant
only for local plugin development.

`plect.lock` carries mechanical pinning only: catalog source, Git commit SHA
when applicable, plugin path, plugin content hash, metadata, and editable state.
Trust policy stays in catalog registrations; the lockfile never records trust
semantics or symbolic Git refs.

Alternatives considered:

- Metadata-supplied plugin identities were rejected because collisions move up
  to a flat namespace and local renaming becomes a special case. Filesystem
  paths already provide intra-catalog uniqueness.
- A central registry or marketplace layer was rejected for v1 because it needs
  central infrastructure and a signing story. It can be revisited with signed
  catalogs or packages.
- Catalog-level usage pinning was rejected because it forces a single revision
  across all enabled plugins from one catalog. That reproduces known friction
  from nix-flake-style updates when the user wants to update one package while
  leaving siblings untouched.
- The accepted UX follows Helm and Homebrew: users choose local aliases and
  update individual packages. The trust boundary follows Go modules: the
  manifest position defines the subtree. Reproducibility comes from lock-based
  pinning, which Helm-style indexes do not provide by themselves.

## Trust boundary

Plecture config can run shell on the user's machine. Plugin config has the same
power because provider setup, resource observe/finalize, task setup/cleanup,
environment exec, and exec channels all execute commands.

Threat model:

- The config directory, including catalog registrations, is the root of trust.
  It is in the same trust boundary as the `plect` binary because Plecture config
  is effectively executable shell already.
- Catalog registrations are policy and `plect.lock` is a mechanical record.
  Keeping them separate preserves their ownership split: humans edit policy;
  tooling writes the lock.
- A catalog registration edit alone changes nothing mounted. Content
  immutability still comes from `plect.lock`: Git catalogs record the resolved
  commit SHA plus the trust-space content hash, locked path catalogs record the
  tree SHA-256, and editable path catalogs are explicitly non-reproducible.
- Plugin declarations are global-only. A dispatched session's workdir cannot
  inject plugins or trust new catalogs through cloned content.
- Catalog registration and lock edits are human actions. Agent-driven workflows
  must not edit them automatically; they should fail or escalate instead.
- First-seen catalogs require interactive confirmation through
  `plect catalog add`. Non-interactive contexts fail unless the user supplied an
  explicit override that remains visible in command history.
- Remote attacks are constrained by transport authentication plus lock
  verification: trust is established at catalog registration time, then the
  resolved Git commit SHA and trust-space content hash are verified on every
  load.
- Users can point at any catalog they choose to trust. Third-party installation
  is possible by design, and responsibility for trusting a catalog lies with the
  human who confirmed it. Deferred signing is what makes third-party consumption
  safe at scale, not what makes it possible.
- No auto-update exists. Plugin directories mounted by core are read-only during
  normal command execution.
- Workdir-owned cloned content cannot declare plugin references, providers,
  resources, environments, channels, or task definitions.

The current loader already enforces most of this shape:

- Providers, resources, environments, and channels load only from plugin dirs
  and global config.
- Tasks reject definitions inside the workdir layer.
- Workflows allow workdir files only to add nodes.
- Templates can be overridden by workdir content, but templates are rendered as
  text; the execution risk comes from tasks or human action that consume them.

## Shadowing and precedence

For loaders that receive plugin dirs, resolution order is:

1. Plugin layers, in declaration order.
2. Global user config.
3. Trusted ancestor overlays, outermost to innermost.
4. Workdir `.plect/` overlay, with the current restrictions.

The user layer always wins because every user-owned layer is deeper than the
plugin layers. Declaration order never chooses between same-id definitions from
two plugin layers: that conflict is a load error, and the user must resolve it
explicitly in global config by choosing one plugin or disabling one plugin.

Same-id behavior by kind:

| Kind | Same-id rule |
|---|---|
| Providers, resources, environments, channels | Same-id conflicts between plugin layers fail. A deeper user-owned layer replaces the whole definition. No partial override. |
| Tasks | Same-id conflicts between plugin layers fail. A deeper user-owned layer replaces the whole definition. No partial override. |
| Workflows | Same-id conflicts between plugin workflow node ids, event channel names, or singleton fields fail. User-owned layers merge by adding nodes and event channels. Singleton fields cannot be redeclared, except runtime tuning tables where deeper trusted layers replace the whole table. |
| Workflow input schemas | Plugin-layer schemas for the same workflow id conflict unless they belong to the same selected plugin workflow. User-owned layer schemas combine with `allOf`. |
| Templates | Same-id conflicts between plugin layers fail. Lookup then remains user-overridable: nearest workdir or ancestor template, then global user templates, then plugin templates. |

Partial override model:

- To partially customize a plugin workflow, add a same-named workflow file in a
  trusted overlay that adds new `[[nodes]]` or new `[[event.channel]]` entries.
- To replace a plugin task, provider, resource, environment, or channel, place a
  full same-id definition in global config or a trusted overlay.
- To customize a template, place a same-named Markdown template in the nearest
  desired overlay.
- A plugin-provided task cannot be edited in place through a patch mechanism.
  Whole-definition replacement keeps arbitrary shell behavior auditable.

This model keeps the existing loader semantics and makes plugin distribution a
new source of base layers rather than a new merge language.

Alternatives considered: letting declaration order decide same-id plugin
conflicts would make behavior depend on list order in `catalogs.toml`. That is
too easy to miss during review, especially for executable shell hooks, so plugin
layer conflicts fail loud.

## Compatibility

A plugin with an unsatisfied `plect_min_version` must fail loud before any of
its config is mounted. The error should name the plugin id, required version,
and running version.

Compatibility checks happen in this order:

1. Catalog resolution and catalog manifest validation.
2. Plugin path validation and lockfile verification.
3. Plugin metadata parse.
4. `plect_min_version` check.
5. Config load and existing schema/contract validation.

There is no silent degradation. A plugin cannot say "try this weaker behavior
on old core." If a breaking change is needed while Plecture is pre-1.0, it ships
with a one-time migration or documented procedure that includes a backup step.

Compatibility metadata decisions:

- `plect_min_version` is the only generic compatibility field before 1.0. A
  maximum tested version is rejected because an advisory field with no current
  consumer is speculative. Revisit only if real incompatibility reports
  demonstrate the need.
- Executable adapter protocol versions remain adapter-specific documentation
  unless core later defines a generic adapter protocol.

## Migration sketch

Today's shipped binaries and hand-authored global TOML map onto this model
without changing provider, resource, task, workflow, or channel contracts.

Current shape:

- Binaries live under `plugins/`.
- Some shipped provider TOML already lives with a plugin.
- Production-like usage still requires hand-authored global TOML for providers,
  resources, tasks, workflows, channels, and templates.
- `~/.config/plect/config.toml` lists `plugin_dirs`.

Target shape:

- Each distributable catalog carries `catalog.toml`.
- Each distributable plugin directory listed by the catalog carries
  `plugin.toml`.
- Shipped config stays in the existing subdirectories.
- Users register catalogs and enable plugins by catalog-qualified path identity.
- `plect.lock` records the exact mounted plugin content.
- Core resolves those references to read-only directories and feeds them into
  the existing loader as base plugin layers.
- Residual user config contains only local choices: allowlists, selected
  channels, workflow inputs, team overlays, and template customizations.

One-time migration procedure:

1. Back up `~/.config/plect/config.toml` and sibling config directories.
2. Group existing global files by ownership: provider/resource/channel/task pack,
   workflow pack, and team-owned template or overlay.
3. Move reusable groups into plugin directories with `plugin.toml`.
4. Create a `catalog.toml` next to those plugin directories and list every
   plugin path.
5. Replace `plugin_dirs` with catalog registrations and plugin selections.
6. Run `plect catalog add <alias> <source> [--revision <rev>]` for each
   catalog.
7. Run `plect plugin add <alias>/<path>` for each selected plugin to write
   `plect.lock`.
8. Run the normal per-module tests for any plugin source that contains code.
9. Keep only residual local choices in global config.

## Walkthrough examples

### Agent task pack

A Claude or Codex task pack is a plugin because it commits to a particular
agent CLI and runtime behavior.

Catalog registration and plugin enablement:

```bash
plect catalog add official git+https://github.com/example/plect-plugins --revision v0.3.0
plect plugin add official/agent/codex-tasks
plect plugin update official/agent/codex-tasks
```

`plect plugin update official/agent/codex-tasks` fetches the newest catalog
snapshot and repoints only `official/agent/codex-tasks`; `official/github` or
`official/agent/claude-tasks` stay at their locked coordinates until explicitly
updated.

Catalog-owned files:

```text
catalog.toml
agent/codex-tasks/plugin.toml
agent/codex-tasks/tasks/agent.toml
agent/codex-tasks/tasks/runtime.toml
agent/codex-tasks/channels/agent_socket.toml
agent/codex-tasks/workflows/coding.toml
agent/codex-tasks/bin/plect-agent-activity
```

Plugin-owned templates:

```text
agent/codex-tasks/templates/work.md
agent/codex-tasks/templates/review.md
```

Residual user config:

- Which agent pack is selected.
- Local command path or model defaults if they differ from plugin defaults.
- Event channel bindings for the user's runtime session.
- Team-specific workflow overlays that add local notification or review nodes.
- Prompt templates that encode team operating style.

No provider, task, workflow, or channel contract change is required. The
workflow still references tasks and channels by their existing ids. Shipping
templates from the plugin requires the generic read-only template-loader layer
described above; it does not require task or workflow contract changes.
`bin/plect-agent-activity` is a build-less shell script with its interpreter in
the shebang, referenced from hooks through the same `{{bin ...}}` helper as a
built binary.

### GitHub provider

The GitHub provider remains a plugin because URL parsing, branch lookup, and
workdir acquisition are GitHub-specific.

Catalog registration and plugin enablement:

```bash
plect catalog add official git+https://github.com/example/plect-plugins --revision v0.3.0
plect plugin add official/github
plect plugin update official/github
```

`plect plugin update official/github` fetches the newest catalog snapshot and
repoints only `official/github`; `official/agent/codex-tasks` stays at its
locked coordinate.

Catalog-owned files:

```text
catalog.toml
github/plugin.toml
github/providers/github.toml
github/resources/github.toml
github/tasks/github_work.toml
github/tasks/github_review.toml
github/workflows/github_coding.toml
github/bin/plect-github-provider
github/bin/plect-github-watcher
```

Residual user config:

- Resource allowlist entries for allowed owners or repositories.
- Authentication outside Plecture, such as the GitHub CLI token.
- Workdir root choice.
- Whether the user composes the GitHub workflow with Claude, Codex, or another
  agent plugin.
- Project-board or watcher subscriptions that are local operating policy.

Core still sees only provider, resource, task, workflow, and channel contracts.
It does not parse GitHub URLs or know GitHub exists.

### Hypothetical okf plugin

An okf plugin is scoped by the OKF specification, not by one use case. Its
first version owns only the goal resource mechanics that have machine
semantics: observation, finalization, and ticks. Bundle records that do not have
machine semantics, such as retrospectives, stay plain files outside the plugin.

Catalog registration and plugin enablement:

```bash
plect catalog add local path:///home/user/src/plect-catalog
plect plugin add local/okf
plect plugin update local/okf
```

`plect plugin update local/okf` refreshes the catalog snapshot used by
`local/okf` only. Other enabled plugins from `local` remain pinned to their
current lock entries.

Catalog-owned files:

```text
catalog.toml
okf/plugin.toml
okf/resources/okf_goal.toml
okf/bin/plect-okf
```

Plugin-owned behavior:

- Goal resource id syntax.
- Goal observation, finalization, and tick entrypoints.
- Revision and checklist status reporting for goal resources.
- Idempotent completion logging for goal resources.

Internally separable plugin behavior:

- Owner alias resolution.
- Bundle root discovery.
- Bundle containment checks.
- Frontmatter parsing shared by future OKF concepts.

Not plugin-owned in the first version:

- Goal bootstrap, pursue, or review tasks.
- Goal review workflows.
- Goal review templates.
- Retrospectives or other bundle records without machine semantics.

Residual user config:

- Which goal roots or owners are allowed.
- Which orchestrator workflow is used.
- Which agent and channel plugins handle the work.
- Goal bootstrap, pursue, and review task composition.
- Team-owned operating procedure templates.
- Any local overlay that maps goal review into the team's workflow shape.

No provider/resource/task/workflow contract changes are needed. If the plugin
discovers that goal state needs data the existing resource observation contract
cannot express, that belongs in a later contract issue.
