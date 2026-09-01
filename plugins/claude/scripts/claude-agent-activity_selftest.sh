#!/usr/bin/env bash
set -euo pipefail

script_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
subject="$script_dir/claude-agent-activity"
tmp="$(mktemp -d)"
trap 'rm -rf "$tmp"' EXIT

bin_dir="$tmp/bin"
mkdir -p "$bin_dir"
cat > "$bin_dir/plect" <<'EOF'
#!/usr/bin/env bash
printf '%s\n' "$*" >> "$PLECT_CALLS"
EOF
chmod +x "$bin_dir/plect"

run_hook() {
  local payload="$1"
  PLECT_SESSION_NAME="owner/repo-1" \
  PLECT_CALLS="$tmp/calls" \
  XDG_STATE_HOME="$tmp/state" \
  PATH="$bin_dir:$PATH" \
  "$subject" working <<<"$payload"
  tail -n 1 "$tmp/calls"
}

got="$(run_hook '{"hook_event_name":"PreToolUse","tool_name":"Bash","tool_input":{"command":"go test ./app/... --token secret"}}')"
want='state set-message owner/repo-1 working: Bash go'
[ "$got" = "$want" ] || { printf 'Bash text = %q, want %q\n' "$got" "$want" >&2; exit 1; }

got="$(run_hook '{"hook_event_name":"PreToolUse","tool_name":"Edit","tool_input":{"file_path":"/tmp/private/runtime.toml"}}')"
want='state set-message owner/repo-1 working: Edit runtime.toml'
[ "$got" = "$want" ] || { printf 'Edit text = %q, want %q\n' "$got" "$want" >&2; exit 1; }

got="$(run_hook '{"hook_event_name":"PreToolUse","tool_name":"Read","tool_input":{"file_path":"/tmp/private/with space.txt"}}')"
want='state set-message owner/repo-1 working: Read with space.txt'
[ "$got" = "$want" ] || { printf 'Read text = %q, want %q\n' "$got" "$want" >&2; exit 1; }

got="$(run_hook '{"hook_event_name":"PreToolUse","tool_name":"Bash","tool_input":{"command":"/usr/local/bin/deploy --password hunter2"}}')"
case "$got" in
  *password*|*hunter2*) printf 'Bash text leaked command details: %s\n' "$got" >&2; exit 1 ;;
esac
want='state set-message owner/repo-1 working: Bash /usr/local/bin/deploy'
[ "$got" = "$want" ] || { printf 'Bash path command text = %q, want %q\n' "$got" "$want" >&2; exit 1; }

long_name="very-long-generated-file-name-that-would-overflow-the-slack-status-line-runtime.toml"
got="$(run_hook "{\"hook_event_name\":\"PreToolUse\",\"tool_name\":\"Edit\",\"tool_input\":{\"file_path\":\"/tmp/private/$long_name\"}}")"
text="${got#state set-message owner/repo-1 }"
[ "${#text}" -le 80 ] || { printf 'status text length = %d, want <=80: %s\n' "${#text}" "$text" >&2; exit 1; }

got="$(run_hook '{"hook_event_name":"UserPromptSubmit"}')"
want='state set-message owner/repo-1 working'
[ "$got" = "$want" ] || { printf 'UserPromptSubmit text = %q, want %q\n' "$got" "$want" >&2; exit 1; }

# The Stop hook's waiting phase clears the message rather than reporting the
# literal word "waiting" as if it were a current activity.
PLECT_SESSION_NAME="owner/repo-1" \
PLECT_CALLS="$tmp/calls" \
XDG_STATE_HOME="$tmp/state" \
PATH="$bin_dir:$PATH" \
"$subject" waiting <<<'{"hook_event_name":"Stop"}'
got="$(tail -n 1 "$tmp/calls")"
want='state set-message owner/repo-1 '
[ "$got" = "$want" ] || { printf 'Stop text = %q, want %q\n' "$got" "$want" >&2; exit 1; }
