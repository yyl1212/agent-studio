#!/bin/sh
set -eu

script_dir=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
repo_root=$(git -C "$script_dir/.." rev-parse --show-toplevel)

release_config="$repo_root/.goreleaser.yaml"
workflow="$repo_root/.github/workflows/release.yml"
test_root=$(mktemp -d "${TMPDIR:-/tmp}/agent-studio-release-artifacts-test.XXXXXX")
trap 'rm -rf -- "$test_root"' EXIT HUP INT TERM

same_members() {
	expected_members=$(printf '%s\n' "$1" | tr ' ' '\n' | sed '/^$/d' | LC_ALL=C sort)
	actual_members=$(printf '%s\n' "$2" | tr ' ' '\n' | sed '/^$/d' | LC_ALL=C sort)
	test "$expected_members" = "$actual_members"
}

member_contract_failures=
record_member_contract_failure() {
	if test -n "$member_contract_failures"; then
		member_contract_failures="$member_contract_failures
$1"
	else
		member_contract_failures=$1
	fi
}

verify_exact_member_contract() {
	label=$1
	expected=$2
	reordered=$3
	duplicate=$4
	missing=$5
	extra=$6

	if ! same_members "$expected" "$reordered"; then
		record_member_contract_failure "$label rejected a legal reordering"
	fi
	if same_members "$expected" "$duplicate"; then
		record_member_contract_failure "$label accepted a duplicate member"
	fi
	if same_members "$expected" "$missing"; then
		record_member_contract_failure "$label accepted a missing member"
	fi
	if same_members "$expected" "$extra"; then
		record_member_contract_failure "$label accepted an extra member"
	fi
}

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

expected_assets="$test_root/expected-assets"
workflow_assets="$test_root/workflow-assets"
{
	for target in darwin_amd64 darwin_arm64 linux_amd64 linux_arm64; do
		printf 'agent-studio_v0.5.0-rc.1_%s.tar.gz\n' "$target"
		printf 'agent-studio_v0.5.0-rc.1_%s.tar.gz.spdx.json\n' "$target"
	done
	printf 'checksums.txt\n'
} | sort >"$expected_assets"

draft_block=$(sed -n '/^      - name: Verify draft asset set$/,/^      - name:/p' "$workflow")
workflow_targets=$(printf '%s\n' "$draft_block" | sed -n 's/^[[:space:]]*for target in \(.*\); do$/\1/p')
expected_workflow_targets='darwin_amd64 darwin_arm64 linux_amd64 linux_arm64'
same_members "$expected_workflow_targets" "$workflow_targets"
workflow_printfs=$(printf '%s\n' "$draft_block" | grep -E '^[[:space:]]*printf '\''agent-studio_')
test "$(printf '%s\n' "$workflow_printfs" | grep -Fxc '              printf '\''agent-studio_%s_%s.tar.gz\n'\'' "$GITHUB_REF_NAME" "$target"')" -eq 1
test "$(printf '%s\n' "$workflow_printfs" | grep -Fxc '              printf '\''agent-studio_%s_%s.tar.gz.spdx.json\n'\'' "$GITHUB_REF_NAME" "$target"')" -eq 1
workflow_templates=$(printf '%s\n' "$draft_block" | sed -n "s/^[[:space:]]*printf '\(agent-studio_[^']*\)\\\\n'.*/\1/p")
expected_workflow_templates=$(printf 'agent-studio_%%s_%%s.tar.gz\nagent-studio_%%s_%%s.tar.gz.spdx.json')
same_members "$expected_workflow_templates" "$workflow_templates"
verify_exact_member_contract \
	'workflow target set' \
	"$expected_workflow_targets" \
	'linux_arm64 linux_amd64 darwin_arm64 darwin_amd64' \
	'darwin_amd64 darwin_arm64 linux_amd64 linux_arm64 linux_arm64' \
	'darwin_amd64 darwin_arm64 linux_amd64' \
	'darwin_amd64 darwin_arm64 linux_amd64 linux_arm64 windows_amd64'
verify_exact_member_contract \
	'workflow template set' \
	"$expected_workflow_templates" \
	"$(printf 'agent-studio_%%s_%%s.tar.gz.spdx.json\nagent-studio_%%s_%%s.tar.gz')" \
	"$(printf 'agent-studio_%%s_%%s.tar.gz\nagent-studio_%%s_%%s.tar.gz.spdx.json\nagent-studio_%%s_%%s.tar.gz')" \
	'agent-studio_%s_%s.tar.gz' \
	"$(printf 'agent-studio_%%s_%%s.tar.gz\nagent-studio_%%s_%%s.tar.gz.spdx.json\nagent-studio_%%s_%%s.zip')"
if test -n "$member_contract_failures"; then
	printf '%s\n' "$member_contract_failures" >&2
	exit 1
fi
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

verify_concurrent_invocations() {
	concurrency_root="$test_root/concurrency"
	fake_bin="$concurrency_root/bin"
	block_root="$concurrency_root/block"
	mkdir -p "$fake_bin" "$block_root"
	real_sort=$(command -v sort)
	cat >"$fake_bin/sort" <<'SH'
#!/bin/sh
"$REAL_SORT" "$@"
if mkdir "$SORT_BLOCK_ROOT/claimed" 2>/dev/null; then
	: >"$SORT_BLOCK_ROOT/ready"
	while test ! -f "$SORT_BLOCK_ROOT/release"; do
		sleep 0.05
	done
fi
SH
	chmod +x "$fake_bin/sort"

	PATH="$fake_bin:$PATH" \
		REAL_SORT="$real_sort" \
		SORT_BLOCK_ROOT="$block_root" \
		AGENT_STUDIO_ARTIFACT_CONCURRENCY_CHILD=1 \
		sh "$0" >"$concurrency_root/first.out" 2>"$concurrency_root/first.err" &
	first_pid=$!

	attempt=0
	while test ! -f "$block_root/ready"; do
		attempt=$((attempt + 1))
		if test "$attempt" -ge 200; then
			: >"$block_root/release"
			wait "$first_pid" || true
			printf '%s\n' 'concurrency fixture did not reach the shared-file boundary' >&2
			return 1
		fi
		sleep 0.05
	done

	set +e
	AGENT_STUDIO_ARTIFACT_CONCURRENCY_CHILD=1 \
		sh "$0" >"$concurrency_root/second.out" 2>"$concurrency_root/second.err"
	second_status=$?
	: >"$block_root/release"
	wait "$first_pid"
	first_status=$?
	set -e

	if test "$first_status" -ne 0 || test "$second_status" -ne 0; then
		printf '%s\n' "concurrent artifact contract failed: first=$first_status second=$second_status" >&2
		printf '%s\n' 'first stderr:' >&2
		cat "$concurrency_root/first.err" >&2
		printf '%s\n' 'second stderr:' >&2
		cat "$concurrency_root/second.err" >&2
		return 1
	fi
}

if test "${AGENT_STUDIO_ARTIFACT_CONCURRENCY_CHILD:-0}" = 0; then
	verify_concurrent_invocations
fi

printf 'check-release-artifacts wrapper tests passed\n'
