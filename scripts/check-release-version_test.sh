#!/bin/sh
set -eu

expected_version='v0.3.1-snapshot'

check_release_versions() {
	repo_root=$1
	goreleaser_version=$(sed -n 's/^[[:space:]]*version_template: "\([^"]*\)"$/\1/p' "$repo_root/.goreleaser.yaml")
	workflow_version=$(sed -n '/^[[:space:]]*- name: Select dry-run artifact version$/,/^[[:space:]]*- name: Export artifact version$/ { s/^[[:space:]]*run: echo "value=\([^"]*\)" >> "\$GITHUB_OUTPUT"$/\1/p; }' "$repo_root/.github/workflows/release.yml")
	makefile_version=$(sed -n 's/.*check-release-artifacts\.sh collection dist "\([^"]*\)"$/\1/p' "$repo_root/Makefile")

	for source_version in \
		".goreleaser.yaml:$goreleaser_version" \
		".github/workflows/release.yml:$workflow_version" \
		"Makefile:$makefile_version"; do
		source_file=${source_version%%:*}
		actual_version=${source_version#*:}
		if [ "$actual_version" != "$expected_version" ]; then
			printf 'snapshot version mismatch in %s: got %s, want %s\n' \
				"$source_file" "${actual_version:-missing}" "$expected_version" >&2
			return 1
		fi
	done
}

expect_failure() {
	expected_message=$1
	shift
	set +e
	output=$("$@" 2>&1)
	status=$?
	set -e
	if [ "$status" -eq 0 ]; then
		printf 'expected failure containing %s\n' "$expected_message" >&2
		exit 1
	fi
	case "$output" in
		*"$expected_message"*) ;;
		*)
			printf 'output %s does not contain %s\n' "$output" "$expected_message" >&2
			exit 1
			;;
	esac
}

script_dir=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
repo_root=$(git -C "$script_dir/.." rev-parse --show-toplevel)
check_release_versions "$repo_root"

test_root=$(mktemp -d "${TMPDIR:-/tmp}/agent-studio-release-version-test.XXXXXX")
trap 'rm -rf "$test_root"' EXIT HUP INT TERM
mkdir -p "$test_root/.github/workflows"
printf '%s\n' 'snapshot:' '  version_template: "v0.3.1-snapshot"' > "$test_root/.goreleaser.yaml"
printf '%s\n' \
	'- name: Select dry-run artifact version' \
	'  run: echo "value=v0.2.1-snapshot" >> "$GITHUB_OUTPUT"' \
	'- name: Export artifact version' > "$test_root/.github/workflows/release.yml"
printf '%s\n' 'release-snapshot:' '  sh scripts/check-release-artifacts.sh collection dist "v0.3.1-snapshot"' > "$test_root/Makefile"
expect_failure 'snapshot version mismatch in .github/workflows/release.yml' check_release_versions "$test_root"

printf '%s\n' 'check-release-version tests passed'
