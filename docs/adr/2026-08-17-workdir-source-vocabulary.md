# Workdir source vocabulary

## Context

The core term `provider` no longer names the responsibility it carries.
The configuration object called a provider answers one narrow question:
given a session resource, what session id does it map to, and how is the
session workdir acquired, subscribed, and released?

The repository already separates that question from resource observation.
`resources/*.toml` defines how a resource is observed and finalized.
`providers/*.toml` defines optional resource-to-session-name resolution,
workdir setup and cleanup, optional subscription, and the setup output
contract. That split is durable, but the noun `provider` hides it and also
looks like a daemon or external service role.

The vocabulary appears in these surfaces:

- config paths: global `providers/` and plugin `config/providers/`;
- workflow config: `provider = "<id>"`;
- app Go identifiers and JSON: `ProviderConfig`, `LoadProviders`,
  `WorkflowFile.Provider`, `WorkflowDetail.Provider`, `provider_info`,
  `app/internal/config/provider.go`, and related service/task plumbing;
- contracts Go prose: `contracts/state` uses provider in the workdir-source
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
  workdir vocabulary migration, and repository-adjacent wiki prose that still
  explains the concept through retired workspace language.

The binary-naming discussion exposed the issue. A binary that implements a
concept should be named after the concept, but the concept's current name is
too vague to support that rule.

## Decision

The concept is named **workdir source**.

A workdir source is a trusted configuration declaration that may resolve a
resource id to a session id, acquires the session workdir, releases what it
acquires, may bind an existing session to another resource, and declares the
setup outputs available to workflows.

The code-facing vocabulary for the breaking implementation is:

- config directory: `workdir_sources/`;
- workflow field: `workdir_source = "<id>"`;
- Go type and loader: `WorkdirSourceConfig` and `LoadWorkdirSources`;
- workflow detail JSON: `workdir_source` and `workdir_source_info`;
- CLI noun, when a direct inspection command exists: `workdir-source`;
- shipped single-purpose executable names: `<plugin>-workdir-source`, with the
  existing `plect-` prefix retained where the plugin already uses it.

`resource definition` remains the paired concept. A resource definition
observes a resource's state. A workdir source turns a session resource into a
workdir lifecycle.

## Consequences

This is a breaking vocabulary change. Plecture is pre-1.0, so the
implementation uses a one-time migration rather than compatibility aliases
that accept both vocabularies.

Core becomes easier to review for boundary leaks: the durable core term names
workdir acquisition, while plugin-specific integrations remain in plugins.
The existing rule that core must not name any specific integration stays
unchanged.

Implementation touches multiple packages and shipped plugin config. It must
update docs, help text, tests, schema-facing JSON names, and migration
instructions in the same PR that changes code.

## Alternatives considered

### Keep `provider`

Keeping the term avoids a breaking rename, but it leaves the same ambiguity
that caused the issue. It does not say what is provided, and it encourages
phrases such as `resource provider` that blur the boundary between workdir
acquisition and resource observation.

### Workdir provider

`workdir provider` names the produced thing, but it keeps the overloaded noun
and the `-er` shape that reads like a daemon or vendor role. It is a partial
repair that leaves the disputed vocabulary in the core model.

### Provider/resource concept-split mains

Naming executable mains directly after the old provider/resource concept split
would make binary names mirror stale vocabulary. It loses because `provider`
is the term under dispute, and `resource` already names the observation side of
the model rather than workdir acquisition.

### CLI as the executable noun

`cli` is rejected for binaries or packages that implement this contract because
it names an interface style, not a responsibility. It creates no single
responsibility pressure: any command-line surface can be called a CLI, even
when it mixes unrelated contract work.

### Workdir definition

`workdir definition` pairs neatly with `resource definition`, but it implies a
static declaration of a workdir rather than an active lifecycle contract. The
concept does not merely define a path; it runs setup, cleanup, subscription, and
optional resolution.

### Workdir acquisition

`workdir acquisition` names the central act, but it drops cleanup and
subscription. It also reads as an operation rather than the durable config
object that workflows reference.

### Workspace source

`workspace` is rejected because Plecture already migrated user-facing state to
`workdir`. Reintroducing workspace language would make the durable vocabulary
less precise.
