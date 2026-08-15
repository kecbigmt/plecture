# Plugin packaging, reference resolution, lockfile, and trust model

This document defines the plugin model for distributing reusable Plecture
configuration and executable adapters. The goal is to reduce bootstrap cost
without moving provider, tool, or domain knowledge into core.

The design keeps one concept and one word: plugin. A plugin is a
distributable package that may contain executable adapters, config resources,
templates, and metadata. A plugin with only configuration is still a plugin.

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
name = "plect-github-watcher"
path = "bin/plect-github-watcher"
build = "go build -o bin/plect-github-watcher ./cmd/plect-github-watcher"

[[executables]]
name = "plect-github-provider"
path = "bin/plect-github-provider"
build = "go build -o bin/plect-github-provider ./cmd/plect-github-provider"
```

Metadata fields:

| Field | Required | Meaning |
|---|---:|---|
| `name` | yes | Stable plugin identity. It is used in lockfile entries and diagnostics. |
| `version` | no | Informational plugin package version for diagnostics and list/show commands. |
| `plect_min_version` | yes | Minimum plect version required to load the plugin. |
| `description` | no | Human-readable summary for list/show commands. |
| `executables` | no | Relative paths for scripts or binaries shipped by the plugin. |

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

The same hook against a plugin-shipped executable names the plugin reference at
the command position:

```toml
scope = "run"
setup = '{{bin "agent-runtime"}} launch --workdir {{.Session.WorkdirPath | shellQuote}}'
cleanup = '{{bin "agent-runtime"}} stop --id {{.Self.runtime_id | shellQuote}}'
```

`{{bin "agent-runtime"}}` is the tersest viable form: it keeps the executable
reference in the place a command name already appears, and a plugin with exactly
one executable can be referenced by plugin name alone. A multi-executable plugin
uses `{{bin "plugin-name/executable-name"}}`; an ambiguous single-name reference
is a load error. For locked sources, the helper returns an absolute path inside
the read-only mounted plugin cache. For editable path sources, it returns an
absolute path inside the directly mounted development tree. It never writes
generated paths back into user config.

The same template-variable reference and read-only-mount rules apply to scripts
and built binaries for locked sources; editable path sources keep the same
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
The lock pins the source revision and content hash, so a locally built binary is
a derivative of exactly the trusted content. That is a stronger verification
chain than unsigned prebuilt distribution. Running a build command is within the
trust already granted by confirming the source, because plugin config can run
shell during normal operation. Plecture-maintained plugins also remain available
through the existing Nix path.

## Reference resolution

Users declare selected plugins in trusted user-owned config, not inside cloned
workdir content. The global declaration file lives at:

```text
~/.config/plect/plugins.toml
```

Example:

```toml
[[trusted_sources]]
source_prefix = "git+https://github.com/example/"

[[trusted_sources]]
source_prefix = "git+ssh://git@example.com/team/"

[[trusted_sources]]
source_prefix = "path:///home/user/src/"

[[plugins]]
name = "github"
source = "git+https://github.com/example/plect-github-plugin"
revision = "4f2db5e4c2b4b4a8c6c0f6c0d4d2d2ecf0c1a0b3"

[[plugins]]
name = "agent-runtime"
source = "git+ssh://git@example.com/team/agent-runtime-plugin"
revision = "v0.6.1"

[[plugins]]
name = "okf"
source = "path:///home/user/src/okf-plugin"
sha256 = "sha256:2b7c..."
```

Declaration fields:

| Field | Required | Meaning |
|---|---:|---|
| `name` | yes | Expected plugin name. It must match `plugin.toml`; a mismatch is a load error. |
| `source` | yes | Source locator. Supported v1 schemes are `git+https`, `git+ssh`, and `path`. |
| `revision` | scheme-dependent | Git revision to resolve. Required for Git sources. |
| `sha256` | scheme-dependent | Content hash in `sha256:<digest>` form. Required for locked path sources. |
| `editable` | no | Path-only development mode. When true, the path is mounted directly and is not reproducible. |

`trusted_sources` is a classless, human-owned trust list. Each entry contains a
`source_prefix`; core only checks whether a plugin source string matches one of
those prefixes. There is no machine-verified officialness, and core does not
contain an official host, owner, registry, or provider list. A declared plugin
whose source matches no `trusted_sources` entry is a load error.

Git is the default transport for reference distribution, not a requirement of
the resolution model. The model needs only fetch, revision or hash
verification, and read-only mount; the `path` scheme is the git-free floor. Core
invokes git as an external command and does not take a git library dependency.
Environments without git lose only the git schemes, with a clear error.

Resolution flow:

1. Read plugin declarations from global user config. The first implementation
   does not read plugin declarations from ancestor overlays.
2. Fail if a declaration's `source` matches no `trusted_sources` entry.
3. Resolve each declaration to concrete content:
   - Git source: fetch the source and resolve `revision` to a commit hash.
   - Locked path source: resolve symlinks and verify the tree against `sha256`.
   - Editable path source: resolve symlinks and mount the path directly.
4. Verify the resolved content against the lockfile, except editable path
   sources.
5. Read `plugin.toml` from the verified tree.
6. Fail if `plugin.toml`'s `name` differs from the declaration's `name`.
7. Check `plect_min_version` against the running plect version.
8. Mount the plugin directory as a read-only base config layer, except editable
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
editable = false
```

