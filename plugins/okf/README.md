# okf

The OKF goal resource: it turns a `local-okf://<owner>/<concept-id>`
resource id into a bundle-contained file, parses a goal Concept's YAML
frontmatter and "## Done When" checklist, and records completion. Plect's
core knows nothing about OKF or knowledge bundles — everything OKF-shaped
lives here.

The plugin is scoped by the OKF specification, not by one use case: v1 owns
only the goal resource, the concept kind with machine semantics today
(observation, finalization, workspace dispatch). Bundle plumbing shared by
future OKF concepts — owner alias resolution, bundle root discovery,
containment checks, frontmatter parsing — is kept internally separable
(`internal/bundle`, `internal/goal`) so a later concept kind can reuse it
without duplicating it. Bundle records without machine semantics (a
retrospective, say) stay plain files outside the plugin.

`local-okf://` is the resource identifier scheme goal files and session
aliases already reference; it stays as-is even though the package and
plugin are named `okf`, because changing it would be a breaking data-format
change out of scope for this plugin's extraction.

## Contents

- `workspaces/local_okf.toml` — the `local_okf` workspace provider: its
  `match` / `name` pair derives a session id offline from the resource
  identifier's captures; `setup` and `cleanup` invoke the executable below to
  acquire and release a read-context workspace directory over the owner's
  bundle.
- `resources/okf_goal.toml` — the goal resource: `observe` reports parse
  and completion state; `finalize` records a done_when-satisfied goal's
  completion, once.
- `cmd/okf-bundle`, `cmd/okf-goal` — the executables those hooks run. Their
  shared `internal/` packages (`bundle`, `goal`, `resource`, `workspace`,
  `task`) hold the actual logic; each `cmd/` main is a thin flag-parsing and
  JSON-encoding shell around them — `okf-bundle` wraps the workspace
  provider hooks, `okf-goal` wraps the resource and goal task hooks.
- `tasks/goal_bootstrap.toml` — the effect that re-creates any `pursue_goal`
  instance a session lost across a destroy→up cycle. It shells out to
  `plect task setup pursue_goal`, so a host that enables this plugin must
  declare its own `pursue_goal` task document for that instance to attach
  to — this plugin ships no task documents of its own.
- **No `pursue_goal` or `goal_review` task document ships here.** Which
  agent reviews a goal, on what instruction, and under what completion
  gates is a choice the OKF specification does not make, so it is host
  config, not plugin config. See `docs/language/tasks.md` for what a task
  document needs (`done_when`, `resource_observer`, `[[chains]]`);
  `pursue_goal`'s `done_when` reads this plugin's `okf_goal` observer, and
  its chain (if any) spawns the host's own `goal_review` workflow — a prior
  version of this plugin's own `config/workflows/goal_review.toml` is
  readable from this repository's git history as a starting point.

## Install

Build the executables and put them on the `PATH` plect's hooks run with:

```bash
go build -o <bindir>/okf-goal ./cmd/okf-goal
go build -o <bindir>/okf-bundle ./cmd/okf-bundle
```

Then add this directory to `plugin_dirs` in `~/.config/plect/config.toml`,
or enable it through a registered catalog (see
`docs/design/plugin-packaging.md`), so its `workspaces/`, `resources/`,
and `tasks/` are loaded.

## Outputs

`workspaces/local_okf.toml`'s `setup` emits `workspace_dir`, `owner`,
`concept_id`, and `concept_path`. `resources/okf_goal.toml`'s `observe`
emits `goal_parse_status`, `goal_status`, `checklist_status`,
`goal_revision`, `revision`, `open_items`, and `observe_error`. Read them in
a task or template as `.Workflow.outputs.<key>` or a resource-bound task's
own copied outputs.

## Requirements

- `plect` itself on `PATH` — every resolution step shells out to
  `plect status` to find the owner's orchestrator session, and
  `goal_bootstrap` shells out to `plect task setup` to create instances.
- A knowledge bundle at `<orchestrator workspace directory>/knowledge/bundle/`,
  with goal Concept files under `goals/`.
- A host-defined `pursue_goal` task document for `goal_bootstrap` to
  instantiate (see Contents above), and, if it declares a chain to spawn a
  reviewer, a host-defined `goal_review` workflow for that chain to target.
