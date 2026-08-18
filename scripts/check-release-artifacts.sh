#!/bin/sh
set -eu

script_dir=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
repo_root=$(git -C "$script_dir/.." rev-parse --show-toplevel)
cd "$repo_root"
tool_dir=$(mktemp -d "${TMPDIR:-/tmp}/agent-studio-release-check.XXXXXX")
trap 'rm -rf "$tool_dir"' EXIT HUP INT TERM
CGO_ENABLED=0 go build -o "$tool_dir/check-release-artifacts" ./internal/releaseartifact/cmd
"$tool_dir/check-release-artifacts" "$@"