Recorded fields:

| Field | Meaning |
|---|---|
| `name` | Plugin identity from `plugin.toml`. |
| `source` | Original source locator. |
| `requested_revision` | User declaration, such as a tag or commit. |
| `resolved_revision` | Immutable source revision, such as a Git commit. |
| `content_hash` | Digest of the mounted tree after checkout or extraction. |
| `version` | Optional plugin package version read from metadata when present. |
| `plect_min_version` | Compatibility floor read from metadata. |
| `editable` | Whether the source was mounted in explicit local development mode. |

Add and update commands:

- `plect plugin add <source> --revision <rev>` resolves content, writes or
  updates `plugins.toml`, and writes `plect.lock`. If the source matches no
  existing `trusted_sources` entry, it shows the URL, resolved revision, and
  content hash and asks for interactive confirmation. On consent, it appends the
  source prefix to `trusted_sources` so the trust decision is an auditable diff
  in a human-owned file.
- `plect plugin update <name> --revision <rev>` performs an explicit revision
  bump and lockfile rewrite.
- `plect plugin verify` re-hashes cached content and fails if it differs from
  the lockfile.
- `plect plugin list` shows declared, resolved, locked, and compatibility state.

Non-interactive contexts fail instead of prompting for first-seen sources. Any
override flag must be explicit and visible in command history.

No command silently advances a plugin. A missing or mismatched lock entry is a
load error unless the user is running an explicit add or update command.
Editable path sources are the only exception: they are marked non-reproducible
in `plect plugin list`, excluded from `plect plugin verify --locked`, and meant
only for local plugin development.

`plect.lock` carries mechanical pinning only: source, revision, content hash,
metadata, and editable state. Trust policy stays in `trusted_sources`; the
lockfile never records trust semantics.

Alternatives considered: recording a trust class in `plect.lock` was useful
only in the earlier class-based model. With classless explicit trust, that
reviewer nit is moot because the lockfile is deliberately mechanical-only.

## Trust boundary

Plecture config can run shell on the user's machine. Plugin config has the same
power because provider setup, resource observe/finalize, task setup/cleanup,
environment exec, and exec channels all execute commands.

Threat model:

- The config directory, including `trusted_sources`, is the root of trust. It is
  in the same trust boundary as the `plect` binary because Plecture config is
  effectively executable shell already.
- `trusted_sources` is policy and `plect.lock` is a mechanical record. Keeping
  them separate preserves their ownership split: humans edit policy; tooling
  writes the lock.
- A `trusted_sources` edit alone changes nothing mounted. Content immutability
  still comes from revision and hash pinning in `plect.lock`, verified on every
  load.
- Plugin declarations are global-only. A dispatched session's workdir cannot
  inject plugins or trust new sources through cloned content.
- `trusted_sources` and lock edits are human actions. Agent-driven workflows
  must not edit them automatically; they should fail or escalate instead.
- First-seen sources require interactive confirmation through `plect plugin add`.
  Non-interactive contexts fail unless the user supplied an explicit override
  that remains visible in command history.
- Remote attacks are constrained by transport authentication plus revision and
  hash pinning: trust is established at add time, then verified on every load.
- Users can point at any source they choose to trust. Third-party installation
  is possible by design, and responsibility for trusting a source lies with the
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
conflicts would make behavior depend on list order in `plugins.toml`. That is
too easy to miss during review, especially for executable shell hooks, so plugin
layer conflicts fail loud.

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
5. Run `plect plugin add <source> --revision <rev>` for each plugin to write
   `plect.lock`.
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
bin/plect-agent-activity
```

Plugin-owned templates:

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
workflow still references tasks and channels by their existing ids. Shipping
templates from the plugin requires the generic read-only template-loader layer
described above; it does not require task or workflow contract changes.
`bin/plect-agent-activity` is a build-less shell script with its interpreter in
the shebang, referenced from hooks through the same `{{bin ...}}` helper as a
built binary.

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
cannot express, that belongs in a later contract issue.
