# Migration answer sheet: dynamic-use classification

Every shipped plugin configuration is translated in this directory. This table
maps each translated file's dynamic uses onto the eight classes the audit
defines, so the translation can be diffed against its source with no
unclassified residue.

Classes: **L** literal · **R** static Plecture reference · **P** `from`
projection · **C** CEL computation · **K** Plecture capability · **B**
action-local binding · **S** behavior that stays literal shell · **D**
behavior needing a separate language or semantic decision.

| Source | Translation | Dynamic uses | Classes |
|---|---|---|---|
| `plugins/tmux/config/tasks/tmux.toml` | `tmux/pane.toml` | `.SessionName`, `.WorkspaceDirPath`, `.Self.session_name` in setup/cleanup/health/terminal | P, B, S |
| `plugins/claude/config/tasks/claude.toml` | `claude/runtime.toml` | `get .Prev`, `get .Inputs` ×5, `.SessionName`, `bin` ×2, `terminal` ×2, `shellQuote` | P, B, K, S |
| `plugins/claude/config/tasks/claude_initial_prompt.toml` | `claude/initial_prompt.toml` | `get .Inputs`, `get .Prev`, `.Inputs.template`, `.SessionName`, `terminal` ×3 | P, B, K, S |
| `plugins/claude/config/channels/claude.toml` | `claude/delivery.toml` | `.Inputs.path`, `json .Event` | P, L |
| `plugins/codex/config/tasks/codex.toml` | `codex/runtime.toml` | `get .Inputs` ×5, `get .Prev`, `.SessionName`, `.WorkspaceDirPath`, `.Inputs.tmux_session`, `bin`, `terminal` ×3 | P, B, K, S |
| `plugins/codex/config/tasks/codex_exec.toml` | `codex/exec_runtime.toml` | `get .Inputs` ×5, `get .Prev`, `.SessionName`, `.Inputs.tmux_session`, `bin` ×2, `terminal` ×2 | P, B, K, S |
| `plugins/codex/config/tasks/codex_initial_prompt.toml` | `codex/initial_prompt.toml` | same shape as the claude initial prompt | P, B, K, S |
| `plugins/codex/config/channels/codex_exec.toml` | `codex/exec_delivery.toml` | `.Inputs.*` ×3, `.Event.*` ×3, `with index .Event.metadata "url"` | P, R |
| `plugins/codex/config/channels/terminal_submit.toml` | `codex/terminal_submit.toml` | `terminal` ×3, `.Event.*` with body/summary fallback and url suffix | K, C, B, S |
| `plugins/slack/config/channels/slack.toml` | `slack/delivery.toml` | `.Inputs.*` ×3, `json .Event.*` with fallback | P, C, L |
| `plugins/github/config/workspaces/github.toml` | `github/worktree.toml` | `match` captures in `name`, `.ResourceID`, `.SessionName`, `.WorkspaceDirsRoot`, `get .Inputs` ×4, `get .CleanupInputs`, `.Force`, `.Self.*` ×2, `bin` ×2, `shellQuote` ×8 | P, C, K, B, S |
| `plugins/github/config/resources/github.toml` | `github/issue_pr.toml` | `.ResourceID`, `.WorkspaceDirPath`, `bin` ×2 | P, K |
| `plugins/github/config/tasks/gh_guard.toml` | `github/gh_guard.toml` | `bin`, `.Self.dir` | P, K, B, S |
| `.../tasks/work.toml` + `.../templates/work.md` | `github/work.md` | `from_resource_status` bulk copy; `.ResourceID`, `.Instruction` in the body | P, L |
| `.../tasks/respond.toml` + `.../templates/respond.md` | `github/respond.md` | same as `work` | P, L |
| `.../tasks/investigate.toml` + `.../templates/investigate.md` | `github/investigate.md` | same as `work` | P, L |
| `.../tasks/review.toml` + `.../templates/review.md` | `github/review.md` | `from_resource_status`; the `verdict_current` script; body variable assignment and `or` | P, C, **D** |
| `plugins/okf/config/workspaces/local-okf.toml` | `okf/bundle.toml` | `match` captures in `name`, `.ResourceID`, `.SessionName`, `.Self.workspace_dir`, `bin` ×2 | P, C, K |
| `plugins/okf/config/resources/okf_goal.toml` | `okf/goal.toml` | `.ResourceID`, `.Revision`, `.JudgesJSON` heredoc, `bin` ×2 | P, K |
| `plugins/okf/config/tasks/goal_bootstrap.toml` | `okf/goal_bootstrap.toml` | `.WorkspaceDirPath`, `.Inputs.owner`, `.SessionName`, `get .Inputs`, `bin` | P, K |
| `plugins/okf/config/tasks/pursue_goal.toml` | `okf/pursue_goal.md` | `from_resource_status`, chain `.Work.*` ×3; the setup gate and the absent instruction body are residues below | P, R, **D** |
| `.../tasks/goal_review.toml` + `.../templates/goal_review.md` | `okf/goal_review.md` | `from_resource_status`, the `verdict_current` script; body variable assignment | P, C, **D** |
| `plugins/okf/config/workflows/goal_review.toml` | `okf/goal_review_session.toml` | `.Workflow.outputs.concept_id`, `.Nodes.*.outputs.*` ×4, `get .SessionInputs` | P, R, L |
| `plugins/*/plugin.toml` | `../plugins/manifest.toml` | none: names, paths, build commands, service wiring are all literal | L, R |
| `plugins/catalog.toml` | `../plugins/catalog.toml` | none | L |

Class **S** appears wherever the source's imperative logic survives as a shell
action's literal script. Class **B** appears wherever a value that was
interpolated into that script becomes a binding.

