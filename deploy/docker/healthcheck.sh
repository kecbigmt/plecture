#!/usr/bin/env bash
set -euo pipefail

# Docker/ECS run HEALTHCHECK as root regardless of which user the
# supervised process runs as, so this can always reach the bus socket even
# though `plect serve` creates it 0600 under the `plect` user — root
# bypasses Unix socket file-permission checks the same way it bypasses any
# other file's. See app/commands/serve.go: /healthz is the one route the
# bus server never gates on PLECT_BUS_TOKEN.
exec curl --unix-socket "${PLECT_BUS_SOCKET}" -fsS -o /dev/null http://localhost/healthz
