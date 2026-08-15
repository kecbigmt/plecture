#!/usr/bin/env bash
# Proves the packaging-design checker fails when the replaced trust model is
# reintroduced into an otherwise valid copy of the design document.
set -euo pipefail

root="${PLUGIN_PACKAGING_DESIGN_ROOT:-$(pwd)}"
tmp="$(mktemp -d)"
trap 'rm -rf "$tmp"' EXIT

mkdir -p "$tmp/docs"
cp "$root/docs/plugin-packaging-design.md" "$tmp/docs/plugin-packaging-design.md"

if ! PLUGIN_PACKAGING_DESIGN_ROOT="$tmp" "$root/scripts/check-plugin-packaging-design.sh" >/dev/null; then
  echo "checker rejected the unmodified fixture" >&2
  exit 1
fi

printf '\n[[trusted_sources]]\nsource_prefix = "git+https://github.com/example/"\n' >> \
  "$tmp/docs/plugin-packaging-design.md"

if PLUGIN_PACKAGING_DESIGN_ROOT="$tmp" "$root/scripts/check-plugin-packaging-design.sh" >/dev/null 2>&1; then
  echo "checker failed to reject replaced trust-model text" >&2
  exit 1
fi

echo "plugin-packaging-design selftest passed"
