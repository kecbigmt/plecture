#!/usr/bin/env bash
# Proves lint-tools.sh refuses a shellcheck tarball whose bytes do not match
# the pinned checksum, and installs nothing in that case.
set -euo pipefail

root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"

work="$(mktemp -d)"
trap 'rm -rf "$work"' EXIT

# shellcheck source=lint-tools.sh
. "$root/scripts/lint-tools.sh"
platform="$(uname -s | tr '[:upper:]' '[:lower:]').$(uname -m)"
platform="${platform/arm64/aarch64}"

mkdir -p "$work/release/$SHELLCHECK_VERSION" "$work/payload/shellcheck-$SHELLCHECK_VERSION"
printf '#!/bin/sh\necho tampered\n' > "$work/payload/shellcheck-$SHELLCHECK_VERSION/shellcheck"
chmod +x "$work/payload/shellcheck-$SHELLCHECK_VERSION/shellcheck"
tar -cJf "$work/release/$SHELLCHECK_VERSION/shellcheck-$SHELLCHECK_VERSION.$platform.tar.xz" \
  -C "$work/payload" "shellcheck-$SHELLCHECK_VERSION"

if PLECTURE_LINT_TOOLS_DIR="$work/tools" SHELLCHECK_RELEASE_BASE="file://$work/release" \
  lint_tool shellcheck > "$work/log" 2>&1; then
  echo "FAIL: a tarball that does not match the pinned checksum was installed" >&2
  cat "$work/log" >&2
  exit 1
fi
if ! grep -qF "checksum mismatch" "$work/log"; then
  echo "FAIL: the refusal does not name the checksum mismatch" >&2
  cat "$work/log" >&2
  exit 1
fi
if [ -e "$work/tools/shellcheck@$SHELLCHECK_VERSION" ]; then
  echo "FAIL: a refused install left a tool directory behind" >&2
  exit 1
fi
echo "ok: a tarball that does not match the pinned checksum is refused"
