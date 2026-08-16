# Workdir source vocabulary

This design is governed by
[`../adr/2026-08-17-workdir-source-vocabulary.md`](../adr/2026-08-17-workdir-source-vocabulary.md).

## Contract

A workdir source is a trusted configuration declaration that owns the workdir
lifecycle for sessions of a resource shape.

It has these responsibilities:

- optionally match a resource id and derive the session id;
- run setup to acquire the session workdir;
- run cleanup to release the acquired workdir or related state;
- optionally subscribe an existing session to another resource;
- declare the setup output schema exposed to workflow nodes.

The required setup output key is `workdir`. Other outputs are source-specific
contract fields. Resource-state observation remains outside the workdir source
contract and belongs to resource definitions under `resources/`.

## Vocabulary

The canonical prose term is **workdir source**.

The code-facing vocabulary is:

| Surface | Name |
|---|---|
| Config directory | `workdir_sources/` |
| Workflow field | `workdir_source = "<id>"` |
| Go config type | `WorkdirSourceConfig` |
| Go loader | `LoadWorkdirSources` |
| Workflow detail JSON | `workdir_source`, `workdir_source_info` |
| CLI command noun | `workdir-source` |
| Setup output namespace in template prose | workflow outputs |

The workflow output namespace stays `Workflow.outputs`. It names the
workflow-level pseudo-node outputs visible to templates, not the configuration
kind that produced them.

Single-purpose plugin executables use the concept name and do not use the
host-binary `plect-` prefix. The GitHub executable name is
`github-workdir-source`. A multipurpose executable may expose a
`workdir-source` subcommand when the top-level binary name covers broader
plugin behavior.

`provider` remains available as generic English for an external integration
when prose is not naming the workdir source contract. Core config keys, API
fields, command nouns, and Go identifiers use `workdir source`; provider-neutral
boundary tooling may keep `provider` when it means an external integration
boundary.

## Pairing With Resource Definitions

Resource definitions and workdir sources are separate declarations:

| Question | Declaration |
|---|---|
| What is this resource's observable state? | resource definition |
| Where does this session's work happen, and how is that space acquired and released? | workdir source |

A workflow references one workdir source by id. A resource definition is found
by matching the resource id when state observation or finalization runs.

## Migration Shape

The implementation migration performs these moves in one breaking PR:

- rename `providers/` directories to `workdir_sources/` in global config and
  plugin `config/` trees;
- rename workflow `provider` fields to `workdir_source`;
- rename Go identifiers, JSON fields, command output labels, help text, and
  error strings that expose the concept;
- update contracts prose that uses `provider` for the workdir source contract;
- audit contracts prose that uses `provider` for external integrations and keep
  only provider-neutral phrasing;
- audit `scripts/check-provider-boundary.sh`, its selftest, and the
  `provider-boundary` CI job; under this vocabulary they keep their names
  because they guard against specific external integration leakage;
- rename shipped plugin references and binaries that implement only the workdir
  source contract;
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
the workdir source contract. Historical ADR context, migration instructions,
provider-neutral boundary tooling, and generic external-integration prose may
keep the word when it does not define a config kind, API field, command, or Go
identifier.
