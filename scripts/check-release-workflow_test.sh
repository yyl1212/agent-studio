#!/bin/sh
set -eu

script_dir=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
repo_root=$(git -C "$script_dir/.." rev-parse --show-toplevel)

ruby - "$repo_root/.github/workflows/release.yml" <<'RUBY'
require "digest"
require "fileutils"
require "open3"
require "tmpdir"
require "yaml"

# Bash dynamic evaluation cannot be proven safe with a token denylist. These
# digests are the explicit review boundary; the behavior tests below execute
# the approved scripts against controlled external commands.
APPROVED_MAIN_HEAD_SHA256 = "5db480b403882a20af5fff2bc543b2635d64b366c568ffceac305eab8f4f1f3c"
APPROVED_MAIN_CI_GATE_SHA256 = "56b924ec1fe7949aba9556647573d9bc583a23fc67e16eaf4da9199dad2516b0"
APPROVED_PUBLISH_RUN_SHA256 = {
  "Reverify artifact collection" => "4fe84387c3b0db2af7b97a34e88da183f245c41244be2c11ea88163972656ee2",
  "Assert release does not exist" => "f1b2f39fa369f394d1205273664a5eba51d5aede67653a63600981af8813608f",
  "Create draft and upload tested assets" => "f2357f70fd8eac20e0f51c9aa62e8253c48ee0092aa70efdbe04b37b4e93c8ee",
  "Verify draft asset set" => "307aebb80ce1feeeac9b96b3d4bdbb2ae2e84da91bae121f72e4f30f60d0936a",
  "Promote verified release" => "6e9fe085732fdf10dcc87dbed2adf489bd59bae3a41a9bde2d658c2b3b8c6454",
  "Verify published release state" => "5e8cc76f28f04869edaac8ac00f569d2a041d03ee01541af4994497ea81e406b",
  "Verify immutable release" => "45c28156778dce2de987147bbaeb7353c0c3559cd637ade1659e7ab52475202d",
}.freeze
APPROVED_PUBLISH_RUN_ENV = {
  "Reverify artifact collection" => {
    "ARTIFACT_VERSION" => "${{ needs.build.outputs.artifact_version }}",
  },
}.freeze
APPROVED_BUILD_JOB_CONTEXT = {
  "name" => "Build release artifacts",
  "runs-on" => "ubuntu-24.04",
  "timeout-minutes" => 30,
  "permissions" => {"contents" => "read", "actions" => "read"},
  "env" => {"CGO_ENABLED" => "0"},
  "outputs" => {"artifact_version" => "${{ steps.version.outputs.value }}"},
}.freeze
APPROVED_BUILD_STEPS = [
  {
    "name" => "Checkout",
    "uses" => "actions/checkout@3d3c42e5aac5ba805825da76410c181273ba90b1",
    "with" => {"fetch-depth" => 0},
  },
  {
    "name" => "Verify main head",
    "run-sha256" => APPROVED_MAIN_HEAD_SHA256,
  },
  {
    "name" => "Verify successful main CI",
    "env" => {"GH_TOKEN" => "${{ github.token }}"},
    "run-sha256" => APPROVED_MAIN_CI_GATE_SHA256,
  },
  {
    "name" => "Verify annotated release tag",
    "if" => "github.event_name == 'push' && startsWith(github.ref, 'refs/tags/')",
    "run-sha256" => "47aeef875b95e76f05ce6debc19ba2f7c4a9328b5e77df38264f97f44ff0d013",
  },
  {
    "name" => "Select tag artifact version",
    "id" => "tag-version",
    "if" => "github.event_name == 'push' && startsWith(github.ref, 'refs/tags/')",
    "run-sha256" => "a3f6743911c643339eeccad62e5bb4753550156eb33129b86b88df2aef5d057e",
  },
  {
    "name" => "Select dry-run artifact version",
    "id" => "dry-version",
    "if" => "github.event_name == 'workflow_dispatch'",
    "run-sha256" => "f52ad9d09d8651c847e65a32971683c416019a972ea5517c2d3cc4ee24be8895",
  },
  {
    "name" => "Export artifact version",
    "id" => "version",
    "env" => {
      "TAG_VERSION" => "${{ steps.tag-version.outputs.value }}",
      "DRY_VERSION" => "${{ steps.dry-version.outputs.value }}",
    },
    "run-sha256" => "fc8dea07375d8b74f0c37763480c0f05d0aa105281e06d36afc6d9158a62a9c0",
  },
  {
    "name" => "Set up Go",
    "uses" => "actions/setup-go@b7ad1dad31e06c5925ef5d2fc7ad053ef454303e",
    "with" => {"go-version-file" => "go.mod", "cache" => true},
  },
  {
    "name" => "Set up pnpm",
    "uses" => "pnpm/action-setup@0977fd99725f1db4007ccb2928dbb4e90d06cc86",
    "with" => {"version" => "10.34.5", "run_install" => false},
  },
  {
    "name" => "Set up Node.js",
    "uses" => "actions/setup-node@820762786026740c76f36085b0efc47a31fe5020",
    "with" => {
      "node-version" => "24",
      "cache" => "pnpm",
      "cache-dependency-path" => "pnpm-lock.yaml",
    },
  },
  {
    "name" => "Enable Corepack",
    "run-sha256" => "9a1f651215eb36db76b2012213dd57b363c495f93b0f3ff9e38cdc8a675d4233",
  },
  {
    "name" => "Install dependencies",
    "run-sha256" => "e934b31610059a0aa1e337ab20d7b0c94d40fe7c91163f01b175d21bb36d31ab",
  },
  {
    "name" => "Verify source",
    "run-sha256" => "5abcc14b19b6b9e6387111efea90a75867af38d70b788c2b78c95d4783755540",
  },
  {
    "name" => "Download Syft",
    "id" => "syft",
    "uses" => "anchore/sbom-action/download-syft@e22c389904149dbc22b58101806040fa8d37a610",
    "with" => {"syft-version" => "v1.51.0"},
  },
  {
    "name" => "Add Syft to PATH",
    "env" => {"SYFT_CMD" => "${{ steps.syft.outputs.cmd }}"},
    "run-sha256" => "df9f18357fb0878d9bbdd002c245021d062dd18f71a20b01f43ba2fd1d79199e",
  },
  {
    "name" => "Build tagged artifacts",
    "if" => "github.event_name == 'push' && startsWith(github.ref, 'refs/tags/')",
    "env" => {"GORELEASER_CURRENT_TAG" => "${{ github.ref_name }}"},
    "uses" => "goreleaser/goreleaser-action@f06c13b6b1a9625abc9e6e439d9c05a8f2190e94",
    "with" => {
      "distribution" => "goreleaser",
      "version" => "v2.17.1",
      "args" => "release --clean --skip=publish",
    },
  },
  {
    "name" => "Build dry-run artifacts",
    "if" => "github.event_name == 'workflow_dispatch'",
    "uses" => "goreleaser/goreleaser-action@f06c13b6b1a9625abc9e6e439d9c05a8f2190e94",
    "with" => {
      "distribution" => "goreleaser",
      "version" => "v2.17.1",
      "args" => "release --clean --snapshot --skip=publish",
    },
  },
  {
    "name" => "Verify artifact collection",
    "env" => {"ARTIFACT_VERSION" => "${{ steps.version.outputs.value }}"},
    "run-sha256" => APPROVED_PUBLISH_RUN_SHA256.fetch("Reverify artifact collection"),
  },
  {
    "name" => "Upload release candidate artifacts",
    "uses" => "actions/upload-artifact@043fb46d1a93c77aae656e7c1c64a875d1fc6a0a",
    "with" => {
      "name" => "release-dist",
      "if-no-files-found" => "error",
      "retention-days" => 7,
      "path" => "dist/*.tar.gz\ndist/*.spdx.json\ndist/checksums.txt\n",
    },
  },
].freeze
APPROVED_PUBLISH_JOB_CONTEXT = {
  "name" => "Publish verified release",
  "if" => "github.event_name == 'push' && startsWith(github.ref, 'refs/tags/')",
  "needs" => ["build", "smoke"],
  "runs-on" => "ubuntu-24.04",
  "timeout-minutes" => 15,
  "permissions" => {"contents" => "write"},
  "env" => {"CGO_ENABLED" => "0", "GH_TOKEN" => "${{ github.token }}"},
}.freeze
APPROVED_PUBLISH_STEPS = [
  {
    "name" => "Checkout",
    "uses" => "actions/checkout@3d3c42e5aac5ba805825da76410c181273ba90b1",
    "with" => {"fetch-depth" => 0, "ref" => "${{ github.sha }}"},
  },
  {
    "name" => "Set up Go",
    "uses" => "actions/setup-go@b7ad1dad31e06c5925ef5d2fc7ad053ef454303e",
    "with" => {"go-version-file" => "go.mod", "cache" => true},
  },
  {
    "name" => "Download verified release artifacts",
    "uses" => "actions/download-artifact@3e5f45b2cfb9172054b4087a40e8e0b5a5461e7c",
    "with" => {"name" => "release-dist", "path" => "dist"},
  },
  {
    "name" => "Reverify artifact collection",
    "env" => APPROVED_PUBLISH_RUN_ENV.fetch("Reverify artifact collection"),
    "run-sha256" => APPROVED_PUBLISH_RUN_SHA256.fetch("Reverify artifact collection"),
  },
  {
    "name" => "Assert release does not exist",
    "run-sha256" => APPROVED_PUBLISH_RUN_SHA256.fetch("Assert release does not exist"),
  },
  {
    "name" => "Create draft and upload tested assets",
    "run-sha256" => APPROVED_PUBLISH_RUN_SHA256.fetch("Create draft and upload tested assets"),
  },
  {
    "name" => "Verify draft asset set",
    "run-sha256" => APPROVED_PUBLISH_RUN_SHA256.fetch("Verify draft asset set"),
  },
  {
    "name" => "Promote verified release",
    "run-sha256" => APPROVED_PUBLISH_RUN_SHA256.fetch("Promote verified release"),
  },
  {
    "name" => "Verify published release state",
    "run-sha256" => APPROVED_PUBLISH_RUN_SHA256.fetch("Verify published release state"),
  },
  {
    "name" => "Verify immutable release",
    "run-sha256" => APPROVED_PUBLISH_RUN_SHA256.fetch("Verify immutable release"),
  },
].freeze

