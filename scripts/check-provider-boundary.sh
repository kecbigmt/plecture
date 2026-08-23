#!/usr/bin/env bash
# Fails if app/ (excluding most tests — see the scope decision below) or
# contracts/ reference a specific external provider by name or assume its
# identifier shape. Core owns the durable structure of work; a provider
# commitment belongs in plugins/ instead.
#
# The provider-name half of the vocabulary is derived from every shipped
# plugin's plugin.toml (its directory id, plus its `[[executables]]` names)
# via app/cmd/check-provider-boundary, rather than hand-kept here — a
# provider's name is decoded from structured config once, not re-typed into
# this script's own list every time a plugin ships a new one.
#
# Scope decision: an "owner/repo"-shaped identifier is treated as leakage,
# not as plect's own vocabulary. Session/resource names are opaque strings
# to core (contracts/event's own doc comment says as much) — an "owner/repo"
# example is always borrowing a specific hosting provider's naming
# convention, so it is caught alongside literal provider names.
#
# Scope decision: *_test.go under app/ is NOT scanned in general, except for
# app/internal/channel — a package a prior review found hardcoding shipped
# plugin names in a test — which is scanned like any other core file.
# Widening the file set to *all* of app/'s tests surfaces on the order of a
# thousand pre-existing lines across most of the module's test suite — not
# an isolated leak like that one, but two much larger, pre-existing
# conventions: (1) shipped plugin ids/names used throughout generic
# catalog/resolver/service tests as arbitrary placeholder data, and (2) an
# "owner/repo-N"-shaped session name used almost everywhere as the default
# example session. Fixing that is a repo-wide test-fixture migration, not a
# boundary-checker change, and is tracked separately rather than folded
# into this script's default scope. contracts/*_test.go stays in scope
# too: that module is small enough that its few violations were fixed
# outright instead of deferred.
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
app_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")/../app" && pwd)"
plugins_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")/../plugins" && pwd)"

root="${BOUNDARY_CHECK_ROOT:-$(pwd)}"
cd "$root"

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
if ! vocab_words="$(cd "$app_dir" && go run ./cmd/check-provider-boundary "$plugins_dir")"; then
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
while IFS= read -r f; do
  [ -z "$f" ] && continue
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
done < <({ find app -name '*.go' -not -name '*_test.go'; find app/internal/channel -name '*_test.go'; find contracts -name '*.go'; } | sort)

if [ "$fail" -ne 0 ]; then
  echo
  echo "Provider-neutrality boundary violated: core (app/, contracts/) must not" >&2
  echo "name a specific provider or assume its identifier shape. Move the" >&2
  echo "reference to plugins/, or allowlist a genuine exception with a" >&2
  echo "trailing '// boundary-allow: <reason>' comment." >&2
  exit 1
fi

echo "boundary check passed ($count files scanned)"
