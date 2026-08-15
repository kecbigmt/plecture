# Plugin packaging, reference resolution, lockfile, and trust model

This document proposes the plugin model for distributing reusable Plecture
configuration and executable adapters. The goal is to reduce bootstrap cost
without moving provider, tool, or domain knowledge into core.

The proposal keeps one concept and one word: plugin. A plugin is a
distributable package that may contain executable adapters, config resources,
templates, and metadata. A plugin with only configuration is still a plugin.

## Goals and constraints

- Core owns generic mechanics: fetch, verify, mount, resolve, and fail-loud
  compatibility checks.
- Plugins own technology or domain commitments: provider adapters, resource
  observation, channels, agent/runtime tasks, workflow packs, and templates.
- User-owned config remains the final authority.
- No provider/resource/task/workflow contract changes are required by this
  design. Any discovered gap must become an open question or a later contract
  issue, not an assumption in the package format.
- No auto-update. Every install and update is pinned and explicit.

## Package format

A plugin is a directory with a metadata file at the root and zero or more
standard subdirectories that are already understood by the config loaders:

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

`plugin.toml` is the only required file:

```toml
name = "github"
version = "0.3.0"
plect_min_version = "0.8.0"
description = "GitHub resource provider and workflow support."

[[executables]]
name = "plect-github-provider"
path = "bin/plect-github-provider"
```

Metadata fields:

| Field | Required | Meaning |
|---|---:|---|
| `name` | yes | Stable plugin identity. It is used in lockfile entries and diagnostics. |
| `version` | yes | Plugin package version. It is informational for Git pins but required so local and archive sources can be compared. |
| `plect_min_version` | yes | Minimum plect version required to load the plugin. |
| `description` | no | Human-readable summary for list/show commands. |
| `executables` | no | Relative paths for binaries or scripts shipped by the plugin. |

The standard subdirectories with current plugin-layer loader behavior are:

| Directory | Mount behavior |
|---|---|
| `providers/` | Trusted base layer only. Same-id deeper layer replaces shallower layer. |
| `resources/` | Trusted base layer only. Same-id deeper layer replaces shallower layer. |
| `environments/` | Trusted base layer only. Same-id deeper layer replaces shallower layer. |
| `channels/` | Trusted base layer only. Same-id deeper layer replaces shallower layer. |
| `tasks/` | Trusted layer plus trusted ancestor overlay. Same-id deeper layer replaces shallower layer. |
| `workflows/` | Trusted layer plus ancestor overlay. Same-id files merge by adding nodes and channels, with singleton fields guarded against accidental redeclaration. |

`templates/` is proposed package content, but it has no plugin layer today. The
current template loader searches only the workdir ancestor overlays and
`~/.config/plect/templates/`. Shipping templates from plugins therefore
requires one implementation decision: add plugin directories to the template
loader as a base layer, or materialize plugin templates into user-owned config
during install.

Executable adapters are invoked from TOML hooks through normal command lines.
Core does not gain a plugin-specific process protocol in this design. A plugin
may ship a provider/resource/task config that calls a binary in `bin/`; install
mechanics place that binary on the hook execution path or expose a stable
absolute path through a generated mount variable.

## Reference resolution

Users declare selected plugins in trusted user-owned config, not inside cloned
workdir content. A global declaration file is proposed at:

```text
~/.config/plect/plugins.toml
```

Example:

```toml
[[trusted_sources]]
class = "official"
source_prefix = "git+https://github.com/example/"

[[plugins]]
name = "github"
source = "git+https://github.com/example/plect-github-plugin"
revision = "4f2db5e4c2b4b4a8c6c0f6c0d4d2d2ecf0c1a0b3"
trust = "official"

[[plugins]]
name = "agent-runtime"
source = "git+ssh://git@example.com/team/agent-runtime-plugin"
revision = "v0.6.1"
trust = "third_party"

[[plugins]]
name = "okf"
source = "path:///home/user/src/okf-plugin"
sha256 = "2b7c..."
trust = "local"
```

Declaration fields:

| Field | Required | Meaning |
|---|---:|---|
| `name` | yes | Expected plugin name. It must match `plugin.toml`; a mismatch is a load error. |
| `source` | yes | Source locator. Supported schemes are `git+https`, `git+ssh`, `archive+https`, and `path`. |
| `revision` | scheme-dependent | Git revision to resolve. Required for Git sources. |
| `sha256` | scheme-dependent | Content hash. Required for archive sources and locked path sources. |
| `trust` | yes | User-declared trust class: `official`, `third_party`, or `local`. |
| `editable` | no | Path-only development mode. When true, the path is mounted directly and is not reproducible. |