The six work-genre declarations lose classes **S** and **B** entirely. Their
shipped setup was a shell wrapper whose only job was to render an instruction
template and echo it as an output; with the instruction living in the work
document's body, that wrapper has nothing left to do. Nothing about those six
files is imperative any more.

## Class D: the residues

Three things do not map cleanly, and one — the reviewer-recorded revision — was
resolved by the state_schema ratification and is kept here as a record of where
it landed. None is silently widened.

**1. `review`'s and `goal_review`'s `verdict_revision`.** Both documents
complete on a reviewer's self-report, recorded as a revision. As tasks that key
was an `outputs_schema` property with `mutable = true`. It is now declared in the
work document's `[state_schema]`, under the language's one rule for state:
any definition that holds state declares it with `state_schema`, as plain JSON
Schema and with no mutability annotation.

`verdict_revision` is a convention, not a reserved key. Core special-cases
nothing about it — its meaning lives entirely in the configuration that reads it.

**2. `github/review`'s `verdict_current` compares a different revision than the
shipped script.** The shipped script derives the reviewed pull request's branch
from the session branch, reads that branch's head with `git ls-remote`, and only
falls back to `plect resource status`. The ratified dissolution always compares
against the observer's revision:

```toml
verdict_current = { expr = "self.verdict_revision == resource.status.revision" }
```

The two differ when the observer's revision is not the reviewed PR's head — an
issue-keyed resource whose revision is a timestamp rather than a commit. The
mapping adopts the ratified form, so the `git ls-remote` path is not carried
over. Whether that is a correct simplification or a regression is a question
about what `revision` means for an issue-keyed resource.

**3. `pursue_goal` is the one work-genre declaration that does not fit the
genre's evidence.** It has no instruction template, and its `setup` is a
validation gate (`okf-goal task validate-goal-resource`) rather than a
`plect template render` wrapper — so the survey statement "their setup is a
one-line template-render wrapper" holds for five of the six.

Two consequences follow. Its body had to be written here rather than mapped,
because a tracking instance has no shipped instruction; and its gate has nowhere
to go, since a work document owns no lifecycle. The mapping drops the gate on
the reasoning that `goal_parse_status in ["SUCCESS"]` already keeps an
unparseable goal from ever satisfying. That trades a fast failure at
`plect task setup` for an instance that exists but never completes.

**4. The instruction body's interpolation model is unsettled beyond
projection.** `review.md` and `goal_review.md` open with variable assignment,
`get`, and `or`, and all five templates end with an `if` around the extra
instruction. Simple projections map to the value model directly
(`{{.ResourceID}}` becomes `{{ resource.id }}`). Assignment, defaulting, and
conditionals do not, and the ADR explicitly left Markdown asset interpolation
to a separate decision. The mappings keep those constructs in the body and
change only the roots.

Every other dynamic use in every shipped configuration maps onto classes L, R,
P, C, K, B, or S.

## Consequences the translation forces

These are behavior-visible outcomes of the shape change, not open questions.

- **The render-time splice boundary disappears.** Values reach a shell action
  as variables through the binding file, never as script source. The
  `inputs_schema` charset patterns that existed only to close that splice
  (`github/worktree.toml`'s path and template charsets) are dropped. The
  patterns on `model`, `effort`, `path_prepend`, and `state_root` are kept:
  those values still cross into the pane's own shell as keystrokes, which is
  the script's own boundary and is quoted inside the script.
- **A quoted heredoc can no longer carry a value.** The codex tasks wrote their
  hook profile with `<<'HOOKS_TOML'` containing `{{bin ...}}`. A literal script
  cannot interpolate, so the profile is written with `printf` against the bound
  variable instead.
- **`from_resource_status` does not survive.** Every bulk copy becomes per-key
  projections, which is what lets `github/review.toml` rename
  `resource_kind` to `kind` and `checks_status` to `checks` where a task wants
  different names.
- **The channel PATH requirement disappears.** `codex/exec_delivery.toml`
  resolves `codex-exec-enqueue` through `bin`, so this plugin's `scripts/`
  directory no longer has to be on `PATH` for delivery to work.
- **`terminal_submit` stops smuggling a script through argv.** It was an `exec`
  channel running `bash -c <script>` with the event as a positional argument;
  it becomes a shell channel whose script is literal and whose event data is a
  binding.
- **Judge evidence and assignee lists move off argv.** `okf/goal.toml`'s
  finalize and `okf/goal_bootstrap.toml`'s assignee filter use `stdin`.
- **A work document is declared the uniform way.** Its frontmatter is an
  ordinary definition document holding one `[<id>]` block with
  `kind = "work"`, so filename and directory stay non-semantic exactly as a
  TOML definition's do, and the frontmatter needs no parser, schema, or
  validator of its own.
- **Two ids change.** The `local-okf` workspace provider becomes `bundle`,
  because a definition id admits no hyphen. The okf workflow becomes
  `goal_review_session`, because its plugin's work document already owns
  `goal_review` and one layer has a single id namespace across kinds.
- **The instruction template and the task collapse into one file.** Each of the
  six work documents replaces a `tasks/*.toml` plus a `templates/*.md`, and with
  them the `plect template render` wrapper, its empty-output check, its
  `jq` re-emission, and the `instruction` output that carried the result.
- **The frontmatter delimiter changes for the shipped templates.** All five
  shipped instruction templates open with `---`, and
  `app/internal/template.parseFrontmatter` requires exactly that. Work
  documents use `+++`, so those five files and that parser both migrate.
- **String-encoded structured inputs become typed.** `launch_env` becomes an
  object, `mcp_servers` an array of records, and `assignees` an array of
  identity strings, each with a schema the receiving executable's own reading
  already assumed.
