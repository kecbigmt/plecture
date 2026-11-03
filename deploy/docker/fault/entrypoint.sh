#!/usr/bin/env bash
set -euo pipefail
mkdir -p "$PLECT_CONFIG_HOME" "$XDG_CACHE_HOME"
if [ ! -f "$PLECT_CONFIG_HOME/config.toml" ]; then
  cp -a /etc/plect/. "$PLECT_CONFIG_HOME/"
  sed -i 's/^resource_allowlist.*/resource_allowlist = ["^fault:\/\/"]/' "$PLECT_CONFIG_HOME/config.toml"
  test "$(grep -c '^resource_allowlist = ' "$PLECT_CONFIG_HOME/config.toml")" -eq 1
fi
chown -R 10000:10000 "$PLECT_CONFIG_HOME" "$XDG_CACHE_HOME"
exec /usr/local/bin/entrypoint.sh
