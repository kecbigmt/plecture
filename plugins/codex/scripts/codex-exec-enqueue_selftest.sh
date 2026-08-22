#!/usr/bin/env bash
# Proves the script-side half of this plugin's enqueue contract: given the
# six positional arguments its channel delivers, the message_envelope is
# expanded against exactly those fields, by parameter replacement and never
# by eval.
#
# The argv side — that the channel actually passes those fields in that
# order — is pinned by testdata/channel-invocations.txt, which is regenerated
# from the shipped declaration. Neither half alone connects the declaration
# to what the worker reads: this one would still pass if the channel swapped
# two arguments, and that one would still pass if the expansion broke.
set -euo pipefail

here="$(cd "$(dirname "$0")" && pwd)"
enqueue="$here/codex-exec-enqueue"
tmp="$(mktemp -d)"
trap 'rm -rf "$tmp"' EXIT

fail=0
check() {
  local label="$1" want="$2" got="$3"
  if [ "$got" != "$want" ]; then
    echo "FAIL $label: got '$got', want '$want'" >&2
    fail=1
  else
    echo "ok   $label"
  fi
}

# queued <queue_dir> prints the single queued file's field, oldest-first is
# irrelevant with one entry.
queued_field() {
  local dir="$1" field="$2"
  local file
  file="$(find "$dir" -maxdepth 1 -name '*.json' | head -n 1)"
  jq -r ".$field" "$file"
}

envelope='[{type}] {body_or_summary}{url_suffix}'

# A body wins over the summary, and a url in metadata is appended.
q="$tmp/body"
"$enqueue" "$q" "$envelope" "user.emit" "do the thing" "fallback summary" "https://example.test/x"
check "type is carried" "user.emit" "$(queued_field "$q" type)"
check "body wins and the url is appended" \
  "[user.emit] do the thing (https://example.test/x)" "$(queued_field "$q" text)"

# An empty body falls back to the summary, and an empty url appends nothing.
q="$tmp/summary"
"$enqueue" "$q" "$envelope" "example.status" "" "only a summary" ""
check "summary fallback with no url suffix" \
  "[example.status] only a summary" "$(queued_field "$q" text)"

# The envelope chooses the shape and may reorder or relabel, but reaches only
# the fields the channel offers.
q="$tmp/custom"
"$enqueue" "$q" "{summary} -- {type}" "example.status" "b" "s" ""
check "a custom envelope is expanded, not evaluated" \
  "s -- example.status" "$(queued_field "$q" text)"

# An envelope naming no placeholder is passed through verbatim; substitution
# is parameter replacement, never eval.
q="$tmp/literal"
"$enqueue" "$q" 'literal $(echo pwned) `echo pwned`' "example.status" "" "s" ""
check "an envelope is never evaluated as shell" \
  'literal $(echo pwned) `echo pwned`' "$(queued_field "$q" text)"

[ "$fail" -eq 0 ] || { echo "enqueue selftest failed" >&2; exit 1; }
echo "enqueue selftest passed"