`trusted_sources` is user-owned policy. Core does not contain an official host,
owner, registry, or provider list. For `trust = "official"`, core only checks
that the source string matches a user-declared `trusted_sources` rule whose
class is `official`. Third-party and local sources still require explicit trust
on the plugin declaration.

Resolution flow:

1. Read plugin declarations from global user config. The first implementation
   does not read plugin declarations from ancestor overlays.
2. Resolve each declaration to immutable content:
   - Git source: fetch the source and resolve `revision` to a commit hash.
   - Archive source: download only when `sha256` is declared.
   - Locked path source: resolve symlinks and verify the tree against `sha256`.
   - Editable path source: resolve symlinks and mount the path directly.
3. Read `plugin.toml` from the resolved tree.
4. Fail if `plugin.toml`'s `name` differs from the declaration's `name`.
5. Check `plect_min_version` against the running plect version.
6. Verify the resolved content against the lockfile, except editable path
   sources.
7. Mount the plugin directory as a read-only base config layer, except editable
   path sources, which are an explicit local development escape hatch.

Core should materialize resolved plugins under a cache owned by the user, for
example:

```text
~/.cache/plect/plugins/<plugin-name>/<content-hash>/
```

The existing `plugin_dirs` setting becomes an implementation detail for mounted
resolved directories. During migration, users can move from hand-authored
`plugin_dirs` to `plugins.toml` plus `plect.lock`. Because Plecture is pre-1.0,
the migration is one-time and documented rather than a compatibility shim.

Reference declarations are global-only in the first implementation. Ancestor
overlays can still customize tasks and workflows under the existing loader
rules, but they cannot select new plugin sources. The workdir's own `.plect/`
directory is also excluded because cloned content must not fetch or run code.

## Lockfile

The lockfile records exactly what was mounted. The proposed default path is:

```text
~/.config/plect/plect.lock
```

Because plugin declarations are global-only in the first implementation, this
lockfile is global-only too. A workdir-owned lockfile is ignored for plugin
resolution.

Example:

```toml
version = 1

[[plugins]]
name = "github"
source = "git+https://github.com/example/plect-github-plugin"
requested_revision = "v0.3.0"
resolved_revision = "4f2db5e4c2b4b4a8c6c0f6c0d4d2d2ecf0c1a0b3"
content_hash = "sha256:..."
version = "0.3.0"
plect_min_version = "0.8.0"
official = true
```

Recorded fields:

| Field | Meaning |
|---|---|
| `name` | Plugin identity from `plugin.toml`. |
| `source` | Original source locator. |
| `requested_revision` | User declaration, such as a tag or commit. |
| `resolved_revision` | Immutable source revision, such as a Git commit. |
| `content_hash` | Digest of the mounted tree after checkout or extraction. |
| `version` | Plugin package version read from metadata. |
| `plect_min_version` | Compatibility floor read from metadata. |
| `official` | Whether user-owned `trusted_sources` classified the source as official at resolution time. |
| `editable` | Whether the source was mounted in explicit local development mode. |

Install and update commands:

- `plect plugin install <source> --revision <rev>` resolves content, writes or
  updates `plugins.toml`, and writes `plect.lock`.
- `plect plugin update <name> --revision <rev>` performs an explicit revision
  bump and lockfile rewrite.
- `plect plugin verify` re-hashes cached content and fails if it differs from
  the lockfile.
- `plect plugin list` shows declared, resolved, locked, and compatibility state.

No command silently advances a plugin. A missing or mismatched lock entry is a
load error unless the user is running an explicit install or update command.
Editable path sources are the only exception: they are marked non-reproducible
in `plect plugin list`, excluded from `plect plugin verify --locked`, and meant
only for local plugin development.

## Trust boundary

Plecture config can run shell on the user's machine. Plugin config has the same
power because provider setup, resource observe/finalize, task setup/cleanup,
environment exec, and exec channels all execute commands.

Trust rules:

- Every non-local plugin source must be pinned by immutable revision or content
  hash before it can load.
- Official sources are still pinned. "Official" is determined only by
  user-owned `trusted_sources` policy; core must not hardcode any official
  host, owner, provider, or registry. The label changes review expectations,
  not execution safety.
- Third-party sources require an explicit `trust = "third_party"` declaration
  before install.
- No auto-update exists for official or third-party plugins.
- Plugin directories mounted by core are read-only during normal command
  execution.
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

For loaders that already receive plugin dirs, resolution order is:

1. Plugin layers, in declaration order.
2. Global user config.
3. Trusted ancestor overlays, outermost to innermost.
4. Workdir `.plect/` overlay, with the current restrictions.

The user layer always wins because every user-owned layer is deeper than the
plugin layers.

Same-id behavior by kind:

