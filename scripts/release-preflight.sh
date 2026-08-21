#!/usr/bin/env bash
set -euo pipefail

fail() {
	printf 'release preflight failed: %s\n' "$1" >&2
	exit 1
}

[[ "$#" -eq 1 ]] || fail 'usage: release-preflight.sh <vX.Y.Z[-rc.N]>'
tag=$1
[[ "$tag" =~ ^v[0-9]+\.[0-9]+\.[0-9]+(-rc\.[1-9][0-9]*)?$ ]] || fail "invalid release tag: $tag"

origin_url=$(git remote get-url origin) || fail 'unable to read origin URL'
case "$origin_url" in
	git@github.com:*) repository=${origin_url#git@github.com:} ;;
	https://github.com/*) repository=${origin_url#https://github.com/} ;;
	ssh://git@github.com/*) repository=${origin_url#ssh://git@github.com/} ;;
	*) fail "unsupported origin URL: $origin_url" ;;
esac
repository=${repository%.git}
[[ "$repository" =~ ^[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+$ ]] || fail "invalid GitHub repository from origin: $repository"

set +e
git show-ref --verify --quiet "refs/tags/$tag"
local_status=$?
set -e
case "$local_status" in
	0) fail "local tag already exists: $tag" ;;
	1) ;;
	*) fail "unable to determine whether local tag exists: $tag" ;;
esac

set +e
git ls-remote --exit-code --tags origin "refs/tags/$tag" >/dev/null
remote_status=$?
set -e
case "$remote_status" in
	0) fail "remote tag already exists: $tag" ;;
	2) ;;
	*) fail "unable to determine whether remote tag exists: $tag" ;;
esac

script_dir=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
GITHUB_REPOSITORY=$repository bash "$script_dir/check-release-immutability.sh" preflight || \
	fail 'immutable releases are not enabled'

head_commit=$(git rev-parse HEAD) || fail 'unable to resolve HEAD'
printf 'release repository preflight ok: repository=%s tag=%s head=%s\n' \
	"$repository" "$tag" "$head_commit"
