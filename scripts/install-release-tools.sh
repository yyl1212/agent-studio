#!/bin/sh
set -eu

script_dir=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
repo_root=$(git -C "$script_dir/.." rev-parse --show-toplevel)
tools_dir=${RELEASE_TOOLS_DIR:-"$repo_root/.release-tools/bin"}
mkdir -p "$tools_dir"

GOBIN="$tools_dir" CGO_ENABLED=0 go install github.com/goreleaser/goreleaser/v2@v2.17.1
GOBIN="$tools_dir" CGO_ENABLED=0 go install github.com/anchore/syft/cmd/syft@v1.51.0

"$tools_dir/goreleaser" --version | grep -F '2.17.1' >/dev/null
go version -m "$tools_dir/syft" | awk '
	$1 == "mod" && $2 == "github.com/anchore/syft" && $3 == "v1.51.0" { found = 1 }
	END { exit !found }
'
printf 'release tools ready: goreleaser=%s syft=%s\n' 'v2.17.1' 'v1.51.0'
