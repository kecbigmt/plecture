# Workspace source vocabulary

## Context

The core term `provider` no longer names the responsibility it carries.
The configuration object called a provider answers one narrow question:
given a session resource, what session id does it map to, and how is the
session workspace acquired, subscribed, and released?

The repository already separates that question from resource observation.
`resources/*.toml` defines how a resource is observed and finalized.
`providers/*.toml` defines optional resource-to-session-name resolution,
workspace setup and cleanup, optional subscription, and the setup output
contract. That split is durable, but the noun `provider` hides it and also
looks like a daemon or external service role.

The vocabulary appears in these surfaces:

- config paths: global `providers/` and plugin `config/providers/`;
- workflow config: `provider = "<id>"`;
- app Go identifiers and JSON: `ProviderConfig`, `LoadProviders`,
  `WorkflowFile.Provider`, `WorkflowDetail.Provider`, `provider_info`,
  `app/internal/config/provider.go`, and related service/task plumbing;
- contracts Go prose: `contracts/state` uses provider in the workspace-source
  sense when describing setup outputs, while `contracts/event` and
  `contracts/state` also use provider in the external-integration sense for
  opaque resource, chat, and source values;
- CLI and user-facing text: workflow detail output, root/status/destroy/template
  help, and load or dispatch errors;
- shipped plugin content: `plugins/github/config/providers/github.toml`,
  `plugins/okf/config/providers/local-okf.toml`, plugin README files, plugin
  metadata, `plugins/catalog.toml`, and executable names such as
  `plect-github-provider`;
- boundary checks: `scripts/check-provider-boundary.sh`,
  `scripts/check-provider-boundary_selftest.sh`, and the `provider-boundary`
  CI job;
- design and migration docs: plugin packaging, plugin config layout migration,
  workdir vocabulary migration, and repository-adjacent wiki prose that explains
  this concept through workspace language.

The binary-naming discussion exposed the issue. A binary that implements a
concept should be named after the concept, but the concept's current name is
too vague to support that rule.

The same discussion ratified that plugin executables do not use the `plect-`
prefix. Plugin ids already qualify executable references and service logs, so
the prefix is reserved for host-installed binaries such as `plect` and
`plect-web`.

The term `workspace` has project history. It was used for older runner and OKF
phrasing that did not crisply separate the abstract place where session work
happens from the filesystem directory used by the present implementation.
Reusing the word is acceptable only if this decision pins the new sense and the
migration disambiguates the legacy sense.

## Decision

The concept is named **workspace source**.

A workspace source is a trusted configuration declaration that may resolve a
resource id to a session id, acquires the session workspace, releases what it
acquires, may bind an existing session to another resource, and declares the
setup outputs available to workflows.

In this decision, a workspace is the session's acquired work surface. It is not
synonymous with a filesystem directory, a runner process, an executor, or an
environment. The present implementation represents a workspace with a
directory path, and path-bearing names carry the `_dir` or `_dir_path` suffix
to keep that concrete detail out of the concept name.

The code-facing vocabulary for the breaking implementation is:

- config directory: `workspace_sources/`;
- workflow field: `workspace_source = "<id>"`;
- Go type and loader: `WorkspaceSourceConfig` and `LoadWorkspaceSources`;
- workflow detail JSON: `workspace_source` and `workspace_source_info`;
- CLI noun, when a direct inspection command exists: `workspace-source`;
- shipped single-purpose executable names: `<plugin>-workspace-source`;
- setup output path key: `workspace_dir`;
- path-valued state or root fields: `workspace_dir_path`,
  `workspace_dirs_root`, and matching Go/template names.

`resource definition` remains the paired concept. A resource definition
observes a resource's state. A workspace source turns a session resource into a
workspace lifecycle.

This decision removes `provider` as the name of the workspace acquisition
contract. It does not ban `provider` as generic English for an external
integration when the prose is not naming a core config kind, API field, command,
or Go identifier. The provider-boundary checker keeps that term because it
guards against core naming a specific external integration provider.

## Consequences

This is a breaking vocabulary change. Plecture is pre-1.0, so the
implementation uses a one-time migration rather than compatibility aliases
that accept both vocabularies.

Core becomes easier to review for boundary leaks: the durable core term names
workspace acquisition, while plugin-specific integrations remain in plugins.
The existing rule that core must not name any specific integration stays
unchanged.

Implementation touches multiple packages and shipped plugin config. It must
update docs, help text, tests, schema-facing JSON names, and migration
instructions in the same PR that changes code.

Future executor work can add workspace representations that are not only local
directories. Existing path contracts stay explicit because their names carry
`_dir` or `_dir_path`.

## Alternatives considered

### Keep `provider`

Keeping the term avoids a breaking rename, but it leaves the same ambiguity
that caused the issue. It does not say what is provided, and it encourages
phrases such as `resource provider` that blur the boundary between workspace
acquisition and resource observation.

### Workdir source

`workdir source` names the concrete directory produced by today's setup hooks,
but it makes the concept a filesystem implementation detail while nearby core
concepts are role words: resource, environment, session, workflow, and task.
It is also too narrow for future executors that acquire a container, VM, or
other bounded work surface while still exposing a directory path to existing
tools.

### Workdir provider

`workdir provider` names the produced directory, but it keeps the overloaded
noun and the `-er` shape that reads like a daemon or vendor role. It is a
partial repair that leaves the disputed vocabulary in the core model.

### Workspace definition

`workspace definition` pairs neatly with `resource definition`, but it implies
a static declaration of a workspace rather than an active lifecycle contract.
The concept does not merely define a place; it runs setup, cleanup,
subscription, and optional resolution.

### Provider/resource concept-split mains

Naming the workspace-acquisition executable main after the old provider side of
the provider/resource concept split would make binary names mirror stale
vocabulary. The resource-definition main may keep the resource concept name;
the rejection applies to carrying `provider` forward for workspace acquisition.

### Retain `plect-` for plugin executables

Keeping names such as `plect-github-workspace-source` would preserve the old
host-binary prefix inside plugin packages. It loses because plugin executable
references are already qualified by plugin id, and `plect-` is reserved for
host-installed binaries.

### CLI as the executable noun

`cli` is rejected for binaries or packages that implement this contract because
it names an interface style, not a responsibility. It creates no single
responsibility pressure: any command-line surface can be called a CLI, even
when it mixes unrelated contract work.

### Workspace as generic runner language

Reusing workspace without a pinned sense would revive the ambiguity from older
runner and OKF prose. It loses because the concept needs to mean the acquired
session work surface specifically, with directory paths named by `_dir` fields.
