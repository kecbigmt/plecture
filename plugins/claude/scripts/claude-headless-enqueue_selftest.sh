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
enqueue="$here/claude-headless-enqueue"
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

queued_count() {
  find "$1" -maxdepth 1 -name '*.json' | wc -l | tr -d ' '
}

q="$tmp/coalesce"
"$enqueue" "$q" "$envelope" "github.ci_status" "pending" "" "https://example.test/pr/1"
"$enqueue" "$q" "$envelope" "github.ci_status" "running" "" "https://example.test/pr/1"
"$enqueue" "$q" "$envelope" "github.ci_status" "success" "" "https://example.test/pr/1"
check "repeated github.* for one (type, url) leaves one file" "1" "$(queued_count "$q")"
check "the surviving file carries the newest text" \
  "[github.ci_status] success (https://example.test/pr/1)" "$(queued_field "$q" text)"
check "the url is stored in the queue file" \
  "https://example.test/pr/1" "$(queued_field "$q" url)"

q="$tmp/other-url"
"$enqueue" "$q" "$envelope" "github.ci_status" "a" "" "https://example.test/pr/1"
"$enqueue" "$q" "$envelope" "github.ci_status" "b" "" "https://example.test/issues/1"
check "different urls are never coalesced" "2" "$(queued_count "$q")"

q="$tmp/other-type"
"$enqueue" "$q" "$envelope" "github.ci_status" "a" "" "https://example.test/pr/1"
"$enqueue" "$q" "$envelope" "github.mergeable" "b" "" "https://example.test/pr/1"
check "different types are never coalesced" "2" "$(queued_count "$q")"

q="$tmp/user-emit"
for _ in 1 2 3; do
  "$enqueue" "$q" "$envelope" "user.emit" "same text" "" "https://example.test/pr/1"
done
check "user.emit is never coalesced" "3" "$(queued_count "$q")"

q="$tmp/legacy"
mkdir -p "$q"
jq -n '{type: "github.ci_status", text: "legacy"}' > "$q/0-legacy.json"
"$enqueue" "$q" "$envelope" "github.ci_status" "new" "" "https://example.test/pr/1"
check "a file without url is not coalesced" "2" "$(queued_count "$q")"
check "the legacy file itself is still queued" \
  "legacy" "$(jq -r '.text' "$q/0-legacy.json")"

q="$tmp/no-url"
"$enqueue" "$q" "$envelope" "github.ci_status" "a" "" ""
"$enqueue" "$q" "$envelope" "github.ci_status" "b" "" ""
check "github.* with an empty url is not coalesced" "2" "$(queued_count "$q")"

[ "$fail" -eq 0 ] || { echo "enqueue selftest failed" >&2; exit 1; }
echo "enqueue selftest passed"
