#!/usr/bin/env bash
# Validates the plugin packaging design invariants that are easy to regress
# mechanically: catalog-based identity, catalog registration as the trust act,
# per-plugin lock entries, and removal of the replaced prefix-trust model.
set -euo pipefail

root="${PLUGIN_PACKAGING_DESIGN_ROOT:-$(pwd)}"
doc="${PLUGIN_PACKAGING_DESIGN_DOC:-$root/docs/plugin-packaging-design.md}"

fail=0

require_literal() {
  local text="$1"
  if ! grep -Fq -- "$text" "$doc"; then
    echo "$doc: missing required text: $text" >&2
    fail=1
  fi
}

forbid_literal() {
  local text="$1"
  if grep -Fq -- "$text" "$doc"; then
    echo "$doc: forbidden replaced-model text remains: $text" >&2
    fail=1
  fi
}

require_count() {
  local pattern="$1"
  local expected="$2"
  local count
  count="$(grep -Ec -- "$pattern" "$doc" || true)"
  if [ "$count" != "$expected" ]; then
    echo "$doc: expected $expected matches for /$pattern/, found $count" >&2
    fail=1
  fi
}

require_literal 'catalog.toml'
require_literal 'schema_version = 1'
require_literal 'plugins = ['
require_literal 'The catalog-relative path listed in `catalog.toml` is the plugin identity.'
require_literal 'A `trusted_catalogs` entry is the trust act.'
require_literal '{{bin "<catalog-alias>/<plugin-path>/<executable>"}}'
require_literal 'id = "official/github"'
require_literal 'catalog_alias = "official"'
require_literal 'catalog_resolved_revision ='
require_literal '`plect catalog add <alias> <locator> [--revision <rev>]`'
require_literal '`plect plugin update <alias>/<path> [--revision <rev>]`'
require_literal 'Other plugins from the same catalog keep their previous'

forbid_literal '[[trusted_sources]]'
forbid_literal 'trusted_sources'
forbid_literal 'format_version'
forbid_literal 'format = 1'
forbid_literal '[[catalogs]]'
forbid_literal 'source_prefix'
forbid_literal 'name = "github"'
forbid_literal '| `name` | yes | Stable plugin identity.'
forbid_literal 'Expected plugin name. It must match `plugin.toml`'
forbid_literal '{{bin "agent-runtime"}}'
forbid_literal '{{bin "plugin-name/executable-name"}}'
forbid_literal '`plect plugin add <source>'
forbid_literal '`plect plugin update <name>'

require_count '^Catalog registration and plugin enablement:$' 3
require_count '^plect catalog add ' 3
require_count '^plect plugin add ' 3
require_count '^plect plugin update ' 3

if [ "$fail" -ne 0 ]; then
  echo >&2
  echo "Plugin packaging design invariant violated. Keep the document aligned" >&2
  echo "with catalog registrations, path-based plugin identity, and per-plugin" >&2
  echo "lock granularity." >&2
  exit 1
fi

echo "plugin-packaging-design check passed"
