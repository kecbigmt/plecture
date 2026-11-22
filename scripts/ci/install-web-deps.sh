#!/usr/bin/env bash
# Not corepack: corepack enable's own package-manager download path fails
# signature verification against npm's registry ("Cannot find matching
# keyid"), independent of this repo, while npm install -g uses npm's own
# integrity check. The version comes from package.json's packageManager
# field so the two cannot drift apart. One lockfile at the workspace root
# covers both web/api and web/app.
set -euo pipefail
cd "$(dirname "${BASH_SOURCE[0]}")/../../web"

npm install -g "pnpm@$(node -p "require('./package.json').packageManager.split('@')[1]")"
pnpm install --frozen-lockfile
