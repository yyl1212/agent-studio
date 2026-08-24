#!/bin/sh
set -eu

script_dir=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
repo_root=$(git -C "$script_dir/.." rev-parse --show-toplevel)

ruby - "$repo_root/.github/workflows/release.yml" <<'RUBY'
require "yaml"

workflow_path = ARGV.fetch(0)
workflow = YAML.safe_load(File.read(workflow_path), aliases: true)

def verify_tag_isolation(workflow)
  if workflow.fetch("env", {}).key?("GORELEASER_CURRENT_TAG")
    raise "workflow env must not bind a release tag"
  end

  build_job = workflow.fetch("jobs").fetch("build")
  if build_job.fetch("env", {}).key?("GORELEASER_CURRENT_TAG")
    raise "build job env must not bind a release tag"
  end

  build_steps = build_job.fetch("steps")
  build_steps_by_name = build_steps.to_h { |step| [step.fetch("name", ""), step] }

  tagged_build = build_steps_by_name.fetch("Build tagged artifacts")
  unless tagged_build.fetch("env", {}) == {"GORELEASER_CURRENT_TAG" => "${{ github.ref_name }}"}
    raise "tagged build must bind GoReleaser to github.ref_name"
  end

  dry_run_build = build_steps_by_name.fetch("Build dry-run artifacts")
  if dry_run_build.fetch("env", {}).key?("GORELEASER_CURRENT_TAG")
    raise "dry-run build must not bind a release tag"
  end
end

def expect_tag_isolation_failure(workflow, scope, expected)
  verify_tag_isolation(workflow)
rescue RuntimeError => error
  unless error.message == expected
    abort "release workflow contract test fixture failed for the wrong reason: #{error.message}"
  end
  return
else
  abort "release workflow contract test fixture was accepted: inherited #{scope} GORELEASER_CURRENT_TAG"
end

begin
  verify_tag_isolation(workflow)
rescue RuntimeError => error
  abort "release workflow contract violation: #{error.message}"
end

workflow_env_fixture = Marshal.load(Marshal.dump(workflow))
workflow_env_fixture["env"] = {"GORELEASER_CURRENT_TAG" => "v9.9.9"}
expect_tag_isolation_failure(workflow_env_fixture, "workflow", "workflow env must not bind a release tag")

job_env_fixture = Marshal.load(Marshal.dump(workflow))
job_env_fixture.fetch("jobs").fetch("build")["env"]["GORELEASER_CURRENT_TAG"] = "v9.9.9"
expect_tag_isolation_failure(job_env_fixture, "job", "build job env must not bind a release tag")

publish_steps = workflow.fetch("jobs").fetch("publish").fetch("steps")
publish_job = workflow.fetch("jobs").fetch("publish")
steps_by_name = publish_steps.to_h { |step| [step.fetch("name", ""), step] }

unless publish_job.fetch("permissions", {}) == {"contents" => "write"}
  abort "release workflow contract violation: publish permissions must be exactly contents: write"
end
unless publish_job.fetch("env", {}).fetch("GH_TOKEN", "") == "${{ github.token }}"
  abort "release workflow contract violation: publish must use the built-in github.token"
end
if workflow.to_s.include?("secrets.")
  abort "release workflow contract violation: release workflow must not use a long-lived repository secret"
end

required_order = [
  "Reverify artifact collection",
  "Assert release does not exist",
  "Create draft and upload tested assets",
  "Verify draft asset set",
  "Promote verified release",
  "Verify published release state",
  "Verify immutable release",
]

actual_order = publish_steps.map { |step| step.fetch("name", "") }
positions = required_order.map do |name|
  index = actual_order.index(name)
  abort "release workflow contract violation: missing publish step: #{name}" unless index
  index
end
abort "release workflow contract violation: immutable release gates are out of order" unless positions == positions.sort

if steps_by_name.key?("Verify immutable releases are enabled")
  abort "release workflow contract violation: GITHUB_TOKEN cannot read the repository immutable setting"
end

verification = steps_by_name.fetch("Verify immutable release")
unless verification.fetch("run", "") == "bash scripts/check-release-immutability.sh verify \"$GITHUB_REF_NAME\""
  abort "release workflow contract violation: final verification must run the tested immutability gate"
end
RUBY

printf '%s\n' 'release workflow tests passed'
