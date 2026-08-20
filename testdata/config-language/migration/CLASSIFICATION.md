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
| `plugins/github/config/tasks/work.toml` | `github/work.toml` | `get .Inputs`, `.SessionName`, `from_resource_status` bulk copy | P, B, S |
| `plugins/github/config/tasks/respond.toml` | `github/respond.toml` | same as `work` | P, B, S |
| `plugins/github/config/tasks/investigate.toml` | `github/investigate.toml` | same as `work` | P, B, S |
| `plugins/github/config/tasks/review.toml` | `github/review.toml` | `get .Inputs`, `.SessionName`, `from_resource_status`, the `verdict_current` script | P, C, B, S, **D** |
| `plugins/okf/config/workspaces/local-okf.toml` | `okf/bundle.toml` | `match` captures in `name`, `.ResourceID`, `.SessionName`, `.Self.workspace_dir`, `bin` ×2 | P, C, K |
| `plugins/okf/config/resources/okf_goal.toml` | `okf/goal.toml` | `.ResourceID`, `.Revision`, `.JudgesJSON` heredoc, `bin` ×2 | P, K |
| `plugins/okf/config/tasks/goal_bootstrap.toml` | `okf/goal_bootstrap.toml` | `.WorkspaceDirPath`, `.Inputs.owner`, `.SessionName`, `get .Inputs`, `bin` | P, K |
| `plugins/okf/config/tasks/pursue_goal.toml` | `okf/pursue_goal.toml` | `.ResourceID`, `bin`, `from_resource_status`, chain `.Work.*` ×3 | P, R, K |
| `plugins/okf/config/tasks/goal_review.toml` | `okf/goal_review.toml` | `.SessionName`, `get .Inputs`, `from_resource_status`, the `verdict_current` script | P, C, B, S |
| `plugins/okf/config/workflows/goal_review.toml` | `okf/goal_review_session.toml` | `.Workflow.outputs.concept_id`, `.Nodes.*.outputs.*` ×4, `get .SessionInputs` | P, R, L |
| `plugins/*/plugin.toml` | `../plugins/manifest.toml` | none: names, paths, build commands, service wiring are all literal | L, R |
| `plugins/catalog.toml` | `../plugins/catalog.toml` | none | L |

Class **S** appears wherever the source's imperative logic survives as a shell
action's literal script. Class **B** appears wherever a value that was
interpolated into that script becomes a binding.

## Class D: the one residue

`github/review.toml`'s `verdict_current` is the only translated behavior whose
semantics are not settled by the ratified language.

The shipped script does not compare against the observer's revision directly.
It derives the reviewed pull request's branch from the session branch, reads
that branch's head with `git ls-remote`, and only falls back to
`plect resource status` when that yields nothing. The ratified dissolution is

```toml
verdict_current = { expr = "self.verdict_revision == resource.status.revision" }
```

which always compares against the observer's revision.

The two differ when the observer's revision is not the reviewed PR's head — an
issue-keyed resource whose revision is a timestamp rather than a commit. The
translation above adopts the ratified form, so the `git ls-remote` path is not
carried over. Whether that is a correct simplification or a regression is a
semantic question about what `revision` means for an issue-keyed resource, not
a question about the language, and is listed for owner decision rather than
settled here.

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
- **Two ids change.** The `local-okf` workspace provider becomes `bundle`,
  because a definition id admits no hyphen. The okf workflow becomes
  `goal_review_session`, because its plugin's task already owns `goal_review`
  and one layer has a single id namespace across kinds.
- **String-encoded structured inputs become typed.** `launch_env` becomes an
  object, `mcp_servers` an array of records, and `assignees` an array of
  identity strings, each with a schema the receiving executable's own reading
  already assumed.
