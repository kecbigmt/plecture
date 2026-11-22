#!/usr/bin/env bash
# Proves check-shellcheck.sh against seeded defects in a fixture repository:
# a warning fails and is named in a `.sh` file and in an extension-less
# script found by its shebang, and an exception passes only with a reason,
# only on one command, and only while it still suppresses something.
set -euo pipefail

root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
checker="$root/scripts/check-shellcheck.sh"

fixture="$(mktemp -d)"
log="$fixture.log"
trap 'rm -rf "$fixture" "$log"' EXIT
git -C "$fixture" init -q

run_check() {
  git -C "$fixture" add -A
  SHELLCHECK_CHECK_ROOT="$fixture" "$checker" > "$log" 2>&1
}

expect_pass() {
  if ! run_check; then
    echo "FAIL: $1: checker failed" >&2
    cat "$log" >&2
    exit 1
  fi
  echo "ok: $1"
}

expect_fail_naming() {
  local name="$1"
  shift
  if run_check; then
    echo "FAIL: $name: checker passed" >&2
    cat "$log" >&2
    exit 1
  fi
  local want
  for want in "$@"; do
    if ! grep -qF -- "$want" "$log"; then
      echo "FAIL: $name: output does not name '$want'" >&2
      cat "$log" >&2
      exit 1
    fi
  done
  echo "ok: $name"
}

# script <path> writes a bash script whose body comes from stdin.
script() {
  mkdir -p "$(dirname "$fixture/$1")"
  { echo '#!/usr/bin/env bash'; cat; } > "$fixture/$1"
}

script tool.sh <<'EOF'
echo "$1"
EOF
expect_pass "a clean script passes"

script tool.sh <<'EOF'
unused=1
EOF
expect_fail_naming "a warning in a .sh file fails and is named" \
  "tool.sh:2:1: SC2034 (warning)" "https://www.shellcheck.net/wiki/SC2034"

script tool.sh <<'EOF'
echo "$1"
EOF
script bin/tool <<'EOF'
echo $(ls)
EOF
expect_fail_naming "a warning in an extension-less script is found by its shebang" "bin/tool:2:6: SC2046"

script bin/tool <<'EOF'
echo start
# shellcheck disable=SC2046 # each word is a separate argument on purpose
echo $(ls)
EOF
expect_pass "an exception with a reason that suppresses a finding passes"

script bin/tool <<'EOF'
echo start
# shellcheck disable=SC2046
echo $(ls)
EOF
expect_fail_naming "an exception without a reason fails" "bin/tool:3: exception has no reason"

script bin/tool <<'EOF'
echo start
# shellcheck disable=SC2046 # nothing left to excuse
echo "$(ls)"
EOF
expect_fail_naming "an exception that suppresses nothing fails" "bin/tool:3: exception for SC2046 suppresses nothing"

script bin/tool <<'EOF'
# shellcheck disable=SC2046 # whole file
echo $(ls)
EOF
expect_fail_naming "an exception before the first command fails as file-wide" "bin/tool:2: exception before the first command"

script bin/tool <<'EOF'
echo start
# shellcheck disable=all # everything
echo $(ls)
EOF
expect_fail_naming "an exception naming no specific code fails" "bin/tool:3: exception names all"

script bin/tool <<'EOF'
echo start
cat <<'TEXT'
# shellcheck disable=SC2046
TEXT
EOF
expect_pass "directive-shaped text inside a heredoc is not taken for an exception"

script bin/tool <<'EOF'
echo start
# shellcheck disable=SC2034 # values retained for debugging
f() {
  unused1=1
  unused2=2
}
f
EOF
expect_fail_naming "an exception over a function body fails as broader than one line" \
  "bin/tool:3: exception covers"

script bin/tool <<'EOF'
echo start
# shellcheck disable=SC2034 # value retained for debugging
unused=1
EOF
mkdir -p "$fixture/nested/bin"
cat > "$fixture/nested/bin/lib.inc" <<'EOF'
arr=(a b)
printf '%s\n' "${arr[@]}"
EOF
script nested/bin/tool.sh <<'EOF'
# shellcheck source=lib.inc
. "$(dirname "$0")/lib.inc"
# shellcheck disable=SC2128 # only the first item is wanted
echo "$arr"
EOF
expect_pass "a live exception whose finding needs a sibling sourced file passes"

script bin/tool <<'EOF'
echo start
# shellcheck disable=SC2046 # words split on purpose
case "$1" in
  a) echo $(ls) ;;
  b) echo $(pwd) ;;
esac
EOF
expect_fail_naming "an exception over a case block fails" "bin/tool:3: exception covers"

script bin/tool <<'EOF'
echo start
# shellcheck disable=SC2034 # values retained for debugging
f()
{
  unused1=1
  unused2=2
}
f
EOF
expect_fail_naming "an exception over a function whose brace opens on the next line fails" "bin/tool:3: exception covers"

script bin/tool <<'EOF'
echo start; # shellcheck disable=SC2046
f() {
  echo $(ls)
  echo $(pwd)
}
f
EOF
expect_fail_naming "a directive after a command on the same line is validated too" \
  "bin/tool:2: exception has no reason" "bin/tool:2: exception covers"
