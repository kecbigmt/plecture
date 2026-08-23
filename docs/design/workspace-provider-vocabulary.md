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
- declare the setup output schema exposed to workflow nodes;
- optionally declare the parameters a workflow may set to steer those hooks.

A workspace is the session's acquired work surface. It is not the executor,
the task runner, or the resource itself. The filesystem implementation exposes
the acquired location through a required setup output key named
`workspace_dir`. Other outputs are provider-specific contract fields.

Parameters are declared as `inputs_schema` on the workspace provider and set
as `workspace_provider_inputs` on the workflow. The values are literal data,
and `setup` and `cleanup` read them as `inputs.<key>`. `subscribe` receives
none: it resolves a workspace provider from the resource alone, with no
workflow in scope to have set one.

Workspace providers and workflows load independently of one another, so
values are validated against the declaring workspace provider's schema where
a session first needs them: at create, up, and destroy. A workflow setting a
parameter the workspace provider does not declare, or setting one against a
workspace provider that declares no `inputs_schema`, fails there rather than
having the value discarded.

This is the parameterization rung of the customization ladder in
[`task-nesting.md`](task-nesting.md), governed by
[`../adr/2026-08-18-rung-one-parameterization-surfaces.md`](../adr/2026-08-18-rung-one-parameterization-surfaces.md).

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
| Parameter schema field | `inputs_schema` |
| Workflow parameter table | `workspace_provider_inputs` |
| State path field | `workspace_dir_path` |
| Root path field | `workspace_dirs_root` |
| Setup output namespace in prose | workflow outputs |

The workflow output namespace is `workflow.outputs`. It names the
workflow-level pseudo-node outputs a value may project from, not the
configuration kind that produced them.

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
workflow runner, an agent runtime, or an OKF bundle.

Directory paths remain concrete in names:

- setup emits `workspace_dir`;
- state exposes `workspace_dir_path`;
- configuration names the root collection `workspace_dirs_root`;
- a value projecting one of them uses the matching name.
