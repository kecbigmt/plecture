#!/usr/bin/env bash
# Proves lint-tools.sh refuses a shellcheck tarball whose bytes do not match
# the pinned checksum, and installs nothing in that case; and that the yq it
# provides is the pinned one whatever yq is on PATH, or a failure naming the
# pinned version.
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

mkdir -p "$work/path"
printf '#!/bin/sh\necho "yq 3.4.3"\n' > "$work/path/yq"
chmod +x "$work/path/yq"
if ! yq="$(PATH="$work/path:$PATH" lint_tool yq 2> "$work/log")"; then
  echo "FAIL: the pinned yq did not install" >&2
  cat "$work/log" >&2
  exit 1
fi
if ! "$yq" --version | grep -qF "mikefarah/yq/) version $YQ_VERSION"; then
  echo "FAIL: lint_tool yq is not mikefarah yq $YQ_VERSION: $("$yq" --version)" >&2
  exit 1
fi
echo "ok: the pinned yq is used, not the yq on PATH"

# An empty module cache with the proxy off stands in for a host that cannot
# fetch the pinned version.
if GOPROXY=off GOMODCACHE="$work/modcache" PLECTURE_LINT_TOOLS_DIR="$work/tools" \
  lint_tool yq > "$work/log" 2>&1; then
  echo "FAIL: yq installed with no way to fetch it" >&2
  exit 1
fi
if ! grep -qF "$YQ_MODULE@$YQ_VERSION" "$work/log"; then
  echo "FAIL: the failed yq install does not name the pinned version" >&2
  cat "$work/log" >&2
  exit 1
fi
echo "ok: a yq install that cannot fetch the pin names the pinned version"
