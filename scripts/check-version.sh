#!/bin/sh
set -eu

if [ "$#" -ne 1 ]; then
	printf '%s\n' "usage: scripts/check-version.sh <vX.Y.Z-rc.N>" >&2
	exit 2
fi

tag=$1
if ! printf '%s\n' "$tag" | grep -Eq '^v[0-9]+\.[0-9]+\.[0-9]+-rc\.[1-9][0-9]*$'; then
	printf 'invalid release tag: %s\n' "$tag" >&2
	exit 1
fi

script_dir=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
repo_root=$(git -C "$script_dir/.." rev-parse --show-toplevel)
cd "$repo_root"

base_version=${tag#v}
base_version=${base_version%-rc.*}
sdk_version=$(sed -n 's/^const Version = "\([^"]*\)"$/\1/p' sdk/go/agentnode/node.go)
if [ -z "$sdk_version" ] || [ "$sdk_version" != "$base_version" ]; then
	printf 'SDK version mismatch: tag=%s sdk=%s\n' "$base_version" "${sdk_version:-missing}" >&2
	exit 1
fi

release_notes="docs/releases/$tag.md"
if [ ! -s "$release_notes" ]; then
	printf 'missing release notes: %s\n' "$release_notes" >&2
	exit 1
fi

status=$(git status --porcelain --untracked-files=normal)
if [ -n "$status" ]; then
	printf '%s\n' "worktree is not clean" >&2
	exit 1
fi

printf 'release preflight ok: %s\n' "$tag"
