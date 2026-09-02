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
require "digest"
require "fileutils"
require "open3"
require "tmpdir"
require "yaml"

# Pin the complete security-critical scripts, then execute the approved text
# below. Dynamic Bash syntax cannot bypass a digest without an explicit review.
APPROVED_DRAFT_VERIFICATION_SHA256 = "307aebb80ce1feeeac9b96b3d4bdbb2ae2e84da91bae121f72e4f30f60d0936a"
APPROVED_PROMOTION_SHA256 = "6e9fe085732fdf10dcc87dbed2adf489bd59bae3a41a9bde2d658c2b3b8c6454"

workflow = YAML.safe_load(File.read(ARGV.fetch(0)), aliases: true)

def release_step(workflow, name)
  workflow.fetch("jobs").fetch("publish").fetch("steps")
    .find { |step| step["name"] == name } || raise("missing publish step: #{name}")
end

def verify_release_status(workflow)
  promotion = release_step(workflow, "Promote verified release").fetch("run", "")
  draft_verification = release_step(workflow, "Verify draft asset set").fetch("run", "")
  unless Digest::SHA256.hexdigest(draft_verification) == APPROVED_DRAFT_VERIFICATION_SHA256
    raise "draft verification run script must match approved template"
  end
  unless Digest::SHA256.hexdigest(promotion) == APPROVED_PROMOTION_SHA256
    raise "release promotion run script must match approved template"
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

def write_executable(path, content)
  File.write(path, content)
  File.chmod(0o755, path)
end

def run_github_bash(run, env, chdir)
  Open3.capture3(
    env,
    "/bin/bash",
    "--noprofile",
    "--norc",
    "-e",
    "-o",
    "pipefail",
    "-c",
    run,
    chdir: chdir,
  )
end

