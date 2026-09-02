#!/bin/sh
set -eu

expected_version='v0.5.0-snapshot'

check_release_versions() {
	repo_root=$1
	goreleaser_snapshot=$(sed -n 's/^[[:space:]]*version_template: "\([^"]*\)"$/\1/p' "$repo_root/.goreleaser.yaml")
	goreleaser_version="v$goreleaser_snapshot"
	workflow_version=$(sed -n '/^[[:space:]]*- name: Select dry-run artifact version$/,/^[[:space:]]*- name: Export artifact version$/ { s/^[[:space:]]*run: echo "value=\([^"]*\)" >> "\$GITHUB_OUTPUT"$/\1/p; }' "$repo_root/.github/workflows/release.yml")
	makefile_version=$(sed -n 's/.*check-release-artifacts\.sh collection dist "\([^"]*\)"$/\1/p' "$repo_root/Makefile")

	for source_version in \
		".goreleaser.yaml (rendered):$goreleaser_version" \
		".github/workflows/release.yml:$workflow_version" \
		"Makefile:$makefile_version"; do
		source_file=${source_version%%:*}
		actual_version=${source_version#*:}
		if [ "$actual_version" != "$expected_version" ]; then
			printf 'rendered snapshot version mismatch in %s: got %s, want %s\n' \
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
workflow_old_root="$test_root/workflow-old"
mkdir -p "$workflow_old_root/.github/workflows"
printf '%s\n' 'snapshot:' '  version_template: "0.5.0-snapshot"' > "$workflow_old_root/.goreleaser.yaml"
printf '%s\n' \
	'- name: Select dry-run artifact version' \
	'  run: echo "value=v0.2.1-snapshot" >> "$GITHUB_OUTPUT"' \
	'- name: Export artifact version' > "$workflow_old_root/.github/workflows/release.yml"
printf '%s\n' 'release-snapshot:' '  sh scripts/check-release-artifacts.sh collection dist "v0.5.0-snapshot"' > "$workflow_old_root/Makefile"
expect_failure 'rendered snapshot version mismatch in .github/workflows/release.yml: got v0.2.1-snapshot' check_release_versions "$workflow_old_root"

double_v_root="$test_root/double-v"
mkdir -p "$double_v_root/.github/workflows"
printf '%s\n' 'snapshot:' '  version_template: "v0.5.0-snapshot"' > "$double_v_root/.goreleaser.yaml"
printf '%s\n' \
	'- name: Select dry-run artifact version' \
	'  run: echo "value=v0.5.0-snapshot" >> "$GITHUB_OUTPUT"' \
	'- name: Export artifact version' > "$double_v_root/.github/workflows/release.yml"
printf '%s\n' 'release-snapshot:' '  sh scripts/check-release-artifacts.sh collection dist "v0.5.0-snapshot"' > "$double_v_root/Makefile"
expect_failure 'rendered snapshot version mismatch in .goreleaser.yaml (rendered): got vv0.5.0-snapshot' check_release_versions "$double_v_root"

ruby - "$repo_root/.github/workflows/release.yml" <<'RUBY'
require "shellwords"
require "yaml"

workflow = YAML.safe_load(File.read(ARGV.fetch(0)), aliases: true)

def release_step(workflow, name)
  workflow.fetch("jobs").fetch("publish").fetch("steps")
    .find { |step| step["name"] == name } || raise("missing publish step: #{name}")
end

def shell_commands(run)
  run.lines.each_with_object([]) do |line, commands|
    stripped = line.strip
    next if stripped.empty?
    commands << Shellwords.split(stripped)
  rescue ArgumentError
    next
  end
end

def promotion_commands(run)
  promotion_case = /\Acase "\$GITHUB_REF_NAME" in\n[ \t]+\*-rc\.\*\)[ \t]+([^\n]+?)[ \t]+;;\n[ \t]+\*\)[ \t]+([^\n]+?)[ \t]+;;\n[ \t]*esac\n?\z/
  match = run.match(promotion_case)
  unless match
    raise "release promotion must select on GITHUB_REF_NAME with RC before stable"
  end
  [Shellwords.split(match[1].strip), Shellwords.split(match[2].strip)]
