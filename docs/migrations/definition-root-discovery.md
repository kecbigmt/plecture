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

## Two shipped channels are renamed

The unified namespace found a collision the directory-scoped loaders never
compared: the claude and codex plugins each declared one id twice, as an
effect and as the channel that reaches it. The channels are renamed, since a
workflow node and an `inner` reference name the effects:

| Plugin | Was | Is |
|---|---|---|
| `official/claude` | channel `claude` | channel `delivery` |
| `official/codex` | channel `codex_exec` | channel `exec_delivery` |

The effects keep their ids, and every invocation the channels produce is
unchanged — only the name is. Rewrite each `[[<id>.event.channel]]` that
selects one:

```toml
# before
[[claude.event.channel]]
name = "runtime"
uses = "claude"

# after
[[claude.event.channel]]
name = "runtime"
uses = "delivery"
```

```bash
CONFIG_HOME="${PLECT_CONFIG_HOME:-$HOME/.config/plect}"
grep -rn 'uses *= *"claude"\|uses *= *"codex_exec"' "$CONFIG_HOME"/workflows/
```

Each hit is a channel binding if it sits under an `[[…event.channel]]` header
and a node if it sits under a `[[…nodes]]` one. Only the channel bindings
change; a node selecting the `claude` or `codex_exec` **effect** stays as it
is.

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
name, so a rename is a TOML edit rather than a `mv`:

```bash
CONFIG_HOME="${PLECT_CONFIG_HOME:-$HOME/.config/plect}"
grep -rhoP '^\[\K[A-Za-z_][A-Za-z0-9_]*(?=\])' "$CONFIG_HOME" --include='*.toml' \
  | sort | uniq -d
```

Anything that prints is declared more than once in the global layer. (Run it
per plugin directory too if you author your own plugin.)

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
- **Every shipped invocation.** The golden invocation records regenerate
  byte-identical apart from the two renamed channel labels.

## `plect tick` and `plect check` no longer report warnings

The only config-level warning either surfaced was the surviving `chains/*.toml`
notice, which is now a load error instead. `--json` output no longer carries a
`warnings` key, and nothing is printed to stderr in its place. A script reading
`.warnings` selects nothing rather than failing; a script that treated a
non-empty `.warnings` as a signal has one fewer thing to check.
