#!/usr/bin/env bash
# Fails if app/ or contracts/ reference a specific external provider by
# name or assume its identifier shape. Core owns the durable structure of
# work; a provider commitment belongs in plugins/ instead. Every *.go file
# under app/ and contracts/ is scanned, tests included — see the allowlist
# note below for the one carve-out.
#
# The provider-name half of the vocabulary is derived from every published
# plugin's plugin.toml (its directory id, plus its `[[executables]]` names)
# via scripts/provider-vocab.py, rather than hand-kept here — a provider's
# name is decoded from structured config once, not re-typed into this
# script's own list every time a plugin ships a new one. That derivation is
# a stdlib-only Python repo script rather than a new Go package under
# app/ or contracts/: this checker is itself repo tooling, not core, and
# should not grow a core-Go footprint of its own.
#
# Scope decision: an "owner/repo"-shaped identifier is treated as leakage,
# not as plect's own vocabulary. Session/resource names are opaque strings
# to core (contracts/event's own doc comment says as much) — an "owner/repo"
# example is always borrowing a specific hosting provider's naming
# convention, so it is caught alongside literal provider names.
#
# Allowlist: scripts/check-provider-boundary-test-allowlist.txt names test
# files exempted from scanning because they predate it and use a
# pre-existing, unrelated test-fixture convention (see that file's own
# header). Every non-test file, and every test file not on that list, is
# scanned in full — the allowlist is an explicit, enumerated, shrinking
# exception list, not a directory-wide carve-out.
#
# A line that genuinely needs to keep a provider token can allowlist itself
# with a trailing "// boundary-allow: <reason>" comment.
set -euo pipefail

# The vocabulary source is this script's own repo (the shipped plugins that
# define what "a provider" means here), independent of $root below — which
# is the tree of Go files being scanned and, under the selftest, a throwaway
# fixture with no plugins/ directory of its own. Resolved before the `cd`
# below, and from ${BASH_SOURCE[0]} rather than $0, so it is correct
# regardless of the caller's cwd or whether the script was invoked by a
# relative or absolute path.
vocab_script="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/provider-vocab.py"
plugins_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")/../plugins" && pwd)"
allowlist_file="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/check-provider-boundary-test-allowlist.txt"

root="${BOUNDARY_CHECK_ROOT:-$(pwd)}"
cd "$root"

declare -A allowlisted=()
while IFS= read -r line; do
  case "$line" in
    ''|'#'*) continue ;;
  esac
  allowlisted["$line"]=1
done < "$allowlist_file"

# Each alternative targets a distinct leak:
#   - a shipped plugin's id or executable name (derived above) — boundaried
#     on a non-alnum-non-underscore character on each side, treating "."
#     as a valid boundary (not excluded from it) so a dotted event-type-style
#     token like "slack.message" or "claude.reply" is still caught. The one
#     place that costs something is a Go import path ("github.com/..."),
#     handled below by an explicit skip case rather than by narrowing every
#     word's boundary and going blind to the dotted form instead;
#   - "gh" and "pvti", GitHub-CLI/API vocabulary that names no plugin
#     executable but is still a GitHub-specific token;
#   - a session/resource-id EXAMPLE shaped like a GitHub-style "owner/repo"
#     slug — plect's own session names are opaque strings core never
#     interprets (see contracts/event doc comment), so a literal "owner/repo"
#     or "<owner>/<repo>" placeholder in core is always a leaked assumption
#     about a specific host's naming convention, never core's own vocabulary.
# This intentionally does NOT flag every "word/word" or "word/word-N"
# string (e.g. Go import paths, generic path components) — only the
# owner/repo placeholder shape itself and identifiers that look like a
# resolved instance of it (an alnum segment, "/", an alnum segment, "-N").
if ! vocab_words="$(python3 "$vocab_script" "$plugins_dir")"; then
  echo "failed to derive the provider-name vocabulary from $plugins_dir" >&2
  exit 1
fi

vocab_pattern=""
while IFS= read -r word; do
  [ -z "$word" ] && continue
  escaped=$(printf '%s' "$word" | sed -E 's/[.[\*^$()+?{|\\]/\\&/g')
  alt="(^|[^A-Za-z0-9_])${escaped}([^A-Za-z0-9_]|\$)"
  if [ -z "$vocab_pattern" ]; then
    vocab_pattern="$alt"
  else
    vocab_pattern="$vocab_pattern|$alt"
  fi
done <<< "$vocab_words"

pattern="$vocab_pattern|(^|[^A-Za-z0-9_])gh([^A-Za-z0-9_]|\$)|pvti|<?[Oo]wner>?/<?[Rr]epo>?|[A-Za-z0-9][A-Za-z0-9_.-]*/[A-Za-z0-9][A-Za-z0-9_.-]*-[0-9]+"

fail=0
count=0
skipped=0
while IFS= read -r f; do
  [ -z "$f" ] && continue
  if [ -n "${allowlisted[$f]+x}" ]; then
    skipped=$((skipped + 1))
    continue
  fi
  count=$((count + 1))
  while IFS=: read -r lineno content; do
    [ -z "$lineno" ] && continue
    case "$content" in
      *boundary-allow:*) continue ;;
      *github.com/*) continue ;; # Go module import path, not a provider reference
    esac
    echo "$f:$lineno: $content"
    fail=1
  done < <(grep -niE "$pattern" "$f" || true)
done < <({ find app -name '*.go'; find contracts -name '*.go'; } | sort)

if [ "$fail" -ne 0 ]; then
  echo
  echo "Provider-neutrality boundary violated: core (app/, contracts/) must not" >&2
  echo "name a specific provider or assume its identifier shape. Move the" >&2
  echo "reference to plugins/, or allowlist a genuine exception with a" >&2
  echo "trailing '// boundary-allow: <reason>' comment." >&2
  exit 1
fi

echo "boundary check passed ($count files scanned, $skipped allowlisted)"