end

def verify_release_status(workflow)
  promotion = release_step(workflow, "Promote verified release").fetch("run", "")
  rc_command, stable_command = promotion_commands(promotion)

  expected_prefix = ["gh", "release", "edit", "$GITHUB_REF_NAME"]
  raise "RC promotion must edit the selected release" unless rc_command.first(4) == expected_prefix
  raise "RC promotion must clear draft state" unless rc_command.include?("--draft=false")
  raise "RC promotion must set prerelease state" unless rc_command.include?("--prerelease")
  raise "RC promotion must not set Latest" if rc_command.include?("--latest")
  unless rc_command == expected_prefix + ["--draft=false", "--prerelease"]
    raise "RC promotion contains unexpected arguments"
  end

  expected_stable = expected_prefix + ["--draft=false", "--prerelease=false", "--latest"]
  unless stable_command == expected_stable
    raise "stable promotion must clear draft and prerelease state and set Latest"
  end

  draft_verification = release_step(workflow, "Verify draft asset set").fetch("run", "")
  required_asset_fragments = {
    "for target in darwin_amd64 darwin_arm64 linux_amd64 linux_arm64; do" =>
      "draft verification must enumerate exactly four release targets",
    "printf 'agent-studio_%s_%s.tar.gz\\n' \"$GITHUB_REF_NAME\" \"$target\"" =>
      "draft verification must enumerate release archives",
    "printf 'agent-studio_%s_%s.tar.gz.spdx.json\\n' \"$GITHUB_REF_NAME\" \"$target\"" =>
      "draft verification must enumerate release SBOMs",
    "printf 'checksums.txt\\n'" =>
      "draft verification must enumerate checksums.txt",
    "gh release view \"$GITHUB_REF_NAME\" --json assets --jq '.assets[].name' | sort > actual-assets.txt" =>
      "draft verification must read the remote asset names",
    "test \"$(wc -l < actual-assets.txt | tr -d ' ')\" -eq 9" =>
      "draft verification must require exactly nine assets",
  }
  required_asset_fragments.each do |fragment, message|
    raise message unless draft_verification.include?(fragment)
  end

  commands = shell_commands(draft_verification)
  expected_cleanup_trap = [
    "trap",
    "rm -rf \"$remote_dist\"",
    "EXIT",
    "HUP",
    "INT",
    "TERM",
  ]
  cleanup_traps = commands.select do |command|
    command.any? { |token| token.match?(/\btrap\b/) }
  end
  unless cleanup_traps == [expected_cleanup_trap]
    raise "draft verification trap must only clean remote_dist without changing exit status"
  end

  expected_diff = ["diff", "-u", "expected-assets.txt", "actual-assets.txt"]
  asset_diffs = commands.select { |command| command.first == "diff" }
  unless asset_diffs.length == 1 && asset_diffs.first.first(4) == expected_diff
    raise "draft verification must compare the exact expected and actual asset sets"
  end
  unless asset_diffs == [expected_diff]
    raise "draft verification asset diff must propagate failures"
  end

  required_tag_fragments = {
    "test \"$(gh api \"$tag_api\" --jq '.object.type')\" = tag" =>
      "draft verification must require an annotated Tag object",
    "tag_object=$(gh api \"$tag_api\" --jq '.object.sha')" =>
      "draft verification must resolve the annotated Tag object",
    "annotated_api=\"repos/$GITHUB_REPOSITORY/git/tags/$tag_object\"" =>
      "draft verification must query the annotated Tag object",
    "test \"$(gh api \"$annotated_api\" --jq '.object.type')\" = commit" =>
      "draft verification must require the Tag object to target a commit",
  }
  required_tag_fragments.each do |fragment, message|
    raise message unless draft_verification.include?(fragment)
  end

  expected_tag_head = [
    "test",
    "$(gh api $annotated_api --jq '.object.sha')",
    "=",
    "$(git rev-parse HEAD)",
  ]
  tag_head_assertions = commands.select do |command|
    command.first == "test" &&
      command.fetch(1, "").include?("$annotated_api") &&
      command.fetch(1, "").include?(".object.sha")
  end
  unless tag_head_assertions.length == 1 && tag_head_assertions.first.first(4) == expected_tag_head
    raise "draft verification must require the annotated Tag object to target HEAD"
  end
  unless tag_head_assertions == [expected_tag_head]
    raise "draft verification Tag-to-HEAD assertion must propagate failures"
  end
