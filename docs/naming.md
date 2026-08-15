# Naming

Plecture uses a two-tier name, the same pattern as Kubernetes/`kubectl` or
Mercurial/`hg`: a proper noun for prose about the project, and a short,
lowercase name for everything code-facing. This document is the decision
record for which one to use where. It does not cover how the names were
chosen — only how to use them correctly.

## Decision table

| Context | Name |
|---|---|
| Prose that refers to the project or product (README headings, ADRs, discussion) | **Plecture** — proper noun, capitalized, never appears in code |
| Commands, binaries, and everything execution-facing (`plect`, `plect-web`, config paths, `PLECT_*` environment variables, the module path, state) | **plect** — always the code-facing resident |
| The repository name | **plecture** |
| The web UI | **Plecture Web UI** — the binary name `plect-web` does not change |
| The domain | plect.dev |

## Rules

- **Determine by audience, not by habit.** If the sentence is about the
  project as a thing — what it is, what it's for, who owns it — use
  Plecture. If the sentence is about something a reader will type, run, or
  configure — a command, a flag, a path, an environment variable, a module
  import — use plect.
- **No intermediate forms.** Never capitalize the first letter of `plect`
  when writing about the tool, and never follow "plecture" with a
  code-facing noun such as "command," "CLI," or "binary." Mixed forms like
  that are the single biggest source of reader confusion. Code identifiers,
  command examples, and literal paths are exempt — do not rewrite those to
  chase this rule.
- **The web UI belongs to Plecture, not to the CLI.** Refer to it in prose
  as "Plecture Web UI" — another entry point into Plecture, not an add-on
  bundled with the `plect` command. Its binary name (`plect-web`) is
  code-facing and follows the code-facing rule above.
- **Japanese prose fixes on プレクチャー.** Use this transliteration
  consistently instead of alternatives.

## What the name means

Plecture (プレクチャー, /ˈplɛktʃər/) is a coined word, built from the Latin
verb *plectere* — to weave, to braid, to entwine — the same pattern that
turned *texere* (to weave) into *texture*. The word Plecture does not
otherwise exist in classical Latin; the actual Latin derivation of
*plectere* is *plexura* (English: plexure). Describe Plecture as "coined
from *plectere*," never as "the Latin word for..." or as an attested Latin
noun.

The `-ture` ending is deliberate: it echoes *structure* and *architecture*.
Paired with the *plectere* root, the name points at what Plecture provides
— the structure within which autonomous work can keep moving.

The image the name carries: within a woven structure, each strand stays
identifiable rather than dissolving into the whole. Humans stay human,
agents stay agents — Plecture connects them without replacing them.

`plect` is not a nickname, an abbreviation notice, or a placeholder — it is
the CLI's formal name, exactly as `hg` is Mercurial's and `rg` is
ripgrep's.
