#!/usr/bin/env bash
# The integration-tagged tests spin up real git repos and worktrees and
# build the shipped plugin binaries under test: too slow for the default go
# test run, and exactly the coverage that would otherwise let a
# spec-encoding test rot unnoticed.
set -euo pipefail
cd "$(dirname "${BASH_SOURCE[0]}")/../../app"

# ./... would re-run every untagged unit test a second time, so the package
# list comes from the tag itself, which also picks up a new tagged file
# without an edit here. The sed normalizes to exactly one "./" prefix: go
# test reads a bare "internal/foo" as an import path, and grep's own "./"
# prefixing differs across implementations.
mapfile -t pkgs < <(grep -rl --include='*_test.go' 'go:build integration' . | xargs -n1 dirname | sort -u | sed 's#^\(\./\)\?#./#')
echo "Tagged packages:"
printf '%s\n' "${pkgs[@]}"
go test -tags integration "${pkgs[@]}"
go vet -tags integration ./...
