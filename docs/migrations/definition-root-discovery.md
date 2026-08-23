# Definition-root discovery

Every configuration surface is now on the ratified language, so a cascade
layer is read as one **definition root**: everything under it is discovered in
a single pass, and which kind a declaration is comes from its own `kind` field
rather than from the directory it sits in.

Three things follow, and each can turn a config that loaded yesterday into one
that does not:

- A directory under a root is author organization. `config/effects/` and
  `tasks/` mean the same thing, and a declaration is found wherever it sits.
- A layer has one definition id namespace across every kind, so two
  declarations sharing an id inside one layer are a load error even when their
  kinds differ.
- A non-reserved `.toml` under a root must be a definition document. There is
  nowhere left for a file that is not one.

## Backup

```bash
CONFIG_HOME="${PLECT_CONFIG_HOME:-$HOME/.config/plect}"
STAMP="$(date -u +%Y%m%dT%H%M%SZ)"
cp -r "$CONFIG_HOME" "$CONFIG_HOME.migration-backup.$STAMP"
```

## Four shipped declarations are renamed

The unified namespace found collisions the directory-scoped loaders never
compared, and both are the same shape: an agent runtime effect sharing a name
with something else. Each is renamed to the name the ratified conformance
corpus already spells, so this is the language's own naming rather than a new
choice:

| Plugin | Kind | Was | Is |
|---|---|---|---|
| `official/claude` | effect | `claude` | `runtime` |
| `official/claude` | channel | `claude` | `delivery` |
| `official/codex` | effect | `codex_exec` | `exec_runtime` |
| `official/codex` | channel | `codex_exec` | `exec_delivery` |

The **effects** are renamed rather than your workflows, deliberately: a
workflow id is a segment of every session name it produces, so renaming
`workflows/claude.toml` would change the identity of every live session
frozen to it. Renaming the effect changes only the `uses` lines that select
it.

Every invocation these four produce is unchanged — the golden records move
their labels and nothing else.

Rewrite each `uses` that selects one. A node selects the effect; an
`[[…event.channel]]` entry selects the channel:

```toml
[[claude.nodes]]
uses = "runtime"          # was "claude"

[[claude.event.channel]]
name = "runtime"
uses = "delivery"         # was "claude"
```

```bash
CONFIG_HOME="${PLECT_CONFIG_HOME:-$HOME/.config/plect}"
grep -rn 'uses *= *"claude"\|uses *= *"codex_exec"' "$CONFIG_HOME"/workflows/
```

Every hit changes; which name it takes depends on the header above it. A hit
under `[[…nodes]]` becomes `runtime` or `exec_runtime`; a hit under
`[[…event.channel]]` becomes `delivery` or `exec_delivery`.

The other shipped ids the corpus spells differently — `tmux`'s effect as
`pane`, `github`'s provider as `worktree` — are **not** renamed here. No
collision forces them, and every rename is migration churn; reconciling the
corpus and the shipped catalog wholesale is a decision for the schema-authority
slice.

## Delete anything under a root that is not a definition

A definition root has no place for a file that declares nothing, so a
leftover from an earlier migration now stops the layer from loading. The one
this is most likely to be is a `chains/` directory from before chains moved
into the task document that fires them, and its error names itself:

```text
~/.config/plect/chains/review.toml is not a definition: declare [[chains]]
inside the task document whose instances it fires against, and delete the
retired chains/ directory
```

```bash
CONFIG_HOME="${PLECT_CONFIG_HOME:-$HOME/.config/plect}"
rm -rf "$CONFIG_HOME/chains"
```

Anything else — a `notes.toml`, a hand-kept `*.toml.bak`, a copy saved beside
the file it was copied from — has to move out of the root or lose its `.toml`
extension. The reserved names, which are not definitions and are skipped, are
exactly `config.toml`, `catalogs.toml` and `plect.lock`.

A `.md` file is a definition only when it opens with `+++` frontmatter;
anything else under a root is a template asset. That is a loosening rather
than a tightening: a stray Markdown file beside a task document used to fail
the load and is now simply not a declaration.

## Give a same-id pair distinct ids

One layer's ids are shared across kinds, so a workflow and the workspace
provider it selects cannot both be `github`:

```text
~/.config/plect/workspaces/github.toml: github: PLECTURE-CFG-ID-DUPLICATE:
id github is already declared in ~/.config/plect/workflows/github.toml
```

Rename one of the two and update every reference to it — a workflow's
`workspace_provider`, a node's `uses`, a chain's `workflow`, a document's
`resource_observer`, an `[[event.channel]]`'s `uses`. The id is the table
name, so a rename is a TOML edit rather than a `mv`.

**The loader is the detector.** Every collision it reports names both files
and both kinds, and it reports one per load, so the procedure is to run a
command that loads config, fix what it names, and repeat:

```bash
plect workflow list
# id "codex" is declared as kind "effect" in <plugin>/config/tasks/codex.toml
# and kind "workflow" in ~/.config/plect/workflows/codex.toml
```

Use that rather than a hand-rolled scan, for a reason worth stating: a
pre-check over your own config home cannot see the mounted plugin roots, and a
collision between your config and a plugin's is exactly the kind this change
surfaces. There is no CLI surface that enumerates plugin directories, so a
scan that looks complete would not be.

What a local scan *is* good for is the collisions inside your own layer, which
you can fix before mounting anything. A `kind = "task"` declaration lives only
in `+++` frontmatter, so it has to read both serializations:

```bash
CONFIG_HOME="${PLECT_CONFIG_HOME:-$HOME/.config/plect}"
{
  grep -rhoP '^\[\K[A-Za-z_][A-Za-z0-9_]*(?=\])' "$CONFIG_HOME" --include='*.toml'
  find "$CONFIG_HOME" -name '*.md' -type f -exec awk '
    FNR == 1 && $0 != "+++" { nextfile }
    FNR > 1 && $0 == "+++" { nextfile }
    /^\[[A-Za-z_][A-Za-z0-9_]*\]$/ { gsub(/[][]/, ""); print; nextfile }
  ' {} +
} | sort | uniq -d
```

Anything that prints is declared twice in your own layer — a workspace provider
and a workflow both called `orchestrator`, say. The `.md` half reads only a
file that *opens* with `+++`, and only lines inside that frontmatter, so a
template asset is never mistaken for a declaration: a bracketed word in
ordinary prose, and a `[…]` line in a template's own body, are both skipped.
`nextfile` needs GNU awk, which is what `awk` is on most Linux distributions;
without it, check `*.md` frontmatter by eye.

## What did not change

- **Which layers a kind reaches.** A workspace provider, a resource observer
  and a channel still come from the trusted base layers alone — plugins and
  the global config. Effects and task documents still reach an ancestor
  `.plect/` overlay; workflows still reach the workspace directory, and there
  only as node fragments from `.plect/workflows/`.
- **The workspace directory's refusals.** An effect or a task document under
  the workspace directory's own `.plect/` is still a load error, and still
  says so by name: cloned content must not carry shell, or declare the work it
  is about.
- **Every shipped invocation.** The renames move labels and nothing else: the
  golden records regenerate with eight changed record headings — four per
  plugin, the effect's and the channel's — and every invocation line
  byte-identical.

## `plect tick` and `plect check` no longer report warnings

The only config-level warning either surfaced was the surviving `chains/*.toml`
notice, which is now a load error instead. `--json` output no longer carries a
`warnings` key, and nothing is printed to stderr in its place. A script reading
`.warnings` selects nothing rather than failing; a script that treated a
non-empty `.warnings` as a signal has one fewer thing to check.