workflow_path = ARGV.fetch(0)
workflow = YAML.safe_load(File.read(workflow_path), aliases: true)

def job_context_matches?(job, approved_context)
  approved_keys = (approved_context.keys + ["steps"]).sort
  job.keys.sort == approved_keys && job.reject { |key, _value| key == "steps" } == approved_context
end

def step_signatures(steps)
  steps.map do |step|
    signature = step.dup
    run = signature.delete("run")
    signature["run-sha256"] = Digest::SHA256.hexdigest(run) if run
    signature
  end
end

def verify_main_ci_gate(workflow)
  unless workflow.fetch("permissions", {}) == {"contents" => "read"}
    raise "workflow permissions must be exactly contents: read"
  end
  unless workflow.fetch("env", {}).empty? && !workflow.key?("defaults")
    raise "main CI gate execution context must match approved template"
  end

  build_job = workflow.fetch("jobs").fetch("build")
  if build_job.key?("if")
    raise "release build job must run for every release trigger"
  end
  if build_job.key?("continue-on-error")
    raise "release build job must propagate failures"
  end
  unless build_job.fetch("env", {}) == {"CGO_ENABLED" => "0"} && !build_job.key?("defaults")
    raise "main CI gate execution context must match approved template"
  end
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

  approved_prefix = ["Checkout", "Verify main head", "Verify successful main CI"]
  unless build_steps.first(gate_index + 1).map { |step| step.fetch("name", "") } == approved_prefix
    raise "main CI gate execution context must match approved template"
  end
  checkout = build_steps.fetch(0)
  expected_checkout = {
    "name" => "Checkout",
    "uses" => "actions/checkout@3d3c42e5aac5ba805825da76410c181273ba90b1",
    "with" => {"fetch-depth" => 0},
  }
  unless checkout == expected_checkout
    raise "main CI gate execution context must match approved template"
  end
  main_head = build_steps.fetch(1)
  unless main_head.keys.sort == %w[name run] &&
      Digest::SHA256.hexdigest(main_head.fetch("run")) == APPROVED_MAIN_HEAD_SHA256
    raise "main CI gate execution context must match approved template"
  end

  gate = build_steps.fetch(gate_index)
  unless gate.fetch("env", {}) == {"GH_TOKEN" => "${{ github.token }}"}
    raise "main CI gate must use only github.token"
  end
  if gate.key?("if")
    raise "main CI gate must run for every release build"
  end
  if gate.key?("continue-on-error")
    raise "main CI gate must propagate failures"
  end
  unless gate.keys.sort == %w[env name run]
    raise "main CI gate execution context must match approved template"
  end

  run = gate.fetch("run", "")
  unless Digest::SHA256.hexdigest(run) == APPROVED_MAIN_CI_GATE_SHA256
    raise "main CI gate run script must match approved template"
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

  unless job_context_matches?(build_job, APPROVED_BUILD_JOB_CONTEXT)
    raise "build job execution context must match approved template"
  end
  unless step_signatures(build_steps) == APPROVED_BUILD_STEPS
    raise "build steps must match approved templates"
  end
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

