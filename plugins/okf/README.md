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
- `tasks/pursue_goal.md`, `tasks/goal_review.md` — the goal task documents:
  `pursue_goal` tracks one goal to completion, `goal_review` reviews one and
  records a verdict. Each is a task document, so its body is the instruction
  and its `done_when` reads the `okf_goal` observer it declares.
- `tasks/goal_bootstrap.toml` — the effect that re-creates any `pursue_goal`
  instance a session lost across a destroy→up cycle.
- **`pursue_goal` declares no chain**, although a goal's reviewer is spawned
  by one. A chain's `workflow` is a static reference, and a reference written
  inside a plugin resolves only in that plugin's own namespace; this plugin
  ships no runnable `goal_review` workflow, because which agent runs a review
  is a choice the OKF specification does not make. A host that wants the
  reviewer spawned automatically declares its own `pursue_goal` task document
  carrying the chain to its own `goal_review` workflow —
  `docs/migrations/shipped-completion-conversion.md` gives it verbatim, and a
  prior version of this plugin's own `config/workflows/goal_review.toml` is
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
- A host-defined `goal_review` workflow in local config for
  `tasks/pursue_goal.toml`'s chain to spawn.
