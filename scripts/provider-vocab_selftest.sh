#!/usr/bin/env bash
# Proves provider-vocab.py works before check-provider-boundary.sh trusts it
# for its vocabulary: a valid catalog + plugin.toml produces the expected
# words, and each of catalog.toml missing, catalog.toml malformed, a listed
# plugin with no plugin.toml, and a plugin.toml that is malformed all fail
# loudly (non-zero exit) rather than silently producing wrong or empty
# vocabulary.
set -euo pipefail

root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
tool="$root/scripts/provider-vocab.py"

# Happy path: a published plugin's id and executable name both appear,
# sorted.
happy=$(mktemp -d)
trap 'rm -rf "$happy" "$no_catalog" "$bad_catalog" "$unlisted_ok" "$bad_manifest"' EXIT
mkdir -p "$happy/widget"
cat > "$happy/catalog.toml" <<'EOF'
schema_version = 1
description = "fixture catalog"
plugins = ["widget"]
EOF
cat > "$happy/widget/plugin.toml" <<'EOF'
schema_version = 1
version = "0.1.0"
plect_min_version = "0.0.0"
description = "A widget plugin."

[[executables]]
name = "widget-runner"
path = "bin/widget-runner"
EOF

got=$(python3 "$tool" "$happy")
want=$'widget\nwidget-runner'
if [ "$got" != "$want" ]; then
  echo "FAIL: happy path = $(printf '%q' "$got"), want $(printf '%q' "$want")" >&2
  exit 1
fi
echo "ok: derives a published plugin's id and executable name"

# Failure: no catalog.toml at all.
no_catalog=$(mktemp -d)
if python3 "$tool" "$no_catalog" >/tmp/provider-vocab-selftest-no-catalog.log 2>&1; then
  echo "FAIL: succeeded against a plugins root with no catalog.toml" >&2
  cat /tmp/provider-vocab-selftest-no-catalog.log >&2
  exit 1
fi
echo "ok: fails when catalog.toml is missing"

# Failure: catalog.toml is not valid TOML.
bad_catalog=$(mktemp -d)
printf 'this is not toml [[[' > "$bad_catalog/catalog.toml"
if python3 "$tool" "$bad_catalog" >/tmp/provider-vocab-selftest-bad-catalog.log 2>&1; then
  echo "FAIL: succeeded against a malformed catalog.toml" >&2
  cat /tmp/provider-vocab-selftest-bad-catalog.log >&2
  exit 1
fi
echo "ok: fails when catalog.toml is malformed"

# Failure: catalog.toml lists a plugin with no plugin.toml at that path.
unlisted_ok=$(mktemp -d)
cat > "$unlisted_ok/catalog.toml" <<'EOF'
schema_version = 1
description = "fixture catalog"
plugins = ["ghost"]
EOF
if python3 "$tool" "$unlisted_ok" >/tmp/provider-vocab-selftest-ghost.log 2>&1; then
  echo "FAIL: succeeded against a listed plugin with no plugin.toml" >&2
  cat /tmp/provider-vocab-selftest-ghost.log >&2
  exit 1
fi
echo "ok: fails when a listed plugin has no plugin.toml"

# Failure: a listed plugin's plugin.toml is not valid TOML.
bad_manifest=$(mktemp -d)
mkdir -p "$bad_manifest/broken"
cat > "$bad_manifest/catalog.toml" <<'EOF'
schema_version = 1
description = "fixture catalog"
plugins = ["broken"]
EOF
printf 'this is not toml [[[' > "$bad_manifest/broken/plugin.toml"
if python3 "$tool" "$bad_manifest" >/tmp/provider-vocab-selftest-bad-manifest.log 2>&1; then
  echo "FAIL: succeeded against a malformed plugin.toml" >&2
  cat /tmp/provider-vocab-selftest-bad-manifest.log >&2
  exit 1
fi
echo "ok: fails when a listed plugin's plugin.toml is malformed"
