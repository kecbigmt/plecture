#!/usr/bin/env bash
# Proves check-shellcheck.sh against seeded defects in a fixture repository:
# a warning fails and is named in a `.sh` file and in an extension-less
# script found by its shebang, and every suppression directive fails and is
# named, while directive-shaped text that is not a directive passes.
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
  "tool.sh:2:1: SC2034 (warning)" "https://www.shellcheck.net/wiki/SC2034" "This check takes no exception"

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
expect_fail_naming "a suppression directive fails and names its line" "bin/tool:3: suppression directive"

script bin/tool <<'EOF'
echo start; echo "# shellcheck disable=SC1000"; # shellcheck disable=SC2046
echo $(ls)
EOF
expect_fail_naming "a directive after a command and a quoted look-alike is still found" "bin/tool:2: suppression directive"

script bin/tool <<'EOF'
echo start
cat <<'TEXT'
# shellcheck disable=SC2046
TEXT
EOF
expect_pass "directive-shaped text inside a heredoc is not a directive"

printf 'disable=SC2046\n' > "$fixture/.shellcheckrc"
expect_fail_naming "a tracked .shellcheckrc fails" ".shellcheckrc:1: shellcheck configuration file"
