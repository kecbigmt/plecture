#!/usr/bin/env bash
# Given a base branch tip and head, checks migration order for a pull
# request; a push has no diff to scope and runs only the selftest.
set -euo pipefail
cd "$(dirname "${BASH_SOURCE[0]}")/../../app"

go test -count=1 ./db -run '^TestMigrationsMatchTheirManifest$|^TestManifest_'
go test -count=1 ./db -skip '^TestMigrationsMatchTheirManifest$|^TestManifest_'
go generate ./internal/persistence/...
git diff --exit-code -- internal/persistence/sqlcgen/ || {
  echo "::error::app/internal/persistence/sqlcgen is out of date; run 'go generate ./internal/persistence/...' in app and commit the result." >&2
  exit 1
}
go test -count=1 -tags integration ./internal/persistence -run '^TestMigrate_UpgradesEveryReleasedSchema$'

cd ..
./scripts/check-migration-order_selftest.sh
if [ -n "${1:-}" ]; then
  ./scripts/check-migration-order.sh "$1" "$2"
fi
