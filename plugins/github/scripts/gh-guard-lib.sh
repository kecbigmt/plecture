# gh-guard-lib.sh: the merge/close denial check shared by the auth-agnostic
# `gh-guard` wrapper and `gh-app-guard`'s composite auth+guard wrapper. One
# implementation, sourced in-process by whichever wrapper is on PATH — never
# exec'd as a nested shim, so a session composing gh_app_guard gets exactly
# one process hop between it and the real gh, with auth and deny ordering
# both visible in that one wrapper's own source.
#
# Contract for a sourcing script:
#   1. Call `gh_guard_check_denied "$@"`. A zero return means denied — call
#      `gh_guard_deny` (it prints the message and exits).
#   2. A `gh api --input -` call whose body arrived on this process's own
#      stdin is buffered to $GH_GUARD_STDIN_CAPTURE (empty when nothing was
#      buffered) so the caller can still replay it to the real gh — stdin is
#      a single-read stream, and the scan above already consumed it once.
#   3. The sourcing script's own EXIT trap must `rm -f
#      "$GH_GUARD_STDIN_CAPTURE"` so a denied call (which never reaches the
#      replay step) doesn't leak the temp file.

GH_GUARD_STDIN_CAPTURE=""

gh_guard_deny() {
  echo "This session does not merge/close. Record your result (verdict / PR ready state); merging is the orchestrator's job." >&2
  exit 1
}

# A --input body is a whole JSON document, not a fixed literal — matching
# specific substrings (compact "state":"closed", one particular spacing)
# chases formatting forever and always has another gap. jq -e '.state ==
# "closed"' instead parses the document and checks the actual field, so any
# whitespace/formatting of an equivalent body is caught the same way. The
# grep fallback only covers jq being unavailable; every task setup in this
# catalog already assumes jq is on PATH, so that path is a defensive
# minimum, not the primary check.
gh_guard_body_is_closed() {
  local content_file="$1"
  if command -v jq >/dev/null 2>&1 && jq -e '(.state // empty) == "closed"' "$content_file" >/dev/null 2>&1; then
    return 0
  fi
  grep -q -e 'state=closed' -e '"state":"closed"' -e '"state": "closed"' "$content_file"
}

# gh_guard_check_denied inspects this invocation's gh argv ("$@") and,
# for `gh api --input -`, this process's own stdin. Returns 0 (true, in
# shell-exit-status terms) when the call must be denied, 1 (false) when it
# may proceed.
gh_guard_check_denied() {
  local sub="${1:-}"
  local action="${2:-}"

  case "$sub $action" in
    "pr merge" | "issue close" | "pr close")
      return 0
      ;;
  esac

  [ "$sub" = "api" ] || return 1

  local method="GET" path="" body_has_closed=0 input_value="" prev="" arg
  for arg in "$@"; do
    # --method/-X takes a value three ways: split ("-X" "PUT"), joined with
    # "=" ("--method=PUT"), or glued to the short flag ("-XPUT") — pflag
    # (gh's flag parser) accepts all three, so all three must be recognized
    # here or one of them silently walks past the deny check below. --input
    # has no glued short form (gh defines no short flag for it), so only
    # split and joined need recognizing.
    case "$prev" in
      -X | --method)
        method="$arg"
        ;;
      --input)
        input_value="$arg"
        ;;
    esac
    case "$arg" in
      --method=*)
        method="${arg#--method=}"
        ;;
      -X?*)
        method="${arg#-X}"
        ;;
      --input=*)
        input_value="${arg#--input=}"
        ;;
    esac
    case "$arg" in
      */merge)
        [ -z "$path" ] && path="$arg"
        ;;
    esac
    case "$arg" in
      *state=closed* | *'"state":"closed"'* | *'"state": "closed"'*)
        body_has_closed=1
        ;;
    esac
    prev="$arg"
  done
  method=$(printf '%s' "$method" | tr '[:lower:]' '[:upper:]')

  # --input replaces the whole request body from a file or stdin (per `gh api
  # --help`), which the argv scan above never sees — a state:closed body
  # delivered this way must be inspected from its actual source instead.
  if [ -n "$input_value" ] && [ "$body_has_closed" != "1" ]; then
    if [ "$input_value" = "-" ]; then
      # Stdin is a single-read stream: buffer it to a temp file so it can be
      # inspected here and still replayed to the real gh by the sourcing
      # script — reading it once with no buffer would leave the real call
      # with an already-drained stdin.
      GH_GUARD_STDIN_CAPTURE=$(mktemp "${TMPDIR:-/tmp}/gh-guard-stdin.XXXXXX")
      cat > "$GH_GUARD_STDIN_CAPTURE"
      gh_guard_body_is_closed "$GH_GUARD_STDIN_CAPTURE" && body_has_closed=1
    elif [ -r "$input_value" ]; then
      gh_guard_body_is_closed "$input_value" && body_has_closed=1
    fi
  fi

  if [ "$method" = "PUT" ] && [ -n "$path" ]; then
    return 0
  fi
  if [ "$method" = "PATCH" ] && [ "$body_has_closed" = "1" ]; then
    return 0
  fi
  return 1
}