| Kind | Same-id rule |
|---|---|
| Providers, resources, environments, channels | Deeper layer replaces the whole definition. No partial override. |
| Tasks | Deeper layer replaces the whole definition. No partial override. |
| Workflows | Layers merge. New nodes and event channels append. Existing node ids and event channel names cannot be redeclared. Singleton fields cannot be redeclared, except runtime tuning tables where deeper trusted layers replace the whole table. |
| Workflow input schemas | Multiple layer schemas combine with `allOf`. |
| Templates | Current behavior has no plugin layer: first match wins across nearest workdir or ancestor template, then global user templates. A plugin template layer or install-time materialization is proposed but not yet implemented. |

Partial override model:

- To partially customize a plugin workflow, add a same-named workflow file in a
  trusted overlay that adds new `[[nodes]]` or new `[[event.channel]]` entries.
- To replace a plugin task, provider, resource, environment, or channel, place a
  full same-id definition in global config or a trusted overlay.
- To customize a template, place a same-named Markdown template in the nearest
  desired overlay. Until template plugin support exists, plugin-provided
  templates must be materialized into user-owned config before this override
  model can apply to them.
- A plugin-provided task cannot be edited in place through a patch mechanism.
  Whole-definition replacement keeps arbitrary shell behavior auditable.

This model keeps the existing loader semantics and makes plugin distribution a
new source of base layers rather than a new merge language.

## Compatibility

A plugin with an unsatisfied `plect_min_version` must fail loud before any of
its config is mounted. The error should name the plugin, required version, and
running version.

Compatibility checks happen in this order:

1. Source resolution and lockfile verification.
2. Metadata parse.
3. `plect_min_version` check.
4. Config load and existing schema/contract validation.

There is no silent degradation. A plugin cannot say "try this weaker behavior
on old core." If a breaking change is needed while Plecture is pre-1.0, it ships
with a one-time migration or documented procedure that includes a backup step.

Open compatibility questions:

- Should plugins also declare a maximum tested plect version as advisory
  metadata, or is the minimum floor enough before 1.0?
- Should executable adapters declare their own protocol version separately from
  plugin package version, or should that remain adapter-specific documentation?

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

- Each distributable plugin carries its own `plugin.toml`.
- Shipped config stays in the existing subdirectories.
- Users declare references in `plugins.toml`.
- `plect.lock` records the exact mounted content.
- Core resolves those references to read-only directories and feeds them into
  the existing loader as base plugin layers.
- Residual user config contains only local choices: allowlists, selected
  channels, workflow inputs, team overlays, and template customizations.

One-time migration procedure:

1. Back up `~/.config/plect/config.toml` and sibling config directories.
2. Group existing global files by ownership: provider/resource/channel/task pack,
   workflow pack, and team-owned template or overlay.
3. Move reusable groups into plugin directories with `plugin.toml`.
4. Replace `plugin_dirs` with pinned `plugins.toml` declarations.
5. Run `plect plugin install --locked` to write `plect.lock`.
6. Run the normal per-module tests for any plugin source that contains code.
7. Keep only residual local choices in global config.

## Walkthrough examples

### Agent task pack

A Claude or Codex task pack is a plugin because it commits to a particular
agent CLI and runtime behavior.

Plugin-owned files:

```text
plugin.toml
tasks/agent.toml
tasks/runtime.toml
channels/agent_socket.toml
workflows/coding.toml
```

Proposed plugin-owned templates:

```text
templates/work.md
templates/review.md
```

Residual user config:

- Which agent pack is selected.
- Local command path or model defaults if they differ from plugin defaults.
- Event channel bindings for the user's runtime session.
- Team-specific workflow overlays that add local notification or review nodes.
- Prompt templates that encode team operating style.

No provider, task, workflow, or channel contract change is required. The
workflow still references tasks and channels by their existing ids. Shipping the
templates from the plugin does require either a core template-loader layer or an
install-time materialization step, because templates do not load from
`Config.PluginDirs` today.

### GitHub provider

The GitHub provider remains a plugin because URL parsing, branch lookup, and
workdir acquisition are GitHub-specific.

Plugin-owned files:

```text
plugin.toml
providers/github.toml
resources/github.toml
tasks/github_work.toml
tasks/github_review.toml
workflows/github_coding.toml
bin/plect-github-provider
bin/plect-github-watcher
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

Plugin-owned files:

```text
plugin.toml
resources/okf_goal.toml
bin/plect-okf
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
cannot express, that is an open question for a later contract issue.

## Open questions

- Should same-id conflicts between two plugin layers fail by default, requiring
  the user to resolve the conflict in global config, or should declaration order
  always decide?
- How should generated executable paths be exposed to TOML hooks while keeping
  mounted plugin directories read-only?
- Should templates become a mounted plugin layer in the template loader, or
  should plugin templates be materialized into global config during install?
- Should third-party plugins later gain a signing story on top of explicit
  trust, immutable revision, and content hash?
