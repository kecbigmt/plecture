# Workspace provider vocabulary

This design is governed by
[`../adr/2026-08-17-workspace-provider-vocabulary.md`](../adr/2026-08-17-workspace-provider-vocabulary.md).

## Contract

A workspace provider is a trusted configuration declaration that owns the
workspace lifecycle for sessions of a resource shape.

It has these responsibilities:

- optionally match a resource id and derive the session id;
- run setup to acquire the session workspace;
- run cleanup to release the acquired workspace or related state;
- optionally subscribe an existing session to another resource;
- declare the setup output schema exposed to workflow nodes.

A workspace is the session's acquired work surface. It is not the executor,
the task runner, the environment, or the resource itself. The filesystem
implementation exposes the acquired location through a required setup output
key named `workspace_dir`. Other outputs are provider-specific contract fields.

Resource-state observation remains outside the workspace provider contract and
belongs to resource definitions under `resources/`.

## Vocabulary

The canonical prose term is **workspace provider**.

The code-facing vocabulary is:

| Surface | Name |
|---|---|
| Config directory | `workspaces/` |
| Workflow field | `workspace_provider = "<id>"` |
| Go config type | `WorkspaceProviderConfig` |
| Go loader | `LoadWorkspaceProviders` |
| Workflow detail JSON | `workspace_provider`, `workspace_provider_info` |
| CLI command noun | `workspace-provider` |
| Setup output path key | `workspace_dir` |
| State path field | `workspace_dir_path` |
| Root path field | `workspace_dirs_root` |
| Setup output namespace in template prose | workflow outputs |

The workflow output namespace stays `Workflow.outputs`. It names the
workflow-level pseudo-node outputs visible to templates, not the configuration
kind that produced them.

Config directories use short plural nouns. Resource definitions live in
`resources/`; workspace provider declarations live in `workspaces/`. The full
concept name appears in prose, Go types, API fields, and command nouns.

Executable names are not a mechanical copy of concept names. Single-purpose
plugin executables use the short plugin capability noun and do not use the
host-binary `plect-` prefix. The GitHub executable name is `github-workspace`.
A multipurpose executable may expose a `workspace-provider` subcommand when the
top-level binary name covers broader plugin behavior.

`provider` remains available as generic English for an external integration
when prose is not naming the workspace provider contract. Core config keys, API
fields, command nouns, and Go identifiers use `workspace provider` or the
short config-directory noun `workspace`; provider-neutral boundary tooling may
keep `provider` when it means an external integration boundary.

## Pairing With Resource Definitions

Resource definitions and workspace providers are separate declarations:

| Question | Declaration |
|---|---|
| What is this resource's observable state? | resource definition |
| Where does this session's work happen, and how is that space acquired and released? | workspace provider |

A workflow references one workspace provider by id. A resource definition is
found by matching the resource id when state observation or finalization runs.

## Workspace Disambiguation

The term workspace names the acquired session work surface. It does not name a
workflow runner, an agent runtime, an environment setup hook, or an OKF bundle.

Directory paths remain concrete in names:

- setup emits `workspace_dir`;
- state exposes `workspace_dir_path`;
- configuration names the root collection `workspace_dirs_root`;
- templates use matching names when they refer to filesystem paths.

## Migration Shape

The implementation migration performs these moves in one breaking PR:

- rename `providers/` directories to `workspaces/` in global config and plugin
  `config/` trees;
- rename workflow `provider` fields to `workspace_provider`;
- rename setup output `workdir` to `workspace_dir`;
- rename path-bearing state/config/template fields from `workdir` to
  `workspace_dir` or `workspace_dir_path` according to the field's existing
  shape;
- rename Go identifiers, JSON fields, command output labels, help text, and
  error strings that expose the concept;
- update contracts prose that uses `provider` for the workspace provider
  contract;
- audit contracts prose that uses `provider` for external integrations and keep
  only provider-neutral phrasing;
- audit workspace prose that predates this definition and either align it to
  the acquired-work-surface sense or rename it to the narrower concept it means;
- audit `scripts/check-provider-boundary.sh`, its selftest, and the
  `provider-boundary` CI job; under this vocabulary they keep their names
  because they guard against specific external integration leakage;
- rename shipped plugin references and binaries that implement only the
  workspace provider contract to short capability names such as
  `github-workspace`;
- update docs and repository-adjacent wiki prose that describes the concept;
- supersede `docs/migrations/workdir-vocabulary-migration.md` for this
  breaking release with a new one-time procedure that accepts both worktree-era
  and workdir-era names and writes only the workspace vocabulary, so operators
  do not chain two state rewrites;
- add a one-time migration procedure under `docs/migrations/` with a backup
  step.

The migration does not add dual-read compatibility. A post-migration plect
binary reads the new vocabulary only.

## Verification

The implementation PR carries behavior tests for the config loader, workflow
loader, workflow display, lifecycle setup/cleanup, subscription, shipped plugin
config, and legacy migration path.

One-time survey evidence belongs in the PR body. It should include exact
commands used to confirm that no core/user-facing bare `provider` vocabulary
names the workspace provider contract. Historical ADR context, migration
instructions, provider-neutral boundary tooling, and generic
external-integration prose may keep the word when it does not define a config
kind, API field, command, or Go identifier.

The same evidence should confirm that workspace prose in core docs, code,
tests, and shipped plugins uses the acquired-work-surface sense from this
design. Any remaining workspace wording with runner, environment, or
domain-bundle meaning must be renamed to that narrower concept.
