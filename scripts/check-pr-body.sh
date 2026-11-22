#!/usr/bin/env bash
# Checks a pull request body: it names its issue with a closing keyword, and
# every image under "## Overview" is pinned to a full commit SHA. The rules
# live in scripts/prbody; its tests are their specification.
#
# The tool is a module of its own, run with GOWORK=off and left out of
# go.work: in workspace mode its requirements would join app's build list and
# could move app's dependency versions away from what `go install` resolves.
set -euo pipefail

tool_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")/prbody" && pwd)"
bin_dir="$(mktemp -d)"
trap 'rm -rf "$bin_dir"' EXIT

(cd "$tool_dir" && GOWORK=off go build -o "$bin_dir/prbody" .)
"$bin_dir/prbody" "$@"
