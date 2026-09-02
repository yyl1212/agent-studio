#!/bin/sh
set -eu

script_dir=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
repo_root=$(git -C "$script_dir/.." rev-parse --show-toplevel)

release_config="$repo_root/.goreleaser.yaml"
workflow="$repo_root/.github/workflows/release.yml"

# Keep the release configuration constrained to the nine CLI assets verified below.
build_block=$(sed -n '/^builds:/,/^archives:/p' "$release_config")
test "$(printf '%s\n' "$build_block" | grep -Ec '^  - id:')" -eq 1
goos=$(printf '%s\n' "$build_block" | awk '/^    goos:/{in_list=1;next} /^    goarch:/{in_list=0} in_list && /^      - /{print substr($0,9)}')
goarch=$(printf '%s\n' "$build_block" | awk '/^    goarch:/{in_list=1;next} in_list && /^      - /{print substr($0,9)}')
test "$goos" = "$(printf 'linux\ndarwin')"
test "$goarch" = "$(printf 'amd64\narm64')"

yaml_value() {
	awk -v section="$1" -v key="$2" '$0 == section ":" { in_section = 1; next } in_section && $0 ~ "^  " key ":" { print $2; exit } in_section && /^[^ ]/ { exit }' "$release_config"
}
test "$(yaml_value source enabled)" = false
test "$(yaml_value release disable)" = true

sbom_artifacts=$(awk '$0 == "sboms:" { in_section = 1; next } in_section && /^    artifacts:/ { print $2; next } in_section && /^[^ ]/ { exit }' "$release_config")
test "$sbom_artifacts" = archive
test "$(printf '%s\n' "$sbom_artifacts" | wc -l | tr -d ' ')" -eq 1
archive_formats=$(awk '$0 == "    formats:"{in_list=1;next} in_list && /^      - /{print substr($0,9);next} in_list{exit}' "$release_config")
test "$archive_formats" = tar.gz
test "$(printf '%s\n' "$archive_formats" | wc -l | tr -d ' ')" -eq 1
if grep -Eq '^(dockers|nfpms|brews|scoops|snapcrafts|signs|notarize|dmg|pkgs):' "$release_config"; then
	exit 1
fi

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

draft_block=$(sed -n '/^      - name: Verify draft asset set$/,/^      - name:/p' "$workflow")
workflow_targets=$(printf '%s\n' "$draft_block" | sed -n 's/^[[:space:]]*for target in \(.*\); do$/\1/p')
test "$workflow_targets" = 'darwin_amd64 darwin_arm64 linux_amd64 linux_arm64'
workflow_printfs=$(printf '%s\n' "$draft_block" | grep -E '^[[:space:]]*printf '\''agent-studio_')
test "$(printf '%s\n' "$workflow_printfs" | grep -Fxc '              printf '\''agent-studio_%s_%s.tar.gz\n'\'' "$GITHUB_REF_NAME" "$target"')" -eq 1
test "$(printf '%s\n' "$workflow_printfs" | grep -Fxc '              printf '\''agent-studio_%s_%s.tar.gz.spdx.json\n'\'' "$GITHUB_REF_NAME" "$target"')" -eq 1
workflow_templates=$(printf '%s\n' "$draft_block" | sed -n "s/^[[:space:]]*printf '\(agent-studio_[^']*\)\\\\n'.*/\1/p")
test "$workflow_templates" = "$(printf 'agent-studio_%%s_%%s.tar.gz\nagent-studio_%%s_%%s.tar.gz.spdx.json')"
test "$(printf '%s\n' "$draft_block" | grep -Fxc "            printf 'checksums.txt\\n'")" -eq 1
{
	for target in $workflow_targets; do
		printf '%s\n' "$workflow_templates" | while IFS= read -r template; do
			printf "$template\n" v0.5.0-rc.1 "$target"
		done
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