def verify_main_ci_gate_behavior(workflow)
  run = workflow.fetch("jobs").fetch("build").fetch("steps")
    .find { |step| step["name"] == "Verify successful main CI" }.fetch("run")

  Dir.mktmpdir("agent-studio-main-ci-gate.") do |root|
    fake_bin = File.join(root, "bin")
    FileUtils.mkdir_p(fake_bin)
    gh_log = File.join(root, "gh.log")
    jq_log = File.join(root, "jq.log")

    write_executable(File.join(fake_bin, "git"), <<~'SH')
      #!/bin/sh
      test "$*" = "rev-parse HEAD" || exit 64
      printf '%s\n' deadbeef
    SH
    write_executable(File.join(fake_bin, "gh"), <<~'SH')
      #!/bin/sh
      expected='api --method GET repos/owner/repo/actions/workflows/ci.yml/runs -f branch=main -f event=push -f status=completed -f per_page=100'
      printf '%s|%s\n' "$GH_TOKEN" "$*" > "$FAKE_GH_LOG"
      test "$*" = "$expected" || exit 65
      printf '%s\n' '{"workflow_runs":[]}'
    SH
    write_executable(File.join(fake_bin, "jq"), <<~'SH')
      #!/bin/sh
      : > "$FAKE_JQ_LOG"
      for argument do
        printf '%s\n' "$argument" >> "$FAKE_JQ_LOG"
      done
      exit "${FAKE_JQ_STATUS:-0}"
    SH

    base_env = {
      "BASH_ENV" => nil,
      "ENV" => nil,
      "FAKE_GH_LOG" => gh_log,
      "FAKE_JQ_LOG" => jq_log,
      "GH_TOKEN" => "trusted-job-token",
      "GITHUB_REPOSITORY" => "owner/repo",
      "PATH" => "#{fake_bin}:/usr/bin:/bin",
    }
    _stdout, stderr, status = run_github_bash(run, base_env, root)
    unless status.success?
      raise "approved main CI gate failed with controlled successful dependencies: #{stderr}"
    end

    expected_gh = "trusted-job-token|api --method GET repos/owner/repo/actions/workflows/ci.yml/runs " \
      "-f branch=main -f event=push -f status=completed -f per_page=100\n"
    unless File.read(gh_log) == expected_gh
      raise "approved main CI gate emitted an unexpected gh query"
    end
    jq_arguments = File.read(jq_log)
    [
      "-e\n--arg\nsha\ndeadbeef\n",
      ".workflow_runs | any(",
      ".head_sha == $sha",
      ".head_branch == \"main\"",
      ".event == \"push\"",
      ".status == \"completed\"",
      ".conclusion == \"success\"",
    ].each do |fragment|
      raise "approved main CI gate emitted an unexpected jq assertion" unless jq_arguments.include?(fragment)
    end

    _stdout, _stderr, failed_status = run_github_bash(
      run,
      base_env.merge("FAKE_JQ_STATUS" => "42"),
      root,
    )
    unless failed_status.exitstatus == 42
      raise "main CI gate must propagate a failing jq assertion"
    end
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

