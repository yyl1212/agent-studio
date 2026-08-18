#!/bin/sh
set -eu

script_dir=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
repo_root=$(git -C "$script_dir/.." rev-parse --show-toplevel)
test_root=$(mktemp -d "${TMPDIR:-/tmp}/agent-studio-release-wrapper-test.XXXXXX")
trap 'rm -rf "$test_root"' EXIT HUP INT TERM
runtime_dir="$test_root/runtime"
cache_dir="$test_root/gocache"
mkdir -p "$runtime_dir" "$cache_dir"

set +e
(
	cd "$test_root"
	TMPDIR="$runtime_dir" GOCACHE="$cache_dir" sh "$repo_root/scripts/check-release-artifacts.sh"
) >"$test_root/usage.out" 2>"$test_root/usage.err"
exit_code=$?
set -e
test "$exit_code" -eq 2
grep -F "usage: check-release-artifacts" "$test_root/usage.err" >/dev/null
if grep -F "exit status" "$test_root/usage.err" >/dev/null; then
	exit 1
fi
test -z "$(find "$runtime_dir" -mindepth 1 -maxdepth 1 -name 'agent-studio-release-check.*' -print -quit)"

set +e
(
	cd "$test_root"
	TMPDIR="$runtime_dir" GOCACHE="$cache_dir" sh "$repo_root/scripts/check-release-artifacts.sh" \
		collection "$test_root/missing-dist" v0.2.0-rc.2
) >"$test_root/error.out" 2>"$test_root/error.err"
exit_code=$?
set -e
test "$exit_code" -eq 1
grep -F "verify release artifacts:" "$test_root/error.err" >/dev/null
test -z "$(find "$runtime_dir" -mindepth 1 -maxdepth 1 -name 'agent-studio-release-check.*' -print -quit)"

printf 'check-release-artifacts wrapper tests passed\n'
