#!/usr/bin/env bash
set -euo pipefail

here="$(cd "$(dirname "$0")" && pwd)"
bin="$here/codex-mcp-servers"
fail=0

expect_out() {
  local label="$1" records="$2" want="$3" got
  if ! got=$(CODEX_MCP_SERVERS="$records" "$bin" 2>&1); then
    printf 'FAIL %s: exited non-zero: %s\n' "$label" "$got" >&2
    fail=1
  elif [ "$got" != "$want" ]; then
    printf 'FAIL %s:\n  got  %s\n  want %s\n' "$label" "$got" "$want" >&2
    fail=1
  else
    echo "ok   $label"
  fi
}

expect_err() {
  local label="$1" records="$2" want="$3" got
  if got=$(CODEX_MCP_SERVERS="$records" "$bin" 2>&1); then
    printf 'FAIL %s: succeeded with %s\n' "$label" "$got" >&2
    fail=1
  elif [[ "$got" != *"$want"* ]]; then
    printf 'FAIL %s: error %q lacks %q\n' "$label" "$got" "$want" >&2
    fail=1
  else
    echo "ok   $label"
  fi
}

expect_out "unset yields no overrides" "" '[]'
expect_out "empty array yields no overrides" '[]' '[]'
expect_out "a command-only record becomes one table override" \
  '[{"name":"x","command":"stub-mcp"}]' \
  '["mcp_servers.x={command = \"stub-mcp\"}"]'
# The claude plugin README's own example record, copied unchanged.
expect_out "a record in the claude plugin's shape is accepted unchanged" \
  '[{"name":"kbn","command":"kbn-mcp","args":["--scoped"]}]' \
  '["mcp_servers.kbn={command = \"kbn-mcp\", args = [\"--scoped\"]}"]'
expect_out "each record becomes its own override, in order" \
  '[{"name":"a","command":"one"},{"name":"b_2-x","command":"two","env":{}}]' \
  '["mcp_servers.a={command = \"one\"}","mcp_servers.b_2-x={command = \"two\", env = {}}"]'

# Spaces, both quote kinds, a backslash, a newline, and DEL must survive as
# escapes inside one TOML value rather than becoming separate tokens.
records='[{"name":"q","command":"stub mcp","args":["a b","c\"d","it'"'"'s","back\\slash"],"env":{"K 1":"v \"q\"\nx","K2":"y\u007f"}}]'
want='["mcp_servers.q={command = \"stub mcp\", args = [\"a b\",\"c\\\"d\",\"it'"'"'s\",\"back\\\\slash\"], env = {\"K 1\" = \"v \\\"q\\\"\\nx\", \"K2\" = \"y\\u007f\"}}"]'
expect_out "spaces and quotes stay inside one escaped TOML value" "$records" "$want"

expect_err "invalid JSON fails" '[{' 'mcp_servers is not valid JSON'
expect_err "a non-array fails" '{"name":"x","command":"y"}' 'must be a JSON array'
expect_err "a non-object record fails" '["x"]' 'record at index 0 is not an object'
expect_err "a missing name fails" '[{"command":"y"}]' 'at index 0 is missing required field "name"'
expect_err "an empty name fails" '[{"name":"","command":"y"}]' 'field "name" must be a non-empty string'
expect_err "a dotted name fails" '[{"name":"a.b","command":"y"}]' 'record "a.b" field "name" must contain only'
# jq's ^ and $ also match at an embedded line break, which would let a
# newline split one override into two argv lines in the worker.
expect_err "a name with a trailing newline fails" '[{"name":"x\n","command":"y"}]' 'field "name" must contain only'
expect_err "a name with an embedded newline fails" '[{"name":"a\nb","command":"y"}]' 'field "name" must contain only'
expect_err "a missing command fails" '[{"name":"x"}]' 'record "x" is missing required field "command"'
expect_err "an empty command fails" '[{"name":"x","command":""}]' 'field "command" must be a non-empty string'
expect_err "an unknown field fails" '[{"name":"x","command":"y","cwd":"/"}]' 'declares unsupported field "cwd"'
expect_err "non-string args fail" '[{"name":"x","command":"y","args":[1]}]' 'field "args" must be an array of strings'
expect_err "non-object env fails" '[{"name":"x","command":"y","env":["A=1"]}]' 'field "env" must be an object of strings'
expect_err "non-string env values fail" '[{"name":"x","command":"y","env":{"A":1}}]' 'field "env" must be an object of strings'
expect_err "a repeated name fails" '[{"name":"x","command":"y"},{"name":"x","command":"z"}]' 'record "x" repeats a name'

[ "$fail" -eq 0 ] || { echo "codex-mcp-servers selftest failed" >&2; exit 1; }
echo "codex-mcp-servers selftest passed"
