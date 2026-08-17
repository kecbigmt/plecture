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

Resource-state observation is outside the workspace provider contract and
belongs to resource definitions.

## Vocabulary

The canonical prose term is **workspace provider**.

Naming is layered:

- concept names describe the durable core responsibility;
- config directories use short plural nouns;
- shipped plugin executables use the plugin's concrete realization of the core
  capability.

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

The workflow output namespace is `Workflow.outputs`. It names the workflow-level
pseudo-node outputs visible to templates, not the configuration kind that
produced them.

Config directories use short plural nouns. Resource definitions live in
`resources/`; workspace provider declarations live in `workspaces/`. The full
concept name appears in prose, Go types, API fields, and command nouns.

Single-purpose plugin executables use the plugin's concrete realization and
omit the host-binary `plect-` prefix. The GitHub workspace-provider executable
name is `github-worktree` because GitHub realizes workspaces as git worktrees.

`provider` is available as generic English for an external integration when
prose is not naming the workspace provider contract. Core config keys, API
fields, command nouns, and Go identifiers use `workspace provider` or the short
config-directory noun `workspace`; provider-neutral boundary tooling may use
`provider` when it means an external integration boundary.

## Pairing With Resource Definitions

Resource definitions and workspace providers are separate declarations:

| Question | Declaration |
|---|---|
| What is this resource's observable state? | resource definition |
| Where does this session's work happen, and how is that space acquired and released? | workspace provider |

A workflow references one workspace provider by id. Resource state observation
and finalization find a resource definition by matching the resource id.

## Workspace Disambiguation

The term workspace names the acquired session work surface. It does not name a
workflow runner, an agent runtime, an environment setup hook, or an OKF bundle.

Directory paths remain concrete in names:

- setup emits `workspace_dir`;
- state exposes `workspace_dir_path`;
- configuration names the root collection `workspace_dirs_root`;
- templates use matching names when they refer to filesystem paths.
