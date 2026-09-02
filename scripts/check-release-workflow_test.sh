#!/bin/sh
set -eu

script_dir=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
repo_root=$(git -C "$script_dir/.." rev-parse --show-toplevel)

ruby - "$repo_root/.github/workflows/release.yml" <<'RUBY'
require "yaml"

workflow_path = ARGV.fetch(0)
workflow = YAML.safe_load(File.read(workflow_path), aliases: true)

def verify_main_ci_gate(workflow)
  unless workflow.fetch("permissions", {}) == {"contents" => "read"}
    raise "workflow permissions must be exactly contents: read"
  end

  build_job = workflow.fetch("jobs").fetch("build")
  build_permissions = build_job.fetch("permissions", {})
  unless build_permissions.fetch("actions", nil) == "read"
    raise "build actions permission must be read"
  end
  unless build_permissions.fetch("contents", nil) == "read"
    raise "build contents permission must be read"
  end
  unless build_permissions.keys.sort == %w[actions contents]
    raise "build permissions must contain only contents and actions"
  end

  build_steps = build_job.fetch("steps")
  step_names = build_steps.map { |step| step.fetch("name", "") }
  gate_index = step_names.index("Verify successful main CI")
  raise "missing Verify successful main CI step" unless gate_index

  gate = build_steps.fetch(gate_index)
  unless gate.fetch("env", {}) == {"GH_TOKEN" => "${{ github.token }}"}
    raise "main CI gate must use only github.token"
  end
  if gate.key?("if")
    raise "main CI gate must run for every release build"
  end

  run = gate.fetch("run", "")
  required_fragments = {
    "head_sha=$(git rev-parse HEAD)" => "main CI gate must derive the exact release SHA from HEAD",
    "actions/workflows/ci.yml/runs" => "main CI gate must query the ci.yml workflow runs endpoint",
    "--method GET" => "main CI gate must query workflow runs with GET",
    "branch=main" => "main CI gate must query only main runs",
    "event=push" => "main CI gate must query only push runs",
    "status=completed" => "main CI gate must query only completed runs",
    "--arg sha \"$head_sha\"" => "main CI gate must pass the release SHA to jq",
    ".head_sha == $sha" => "main CI gate must require the exact release SHA",
    ".head_branch == \"main\"" => "main CI gate must require the main branch",
    ".event == \"push\"" => "main CI gate must require a push event",
    ".status == \"completed\"" => "main CI gate must require completed status",
    ".conclusion == \"success\"" => "main CI gate must require a successful conclusion",
  }
  required_fragments.each do |fragment, message|
    raise message unless run.include?(fragment)
  end

  before_gate = ["Verify main head"]
  after_gate = ["Verify source", "Build tagged artifacts", "Build dry-run artifacts"]
  before_gate.each do |name|
    index = step_names.index(name)
    raise "missing build step: #{name}" unless index
    raise "main CI gate must follow #{name}" unless index < gate_index
  end
  after_gate.each do |name|
    index = step_names.index(name)
    raise "missing build step: #{name}" unless index
    raise "main CI gate must precede #{name}" unless gate_index < index
  end
end

def expect_main_ci_gate_failure(workflow, fixture, expected)
  verify_main_ci_gate(workflow)
rescue RuntimeError => error
  unless error.message == expected
    abort "main CI gate fixture #{fixture} failed for the wrong reason: #{error.message}"
  end
  return
else
  abort "main CI gate fixture was accepted: #{fixture}"
end

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

begin
  verify_main_ci_gate(workflow)
rescue RuntimeError => error
  abort "release workflow contract violation: #{error.message}"
end

missing_actions_fixture = Marshal.load(Marshal.dump(workflow))
missing_actions_fixture.fetch("jobs").fetch("build").fetch("permissions").delete("actions")
expect_main_ci_gate_failure(
  missing_actions_fixture,
  "missing actions: read",
  "build actions permission must be read",
)

write_contents_fixture = Marshal.load(Marshal.dump(workflow))
write_contents_fixture.fetch("jobs").fetch("build").fetch("permissions")["contents"] = "write"
expect_main_ci_gate_failure(
  write_contents_fixture,
  "expanded contents: write",
  "build contents permission must be read",
)

missing_sha_fixture = Marshal.load(Marshal.dump(workflow))
missing_sha_run = missing_sha_fixture.fetch("jobs").fetch("build").fetch("steps")
  .find { |step| step["name"] == "Verify successful main CI" }.fetch("run")
missing_sha_run.sub!(".head_sha == $sha", ".head_sha != $sha")
expect_main_ci_gate_failure(
  missing_sha_fixture,
  "missing exact SHA predicate",
  "main CI gate must require the exact release SHA",
)

pull_request_fixture = Marshal.load(Marshal.dump(workflow))
pull_request_run = pull_request_fixture.fetch("jobs").fetch("build").fetch("steps")
  .find { |step| step["name"] == "Verify successful main CI" }.fetch("run")
pull_request_run.sub!("event=push", "event=pull_request")
expect_main_ci_gate_failure(
  pull_request_fixture,
  "pull_request event query",
  "main CI gate must query only push runs",
)

failed_conclusion_fixture = Marshal.load(Marshal.dump(workflow))
failed_conclusion_run = failed_conclusion_fixture.fetch("jobs").fetch("build").fetch("steps")
  .find { |step| step["name"] == "Verify successful main CI" }.fetch("run")
failed_conclusion_run.sub!(".conclusion == \"success\"", ".conclusion == \"failure\"")
expect_main_ci_gate_failure(
  failed_conclusion_fixture,
  "non-success conclusion",
  "main CI gate must require a successful conclusion",
)

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
if workflow.fetch(true, {}).key?("pull_request_target")
  abort "release workflow contract violation: release workflow must not use pull_request_target"
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