end

def expect_release_status_failure(workflow, fixture, expected)
  verify_release_status(workflow)
rescue RuntimeError => error
  unless error.message == expected
    abort "release status fixture #{fixture} failed for the wrong reason: #{error.message}"
  end
  return
else
  abort "release status fixture was accepted: #{fixture}"
end

begin
  verify_release_status(workflow)
rescue RuntimeError => error
  abort "release status contract violation: #{error.message}"
end

fixed_selector_fixture = Marshal.load(Marshal.dump(workflow))
fixed_selector_run = release_step(fixed_selector_fixture, "Promote verified release").fetch("run")
unless fixed_selector_run.sub!("case \"$GITHUB_REF_NAME\" in", "case \"v0.5.0\" in")
  abort "failed to construct fixed promotion selector fixture"
end
expect_release_status_failure(
  fixed_selector_fixture,
  "fixed GA promotion selector",
  "release promotion must select on GITHUB_REF_NAME with RC before stable",
)

reordered_branches_fixture = Marshal.load(Marshal.dump(workflow))
reordered_branches_run = release_step(reordered_branches_fixture, "Promote verified release").fetch("run")
reordered_lines = reordered_branches_run.lines
rc_branch_index = reordered_lines.index { |line| line.strip.start_with?("*-rc.*)") }
stable_branch_index = reordered_lines.index { |line| line.strip.start_with?("*)") }
unless rc_branch_index && stable_branch_index
  abort "failed to construct reordered promotion branches fixture"
end
reordered_lines[rc_branch_index], reordered_lines[stable_branch_index] =
  reordered_lines[stable_branch_index], reordered_lines[rc_branch_index]
reordered_branches_run.replace(reordered_lines.join)
expect_release_status_failure(
  reordered_branches_fixture,
  "stable promotion branch before RC",
  "release promotion must select on GITHUB_REF_NAME with RC before stable",
)

rc_latest_fixture = Marshal.load(Marshal.dump(workflow))
rc_latest_run = release_step(rc_latest_fixture, "Promote verified release").fetch("run")
rc_latest_run.sub!("--draft=false --prerelease ;;", "--draft=false --prerelease --latest ;;")
expect_release_status_failure(rc_latest_fixture, "RC marked Latest", "RC promotion must not set Latest")

rc_not_prerelease_fixture = Marshal.load(Marshal.dump(workflow))
rc_not_prerelease_run = release_step(rc_not_prerelease_fixture, "Promote verified release").fetch("run")
rc_not_prerelease_run.sub!("--draft=false --prerelease ;;", "--draft=false ;;")
expect_release_status_failure(
  rc_not_prerelease_fixture,
  "RC missing prerelease",
  "RC promotion must set prerelease state",
)

asset_count_fixture = Marshal.load(Marshal.dump(workflow))
asset_count_run = release_step(asset_count_fixture, "Verify draft asset set").fetch("run")
asset_count_run.sub!("-eq 9", "-eq 8")
expect_release_status_failure(
  asset_count_fixture,
  "eight draft assets",
  "draft verification must require exactly nine assets",
)

