#!/usr/bin/env bash
set -euo pipefail

if [[ $# -eq 0 ]]; then
  echo "Usage: ./scripts/go-local.sh <go subcommand> [args...]" >&2
  exit 1
fi

project_root="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)"
export GOMODCACHE="${GOMODCACHE:-$project_root/.gomodcache}"
export GOCACHE="${GOCACHE:-$project_root/.gocache}"
export GOFLAGS="${GOFLAGS:+$GOFLAGS }-modcacherw"

cd -- "$project_root"
exec go "$@"
