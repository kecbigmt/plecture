# Workspace source vocabulary

This design is governed by
[`../adr/2026-08-17-workspace-source-vocabulary.md`](../adr/2026-08-17-workspace-source-vocabulary.md).

## Contract

A workspace source is a trusted configuration declaration that owns the
workspace lifecycle for sessions of a resource shape.

It has these responsibilities:

- optionally match a resource id and derive the session id;
- run setup to acquire the session workspace;
- run cleanup to release the acquired workspace or related state;
- optionally subscribe an existing session to another resource;
- declare the setup output schema exposed to workflow nodes.

A workspace is the session's acquired work surface. It is not the executor,
the task runner, the environment, or the resource itself. The present
implementation exposes the acquired filesystem location through a required
setup output key named `workspace_dir`. Other outputs are source-specific
contract fields.

Resource-state observation remains outside the workspace source contract and
belongs to resource definitions under `resources/`.

## Vocabulary

The canonical prose term is **workspace source**.

The code-facing vocabulary is:

| Surface | Name |
|---|---|
| Config directory | `workspace_sources/` |
| Workflow field | `workspace_source = "<id>"` |
| Go config type | `WorkspaceSourceConfig` |
| Go loader | `LoadWorkspaceSources` |
| Workflow detail JSON | `workspace_source`, `workspace_source_info` |
| CLI command noun | `workspace-source` |
| Setup output path key | `workspace_dir` |
| State path field | `workspace_dir_path` |
| Root path field | `workspace_dirs_root` |
| Setup output namespace in template prose | workflow outputs |

The workflow output namespace stays `Workflow.outputs`. It names the
workflow-level pseudo-node outputs visible to templates, not the configuration
kind that produced them.

Single-purpose plugin executables use the concept name and do not use the
host-binary `plect-` prefix. The GitHub executable name is
`github-workspace-source`. A multipurpose executable may expose a
`workspace-source` subcommand when the top-level binary name covers broader
plugin behavior.

`provider` remains available as generic English for an external integration
when prose is not naming the workspace source contract. Core config keys, API
fields, command nouns, and Go identifiers use `workspace source`;
provider-neutral boundary tooling may keep `provider` when it means an
external integration boundary.

## Pairing With Resource Definitions

Resource definitions and workspace sources are separate declarations:

| Question | Declaration |
|---|---|
| What is this resource's observable state? | resource definition |
| Where does this session's work happen, and how is that space acquired and released? | workspace source |

A workflow references one workspace source by id. A resource definition is
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

- rename `providers/` directories to `workspace_sources/` in global config and
  plugin `config/` trees;
- rename workflow `provider` fields to `workspace_source`;
- rename setup output `workdir` to `workspace_dir`;
- rename path-bearing state/config/template fields from `workdir` to
  `workspace_dir` or `workspace_dir_path` according to the field's existing
  shape;
- rename Go identifiers, JSON fields, command output labels, help text, and
  error strings that expose the concept;
- update contracts prose that uses `provider` for the workspace source
  contract;
- audit contracts prose that uses `provider` for external integrations and keep
  only provider-neutral phrasing;
- audit workspace prose that predates this definition and either align it to
  the acquired-work-surface sense or rename it to the narrower concept it means;
- audit `scripts/check-provider-boundary.sh`, its selftest, and the
  `provider-boundary` CI job; under this vocabulary they keep their names
  because they guard against specific external integration leakage;
- rename shipped plugin references and binaries that implement only the
  workspace source contract;
- update docs and repository-adjacent wiki prose that describes the concept;
- add a one-time migration procedure under `docs/migrations/` with a backup
  step.

The migration does not add dual-read compatibility. A post-migration plect
binary reads the new vocabulary only.

## Verification

The implementation PR carries behavior tests for the config loader, workflow
loader, workflow display, lifecycle setup/cleanup, subscription, shipped plugin
config, and legacy migration path.

One-time survey evidence belongs in the PR body. It should include exact
commands used to confirm that no core/user-facing `provider` vocabulary names
the workspace source contract. Historical ADR context, migration instructions,
provider-neutral boundary tooling, and generic external-integration prose may
keep the word when it does not define a config kind, API field, command, or Go
identifier.

The same evidence should confirm that workspace prose in core docs, code,
tests, and shipped plugins uses the acquired-work-surface sense from this
design. Any remaining workspace wording with runner, environment, or
domain-bundle meaning must be renamed to that narrower concept.

