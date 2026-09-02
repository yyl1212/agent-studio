#!/bin/sh
set -eu

script_dir=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
repo_root=$(git -C "$script_dir/.." rev-parse --show-toplevel)

release_config="$repo_root/.goreleaser.yaml"
workflow="$repo_root/.github/workflows/release.yml"

# Keep the release configuration constrained to the nine CLI assets verified below.
build_block=$(sed -n '/^builds:/,/^archives:/p' "$release_config")
test "$(printf '%s\n' "$build_block" | grep -Ec '^      - (linux|darwin)$')" -eq 2
test "$(printf '%s\n' "$build_block" | grep -Ec '^      - (amd64|arm64)$')" -eq 2
test "$(printf '%s\n' "$build_block" | grep -Fxc '      - linux')" -eq 1
test "$(printf '%s\n' "$build_block" | grep -Fxc '      - darwin')" -eq 1
test "$(printf '%s\n' "$build_block" | grep -Fxc '      - amd64')" -eq 1
test "$(printf '%s\n' "$build_block" | grep -Fxc '      - arm64')" -eq 1
grep -F 'source:' "$release_config" >/dev/null
grep -F '  enabled: false' "$release_config" >/dev/null
grep -F 'release:' "$release_config" >/dev/null
grep -F '  disable: true' "$release_config" >/dev/null
grep -F '    artifacts: archive' "$release_config" >/dev/null
test "$(grep -Eic 'docker|windows|sign|notar' "$release_config")" -eq 0

expected_assets="$repo_root/.release-artifacts.expected"
workflow_assets="$repo_root/.release-artifacts.workflow"
trap 'rm -f "$expected_assets" "$workflow_assets"' EXIT HUP INT TERM
{
	for target in darwin_amd64 darwin_arm64 linux_amd64 linux_arm64; do
		printf 'agent-studio_v0.5.0-rc.1_%s.tar.gz\n' "$target"
		printf 'agent-studio_v0.5.0-rc.1_%s.tar.gz.spdx.json\n' "$target"
	done
	printf 'checksums.txt\n'
} | sort >"$expected_assets"

workflow_targets=$(sed -n 's/^[[:space:]]*for target in \(.*\); do$/\1/p' "$workflow")
test "$workflow_targets" = 'darwin_amd64 darwin_arm64 linux_amd64 linux_arm64'
{
	for target in $workflow_targets; do
		printf 'agent-studio_v0.5.0-rc.1_%s.tar.gz\n' "$target"
		printf 'agent-studio_v0.5.0-rc.1_%s.tar.gz.spdx.json\n' "$target"
	done
	printf 'checksums.txt\n'
} | sort >"$workflow_assets"
diff -u "$expected_assets" "$workflow_assets"

test_root=$(mktemp -d "${TMPDIR:-/tmp}/agent-studio-release-wrapper-test.XXXXXX")
trap 'rm -rf "$test_root" "$expected_assets" "$workflow_assets"' EXIT HUP INT TERM
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
		collection "$test_root/missing-dist" v0.5.0-rc.1
) >"$test_root/error.out" 2>"$test_root/error.err"
exit_code=$?
set -e
test "$exit_code" -eq 1
grep -F "verify release artifacts:" "$test_root/error.err" >/dev/null
test -z "$(find "$runtime_dir" -mindepth 1 -maxdepth 1 -name 'agent-studio-release-check.*' -print -quit)"

printf 'check-release-artifacts wrapper tests passed\n'