def verify_release_status_behavior(workflow)
  draft_run = release_step(workflow, "Verify draft asset set").fetch("run")
  promotion_run = release_step(workflow, "Promote verified release").fetch("run")

  Dir.mktmpdir("agent-studio-release-status.") do |root|
    fake_bin = File.join(root, "bin")
    dist = File.join(root, "dist")
    runtime = File.join(root, "tmp")
    FileUtils.mkdir_p([fake_bin, dist, runtime])
    gh_log = File.join(root, "gh.log")
    assets = %w[
      agent-studio_v0.5.0-rc.1_darwin_amd64.tar.gz
      agent-studio_v0.5.0-rc.1_darwin_amd64.tar.gz.spdx.json
      agent-studio_v0.5.0-rc.1_darwin_arm64.tar.gz
      agent-studio_v0.5.0-rc.1_darwin_arm64.tar.gz.spdx.json
      agent-studio_v0.5.0-rc.1_linux_amd64.tar.gz
      agent-studio_v0.5.0-rc.1_linux_amd64.tar.gz.spdx.json
      agent-studio_v0.5.0-rc.1_linux_arm64.tar.gz
      agent-studio_v0.5.0-rc.1_linux_arm64.tar.gz.spdx.json
      checksums.txt
    ]
    assets.each { |name| File.write(File.join(dist, name), "verified fixture: #{name}\n") }

    write_executable(File.join(fake_bin, "git"), <<~'SH')
      #!/bin/sh
      test "$*" = "rev-parse HEAD" || exit 64
      printf '%s\n' expected-head
    SH
    write_executable(File.join(fake_bin, "sh"), <<~'SH')
      #!/bin/sh
      case "$1" in
        *scripts/check-release-artifacts.sh) exit 0 ;;
        *) exec /bin/sh "$@" ;;
      esac
    SH
    write_executable(File.join(fake_bin, "gh"), <<~'SH')
      #!/bin/sh
      case "$1:$2" in
        release:view)
          test "$3" = "$GITHUB_REF_NAME" || exit 65
          test "$4" = "--json" || exit 66
          test "$6" = "--jq" || exit 67
          case "$5|$7" in
            'assets|.assets[].name')
              printf '%s\n' \
                "agent-studio_${GITHUB_REF_NAME}_darwin_amd64.tar.gz" \
                "agent-studio_${GITHUB_REF_NAME}_darwin_amd64.tar.gz.spdx.json" \
                "agent-studio_${GITHUB_REF_NAME}_darwin_arm64.tar.gz" \
                "agent-studio_${GITHUB_REF_NAME}_darwin_arm64.tar.gz.spdx.json" \
                "agent-studio_${GITHUB_REF_NAME}_linux_amd64.tar.gz" \
                "agent-studio_${GITHUB_REF_NAME}_linux_amd64.tar.gz.spdx.json" \
                "agent-studio_${GITHUB_REF_NAME}_linux_arm64.tar.gz" \
                "agent-studio_${GITHUB_REF_NAME}_linux_arm64.tar.gz.spdx.json"
              test "${FAKE_ASSET_MODE:-complete}" = missing || printf '%s\n' checksums.txt
              ;;
            'assets|[.assets[].size > 0] | all') printf '%s\n' true ;;
            'isDraft|.isDraft') printf '%s\n' true ;;
            'tagName|.tagName') printf '%s\n' "$GITHUB_REF_NAME" ;;
            *) exit 68 ;;
          esac
          ;;
        release:download)
          test "$3" = "$GITHUB_REF_NAME" || exit 69
          test "$4" = "--dir" || exit 70
          mkdir -p "$5"
          cp "$FAKE_DIST_DIR"/* "$5"/
          ;;
        release:edit)
          printf '%s|%s\n' "$GH_TOKEN" "$*" >> "$FAKE_GH_LOG"
          ;;
        api:*)
          test "$3" = "--jq" || exit 71
          case "$4" in
            .object.type)
              case "$2" in
                */git/ref/*) printf '%s\n' tag ;;
                */git/tags/*) printf '%s\n' commit ;;
                *) exit 72 ;;
              esac
              ;;
            .object.sha)
              case "$2" in
                */git/ref/*) printf '%s\n' tag-object ;;
                */git/tags/*) printf '%s\n' "$FAKE_TAG_SHA" ;;
                *) exit 73 ;;
              esac
              ;;
            *) exit 74 ;;
          esac
          ;;
        *) exit 75 ;;
      esac
    SH

    base_env = {
      "BASH_ENV" => nil,
      "ENV" => nil,
      "FAKE_ASSET_MODE" => "complete",
      "FAKE_DIST_DIR" => dist,
      "FAKE_GH_LOG" => gh_log,
      "FAKE_TAG_SHA" => "expected-head",
      "GH_TOKEN" => "trusted-job-token",
      "GITHUB_REF_NAME" => "v0.5.0-rc.1",
      "GITHUB_REPOSITORY" => "owner/repo",
      "PATH" => "#{fake_bin}:/usr/bin:/bin",
      "TMPDIR" => runtime,
    }

    _stdout, stderr, status = run_github_bash(draft_run, base_env, root)
    unless status.success?
      raise "approved draft verification failed with controlled valid release data: #{stderr}"
    end

    _stdout, _stderr, asset_status = run_github_bash(
      draft_run,
      base_env.merge("FAKE_ASSET_MODE" => "missing"),
      root,
    )
    unless asset_status.exitstatus == 1
      raise "draft verification must propagate an asset-set assertion failure"
    end

    _stdout, _stderr, tag_status = run_github_bash(
      draft_run,
      base_env.merge("FAKE_TAG_SHA" => "wrong-head"),
      root,
    )
    unless tag_status.exitstatus == 1
      raise "draft verification must propagate a Tag-to-HEAD assertion failure"
    end

    File.write(gh_log, "")
    _stdout, stderr, rc_status = run_github_bash(promotion_run, base_env, root)
    unless rc_status.success?
      raise "approved RC promotion failed with controlled gh: #{stderr}"
    end
    expected_rc = "trusted-job-token|release edit v0.5.0-rc.1 --draft=false --prerelease\n"
    unless File.read(gh_log) == expected_rc
      raise "RC promotion must preserve the job token and set prerelease state"
    end

    File.write(gh_log, "")
    stable_env = base_env.merge("GITHUB_REF_NAME" => "v0.5.0")
    _stdout, stderr, stable_status = run_github_bash(promotion_run, stable_env, root)
    unless stable_status.success?
      raise "approved stable promotion failed with controlled gh: #{stderr}"
    end
    expected_stable = "trusted-job-token|release edit v0.5.0 --draft=false --prerelease=false --latest\n"
    unless File.read(gh_log) == expected_stable
      raise "stable promotion must preserve the job token and set Latest"
    end
  end
end

begin
  verify_release_status(workflow)
  verify_release_status_behavior(workflow)
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
  "release promotion run script must match approved template",
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
  "release promotion run script must match approved template",
)

rc_latest_fixture = Marshal.load(Marshal.dump(workflow))
rc_latest_run = release_step(rc_latest_fixture, "Promote verified release").fetch("run")
rc_latest_run.sub!("--draft=false --prerelease ;;", "--draft=false --prerelease --latest ;;")
expect_release_status_failure(
  rc_latest_fixture,
  "RC marked Latest",
  "release promotion run script must match approved template",
)

rc_not_prerelease_fixture = Marshal.load(Marshal.dump(workflow))
rc_not_prerelease_run = release_step(rc_not_prerelease_fixture, "Promote verified release").fetch("run")
rc_not_prerelease_run.sub!("--draft=false --prerelease ;;", "--draft=false ;;")
expect_release_status_failure(
  rc_not_prerelease_fixture,
  "RC missing prerelease",
  "release promotion run script must match approved template",
)

asset_count_fixture = Marshal.load(Marshal.dump(workflow))
asset_count_run = release_step(asset_count_fixture, "Verify draft asset set").fetch("run")
asset_count_run.sub!("-eq 9", "-eq 8")
expect_release_status_failure(
  asset_count_fixture,
  "eight draft assets",
  "draft verification run script must match approved template",
)

extra_target_fixture = Marshal.load(Marshal.dump(workflow))
extra_target_run = release_step(extra_target_fixture, "Verify draft asset set").fetch("run")
extra_target_run.sub!("linux_arm64; do", "linux_arm64 windows_amd64; do")
expect_release_status_failure(
  extra_target_fixture,
  "extra draft target",
  "draft verification run script must match approved template",
)

missing_asset_diff_fixture = Marshal.load(Marshal.dump(workflow))
missing_asset_diff_run = release_step(missing_asset_diff_fixture, "Verify draft asset set").fetch("run")
missing_asset_diff_run.sub!("diff -u expected-assets.txt actual-assets.txt", "true")
expect_release_status_failure(
  missing_asset_diff_fixture,
  "missing exact asset set comparison",
  "draft verification run script must match approved template",
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
  "draft verification run script must match approved template",
)

wrong_tag_target_fixture = Marshal.load(Marshal.dump(workflow))
wrong_tag_target_run = release_step(wrong_tag_target_fixture, "Verify draft asset set").fetch("run")
wrong_tag_target_run.sub!("git rev-parse HEAD)", "git rev-parse HEAD^)")
expect_release_status_failure(
  wrong_tag_target_fixture,
  "annotated Tag does not target HEAD",
  "draft verification run script must match approved template",
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
  "draft verification run script must match approved template",
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
  "draft verification run script must match approved template",
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
  "draft verification run script must match approved template",
)

dynamic_second_trap_fixture = Marshal.load(Marshal.dump(workflow))
dynamic_second_trap_run = release_step(dynamic_second_trap_fixture, "Verify draft asset set").fetch("run")
dynamic_second_trap_run.prepend(<<~SHELL)
  trap_name=tr
  trap_name="${trap_name}ap"
  "$trap_name" 'exit 0' EXIT
SHELL
expect_release_status_failure(
  dynamic_second_trap_fixture,
  "dynamically registers a second EXIT trap",
  "draft verification run script must match approved template",
)
RUBY

printf '%s\n' 'check-release-version tests passed'