extra_target_fixture = Marshal.load(Marshal.dump(workflow))
extra_target_run = release_step(extra_target_fixture, "Verify draft asset set").fetch("run")
extra_target_run.sub!("linux_arm64; do", "linux_arm64 windows_amd64; do")
expect_release_status_failure(
  extra_target_fixture,
  "extra draft target",
  "draft verification must enumerate exactly four release targets",
)

missing_asset_diff_fixture = Marshal.load(Marshal.dump(workflow))
missing_asset_diff_run = release_step(missing_asset_diff_fixture, "Verify draft asset set").fetch("run")
missing_asset_diff_run.sub!("diff -u expected-assets.txt actual-assets.txt", "true")
expect_release_status_failure(
  missing_asset_diff_fixture,
  "missing exact asset set comparison",
  "draft verification must compare the exact expected and actual asset sets",
)

masked_asset_diff_fixture = Marshal.load(Marshal.dump(workflow))
masked_asset_diff_run = release_step(masked_asset_diff_fixture, "Verify draft asset set").fetch("run")
unless masked_asset_diff_run.sub!(
    "diff -u expected-assets.txt actual-assets.txt",
    "diff -u expected-assets.txt actual-assets.txt || true",
  )
  abort "failed to construct masked asset diff fixture"
end
expect_release_status_failure(
  masked_asset_diff_fixture,
  "masked exact asset set comparison",
  "draft verification asset diff must propagate failures",
)

wrong_tag_target_fixture = Marshal.load(Marshal.dump(workflow))
wrong_tag_target_run = release_step(wrong_tag_target_fixture, "Verify draft asset set").fetch("run")
wrong_tag_target_run.sub!("git rev-parse HEAD)", "git rev-parse HEAD^)")
expect_release_status_failure(
  wrong_tag_target_fixture,
  "annotated Tag does not target HEAD",
  "draft verification must require the annotated Tag object to target HEAD",
)

masked_tag_target_fixture = Marshal.load(Marshal.dump(workflow))
masked_tag_target_run = release_step(masked_tag_target_fixture, "Verify draft asset set").fetch("run")
tag_head_assertion = "test \"$(gh api \"$annotated_api\" --jq '.object.sha')\" = \"$(git rev-parse HEAD)\""
unless masked_tag_target_run.sub!(tag_head_assertion, "#{tag_head_assertion} || true")
  abort "failed to construct masked Tag-to-HEAD fixture"
end
expect_release_status_failure(
  masked_tag_target_fixture,
  "masked annotated Tag-to-HEAD assertion",
  "draft verification Tag-to-HEAD assertion must propagate failures",
)

masked_exit_trap_fixture = Marshal.load(Marshal.dump(workflow))
masked_exit_trap_run = release_step(masked_exit_trap_fixture, "Verify draft asset set").fetch("run")
safe_cleanup_trap = "trap 'rm -rf \"$remote_dist\"' EXIT HUP INT TERM"
unless masked_exit_trap_run.sub!(safe_cleanup_trap, "trap 'exit 0' EXIT HUP INT TERM")
  abort "failed to construct masked EXIT trap fixture"
end
expect_release_status_failure(
  masked_exit_trap_fixture,
  "EXIT trap masks verification failure",
  "draft verification trap must only clean remote_dist without changing exit status",
)

wrapped_second_trap_fixture = Marshal.load(Marshal.dump(workflow))
wrapped_second_trap_run = release_step(wrapped_second_trap_fixture, "Verify draft asset set").fetch("run")
unless wrapped_second_trap_run.sub!(
    safe_cleanup_trap,
    "#{safe_cleanup_trap}\nbuiltin trap 'exit 0' EXIT HUP INT TERM",
  )
  abort "failed to construct wrapped second EXIT trap fixture"
end
expect_release_status_failure(
  wrapped_second_trap_fixture,
  "builtin registers a second EXIT trap",
  "draft verification trap must only clean remote_dist without changing exit status",
)
RUBY

printf '%s\n' 'check-release-version tests passed'