def verify_release_credentials(workflow)
  publish_job = workflow.fetch("jobs").fetch("publish")
  expected_publish_env = {
    "CGO_ENABLED" => "0",
    "GH_TOKEN" => "${{ github.token }}",
  }
  unless publish_job.fetch("env", {}) == expected_publish_env
    raise "publish env must contain only CGO_ENABLED and github.token"
  end
  if workflow.key?("defaults") || publish_job.key?("defaults") || publish_job.key?("continue-on-error")
    raise "publish run scripts must match approved templates"
  end

  publish_steps = publish_job.fetch("steps")
  publish_steps.each do |step|
    token_env = step.fetch("env", {}).select { |name, _value| name.match?(/token/i) }
    unless token_env.empty? || token_env == {"GH_TOKEN" => "${{ github.token }}"}
      raise "publish steps must not override release token credentials"
    end
  end

  publish_run_steps = publish_steps.select { |step| step.key?("run") }
  unless publish_run_steps.map { |step| step.fetch("name", "") }.sort == APPROVED_PUBLISH_RUN_SHA256.keys.sort
    raise "publish run scripts must match approved templates"
  end
  publish_run_steps.each do |step|
    name = step.fetch("name")
    expected_digest = APPROVED_PUBLISH_RUN_SHA256.fetch(name)
    expected_env = APPROVED_PUBLISH_RUN_ENV.fetch(name, {})
    expected_keys = expected_env.empty? ? %w[name run] : %w[env name run]
    unless step.keys.sort == expected_keys &&
        step.fetch("env", {}) == expected_env &&
        Digest::SHA256.hexdigest(step.fetch("run")) == expected_digest
      raise "publish run scripts must match approved templates"
    end
  end

  unless job_context_matches?(publish_job, APPROVED_PUBLISH_JOB_CONTEXT)
    raise "publish job execution context must match approved template"
  end
  unless step_signatures(publish_steps) == APPROVED_PUBLISH_STEPS
    raise "publish steps must match approved templates"
  end

  pending = [workflow]
  until pending.empty?
    value = pending.pop
    case value
    when Hash
      pending.concat(value.keys)
      pending.concat(value.values)
    when Array
      pending.concat(value)
    when String
      if value.match?(/\$\{\{\s*secrets\b/)
        raise "release workflow must not use the secrets context"
      end
    end
  end
end

def expect_release_credentials_failure(workflow, fixture, expected)
  verify_release_credentials(workflow)
rescue RuntimeError => error
  unless error.message == expected
    abort "release credentials fixture #{fixture} failed for the wrong reason: #{error.message}"
  end
  return
else
  abort "release credentials fixture was accepted: #{fixture}"
end

def record_expected_failure(failures, workflow, fixture, expected)
  yield workflow
rescue RuntimeError => error
  unless error.message == expected
    failures << "fixture #{fixture} failed for the wrong reason: #{error.message}"
  end
else
  failures << "fixture was accepted: #{fixture}"
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
  verify_main_ci_gate_behavior(workflow)
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
  "main CI gate run script must match approved template",
)

pull_request_fixture = Marshal.load(Marshal.dump(workflow))
pull_request_run = pull_request_fixture.fetch("jobs").fetch("build").fetch("steps")
  .find { |step| step["name"] == "Verify successful main CI" }.fetch("run")
pull_request_run.sub!("event=push", "event=pull_request")
expect_main_ci_gate_failure(
  pull_request_fixture,
  "pull_request event query",
  "main CI gate run script must match approved template",
)

failed_conclusion_fixture = Marshal.load(Marshal.dump(workflow))
failed_conclusion_run = failed_conclusion_fixture.fetch("jobs").fetch("build").fetch("steps")
  .find { |step| step["name"] == "Verify successful main CI" }.fetch("run")
failed_conclusion_run.sub!(".conclusion == \"success\"", ".conclusion == \"failure\"")
expect_main_ci_gate_failure(
  failed_conclusion_fixture,
  "non-success conclusion",
  "main CI gate run script must match approved template",
)

conditional_build_fixture = Marshal.load(Marshal.dump(workflow))
conditional_build_fixture.fetch("jobs").fetch("build")["if"] = "github.event_name == 'push'"
expect_main_ci_gate_failure(
  conditional_build_fixture,
  "push-only build job",
  "release build job must run for every release trigger",
)

tolerant_build_fixture = Marshal.load(Marshal.dump(workflow))
tolerant_build_fixture.fetch("jobs").fetch("build")["continue-on-error"] = true
expect_main_ci_gate_failure(
  tolerant_build_fixture,
  "continue-on-error build job",
  "release build job must propagate failures",
)

continue_on_error_fixture = Marshal.load(Marshal.dump(workflow))
continue_on_error_gate = continue_on_error_fixture.fetch("jobs").fetch("build").fetch("steps")
  .find { |step| step["name"] == "Verify successful main CI" }
continue_on_error_gate["continue-on-error"] = true
expect_main_ci_gate_failure(
  continue_on_error_fixture,
  "continue-on-error gate",
  "main CI gate must propagate failures",
)

masked_jq_fixture = Marshal.load(Marshal.dump(workflow))
masked_jq_run = masked_jq_fixture.fetch("jobs").fetch("build").fetch("steps")
  .find { |step| step["name"] == "Verify successful main CI" }.fetch("run")
unless masked_jq_run.sub!("' \"$ci_runs\" >/dev/null", "' \"$ci_runs\" >/dev/null || true")
  abort "failed to construct masked jq fixture"
end
expect_main_ci_gate_failure(
  masked_jq_fixture,
  "masked jq failure",
  "main CI gate run script must match approved template",
)

negated_jq_fixture = Marshal.load(Marshal.dump(workflow))
negated_jq_run = negated_jq_fixture.fetch("jobs").fetch("build").fetch("steps")
  .find { |step| step["name"] == "Verify successful main CI" }.fetch("run")
unless negated_jq_run.sub!("jq -e", "! jq -e")
  abort "failed to construct negated jq fixture"
end
expect_main_ci_gate_failure(
  negated_jq_fixture,
  "negated jq assertion",
  "main CI gate run script must match approved template",
)

shadowed_jq_fixture = Marshal.load(Marshal.dump(workflow))
shadowed_jq_run = shadowed_jq_fixture.fetch("jobs").fetch("build").fetch("steps")
  .find { |step| step["name"] == "Verify successful main CI" }.fetch("run")
unless shadowed_jq_run.sub!(
    "head_sha=$(git rev-parse HEAD)",
    "jq(){ return 0; }\nhead_sha=$(git rev-parse HEAD)",
  )
  abort "failed to construct shadowed jq fixture"
end
expect_main_ci_gate_failure(
  shadowed_jq_fixture,
  "jq function shadows external command",
  "main CI gate run script must match approved template",
)

dynamically_shadowed_jq_fixture = Marshal.load(Marshal.dump(workflow))
dynamically_shadowed_jq_run = dynamically_shadowed_jq_fixture.fetch("jobs").fetch("build").fetch("steps")
  .find { |step| step["name"] == "Verify successful main CI" }.fetch("run")
unless dynamically_shadowed_jq_run.sub!(
    "head_sha=$(git rev-parse HEAD)",
    "x=eval; $x \"jq(){ :; }\"\nhead_sha=$(git rev-parse HEAD)",
  )
  abort "failed to construct dynamically shadowed jq fixture"
end
expect_main_ci_gate_failure(
  dynamically_shadowed_jq_fixture,
  "dynamically evaluated jq function",
  "main CI gate run script must match approved template",
)

aliased_jq_fixture = Marshal.load(Marshal.dump(workflow))
aliased_jq_run = aliased_jq_fixture.fetch("jobs").fetch("build").fetch("steps")
  .find { |step| step["name"] == "Verify successful main CI" }.fetch("run")
aliased_jq_run.prepend("alias jq='true'\n")
expect_main_ci_gate_failure(
  aliased_jq_fixture,
  "jq alias shadows external command",
  "main CI gate run script must match approved template",
)

wrapped_jq_fixture = Marshal.load(Marshal.dump(workflow))
wrapped_jq_run = wrapped_jq_fixture.fetch("jobs").fetch("build").fetch("steps")
  .find { |step| step["name"] == "Verify successful main CI" }.fetch("run")
unless wrapped_jq_run.sub!("jq -e", "command jq -e")
  abort "failed to construct wrapped jq fixture"
end
expect_main_ci_gate_failure(
  wrapped_jq_fixture,
  "command wrapper invokes jq",
  "main CI gate run script must match approved template",
)

early_exit_fixture = Marshal.load(Marshal.dump(workflow))
early_exit_run = early_exit_fixture.fetch("jobs").fetch("build").fetch("steps")
  .find { |step| step["name"] == "Verify successful main CI" }.fetch("run")
early_exit_run.prepend("exit 0\n")
expect_main_ci_gate_failure(
  early_exit_fixture,
  "gate exits before jq",
  "main CI gate run script must match approved template",
)

masked_gate_trap_fixture = Marshal.load(Marshal.dump(workflow))
masked_gate_trap_run = masked_gate_trap_fixture.fetch("jobs").fetch("build").fetch("steps")
  .find { |step| step["name"] == "Verify successful main CI" }.fetch("run")
safe_gate_trap = "trap 'rm -f \"$ci_runs\"' EXIT HUP INT TERM"
unless masked_gate_trap_run.sub!(safe_gate_trap, "trap 'exit 0' EXIT HUP INT TERM")
  abort "failed to construct masked gate EXIT trap fixture"
end
expect_main_ci_gate_failure(
  masked_gate_trap_fixture,
  "EXIT trap masks jq failure",
  "main CI gate run script must match approved template",
)

bracket_secret_fixture = Marshal.load(Marshal.dump(workflow))
bracket_secret_step = bracket_secret_fixture.fetch("jobs").fetch("build").fetch("steps")
  .find { |step| step["name"] == "Checkout" }
bracket_secret_step["env"] = {"RELEASE_TOKEN" => "${{ secrets['RELEASE_TOKEN'] }}"}
expect_release_credentials_failure(
  bracket_secret_fixture,
  "bracket secrets context",
  "release workflow must not use the secrets context",
)

dot_secret_fixture = Marshal.load(Marshal.dump(workflow))
dot_secret_step = dot_secret_fixture.fetch("jobs").fetch("build").fetch("steps")
  .find { |step| step["name"] == "Checkout" }
dot_secret_step["env"] = {"RELEASE_TOKEN" => "${{ secrets.RELEASE_TOKEN }}"}
expect_release_credentials_failure(
  dot_secret_fixture,
  "dot secrets context",
  "release workflow must not use the secrets context",
)

extra_publish_token_fixture = Marshal.load(Marshal.dump(workflow))
extra_publish_token_fixture.fetch("jobs").fetch("publish").fetch("env")["RELEASE_TOKEN"] =
  "${{ github.token }}"
expect_release_credentials_failure(
  extra_publish_token_fixture,
  "extra publish token environment variable",
  "publish env must contain only CGO_ENABLED and github.token",
)

step_token_override_fixture = Marshal.load(Marshal.dump(workflow))
step_token_override = step_token_override_fixture.fetch("jobs").fetch("publish").fetch("steps")
  .find { |step| step["name"] == "Promote verified release" }
step_token_override["env"] = {"GH_TOKEN" => "${{ vars.RELEASE_TOKEN }}"}
expect_release_credentials_failure(
  step_token_override_fixture,
  "publish step overrides github.token",
  "publish steps must not override release token credentials",
)

run_token_override_fixture = Marshal.load(Marshal.dump(workflow))
run_token_override = run_token_override_fixture.fetch("jobs").fetch("publish").fetch("steps")
  .find { |step| step["name"] == "Promote verified release" }
run_token_override["run"] =
  "export GH_TOKEN=\"${{ vars.RELEASE_TOKEN }}\"\n#{run_token_override.fetch("run")}"
expect_release_credentials_failure(
  run_token_override_fixture,
  "publish run exports vars token",
  "publish run scripts must match approved templates",
)

unset_token_fixture = Marshal.load(Marshal.dump(workflow))
unset_token_step = unset_token_fixture.fetch("jobs").fetch("publish").fetch("steps")
  .find { |step| step["name"] == "Promote verified release" }
unset_token_step["run"] = "unset GH_TOKEN\n#{unset_token_step.fetch("run")}"
expect_release_credentials_failure(
  unset_token_fixture,
  "publish run unsets inherited token",
  "publish run scripts must match approved templates",
)

dynamic_token_override_fixture = Marshal.load(Marshal.dump(workflow))
dynamic_token_override_step = dynamic_token_override_fixture.fetch("jobs").fetch("publish").fetch("steps")
  .find { |step| step["name"] == "Promote verified release" }
dynamic_token_override_step["run"] = <<~SHELL + dynamic_token_override_step.fetch("run")
  a=ex
  a="${a}port"
  b=GH_
  b="${b}TO"
  b="${b}KEN"
  "$a" "$b=attacker"
SHELL
expect_release_credentials_failure(
  dynamic_token_override_fixture,
  "publish run dynamically exports GH_TOKEN",
  "publish run scripts must match approved templates",
)

round5_failures = []

build_container_env_fixture = Marshal.load(Marshal.dump(workflow))
build_container_env_fixture.fetch("jobs").fetch("build")["container"] = {
  "image" => "ubuntu:24.04",
  "env" => {"BASH_ENV" => "/github/workspace/scripts/attacker-build-env"},
}
record_expected_failure(
  round5_failures,
  build_container_env_fixture,
  "build container BASH_ENV shadows jq",
  "build job execution context must match approved template",
) { |fixture| verify_main_ci_gate(fixture) }

publish_container_env_fixture = Marshal.load(Marshal.dump(workflow))
publish_container_env_fixture.fetch("jobs").fetch("publish")["container"] = {
  "image" => "ubuntu:24.04",
  "env" => {"BASH_ENV" => "/github/workspace/scripts/attacker-publish-env"},
}
record_expected_failure(
  round5_failures,
  publish_container_env_fixture,
  "publish container BASH_ENV shadows trap",
  "publish job execution context must match approved template",
) { |fixture| verify_release_credentials(fixture) }

publish_github_env_fixture = Marshal.load(Marshal.dump(workflow))
publish_github_env_fixture.fetch("jobs").fetch("publish").fetch("steps").insert(
  3,
  {
    "name" => "Override publish credentials",
    "uses" => "actions/github-script@60a0d83039c74a4aee543508d2ffcb1c3799cdea",
    "with" => {
      "script" => "require('node:fs').appendFileSync(process.env.GITHUB_ENV, 'GH_TOKEN=attacker\\n')",
    },
  },
)
record_expected_failure(
  round5_failures,
  publish_github_env_fixture,
  "extra uses step writes GH_TOKEN to GITHUB_ENV",
  "publish steps must match approved templates",
) { |fixture| verify_release_credentials(fixture) }

abort round5_failures.join("\n") unless round5_failures.empty?

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
begin
  verify_release_credentials(workflow)
rescue RuntimeError => error
  abort "release workflow contract violation: #{error.message}"
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
