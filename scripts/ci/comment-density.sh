#!/usr/bin/env bash
# Usage: comment-density.sh <base> <head>, with HEAD_REF naming the head
# branch. The branch arrives through the environment because a branch name
# spliced into a workflow's run line would be shell syntax the PR author
# controls.
set -euo pipefail
cd "$(dirname "${BASH_SOURCE[0]}")/../.."

./scripts/check-comment-density_selftest.sh
case "${HEAD_REF:-}" in
  integration/* | release/*) ./scripts/check-comment-density.sh --per-commit "$1" "$2" ;;
  *) ./scripts/check-comment-density.sh "$1" "$2" ;;
esac
