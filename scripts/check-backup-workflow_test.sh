#!/bin/sh
set -eu

workflow_path=${1:-.github/workflows/ci.yml}

ruby -ryaml -e '
  workflow = YAML.safe_load(File.read(ARGV.fetch(0)), aliases: true)
  job = workflow.fetch("jobs").fetch("backup-recovery")
  raise "wrong name" unless job.fetch("name") == "Backup recovery"
  raise "wrong runner" unless job.fetch("runs-on") == "ubuntu-latest"
  raise "wrong timeout" unless job.fetch("timeout-minutes") == 15
  service = job.fetch("services").fetch("postgres")
  raise "postgres image not pinned" unless service.fetch("image") == "postgres:18"
  env = job.fetch("env")
  raise "CGO disabled missing" unless env.fetch("CGO_ENABLED") == "0"
  steps = job.fetch("steps")
  raise "unexpected backup recovery step count" unless steps.length == 3
  runs = steps.map { |step| step["run"] }.compact
  raise "backup e2e missing" unless runs.include?("make test-backup-e2e")
  actions = steps.map { |step| step["uses"] }.compact.map { |uses| uses.split("@", 2).first }
  raise "unexpected backup recovery actions: #{actions.inspect}" unless actions == ["actions/checkout", "actions/setup-go"]
  raise "unexpected backup recovery run steps: #{runs.inspect}" unless runs == ["make test-backup-e2e"]
  checkout, setup_go, backup_e2e = steps
  raise "checkout step is malformed" unless checkout.fetch("uses").start_with?("actions/checkout@") && !checkout.key?("run")
  raise "setup-go step is malformed" unless setup_go.fetch("uses").start_with?("actions/setup-go@") && !setup_go.key?("run")
  raise "setup-go must use go.mod" unless setup_go.fetch("with").fetch("go-version-file") == "go.mod"
  raise "backup e2e step is malformed" unless backup_e2e.fetch("run") == "make test-backup-e2e" && !backup_e2e.key?("uses")
' "$workflow_path"

ruby -ryaml -e '
  def collect_key_paths(value, wanted, path = [], found = [])
    case value
    when Hash
      value.each do |key, child|
        child_path = path + [key.to_s]
        found << [child_path, child] if key.to_s == wanted
        collect_key_paths(child, wanted, child_path, found)
      end
    when Array
      value.each_with_index do |child, index|
        collect_key_paths(child, wanted, path + [index.to_s], found)
      end
    end
    found
  end

  workflow = YAML.safe_load(File.read(ARGV.fetch(0)), aliases: true)
  raise "permissions must be contents: read" unless workflow.fetch("permissions") == {"contents" => "read"}
  job = workflow.fetch("jobs").fetch("backup-recovery")
  url = job.fetch("env").fetch("TEST_DATABASE_URL")
  raise "database URL must use localhost" unless url.include?("@localhost:5432/")
  raise "database URL leaked through a run step" if job.fetch("steps").any? { |step| step.fetch("run", "").include?(url) }
  url_paths = collect_key_paths(workflow, "TEST_DATABASE_URL").map(&:first)
  expected_url_paths = [["jobs", "backup-recovery", "env", "TEST_DATABASE_URL"]]
  raise "TEST_DATABASE_URL must exist only in backup recovery job env: #{url_paths.inspect}" unless url_paths == expected_url_paths
  collect_key_paths(workflow, "uses").each do |_path, uses|
    raise "action is not pinned by full commit SHA: #{uses}" unless uses.is_a?(String) && uses.match?(/\A[^@\s]+@[0-9a-f]{40}\z/)
  end
' "$workflow_path"

if [ "${BACKUP_WORKFLOW_MUTATION_CHILD:-0}" = "1" ]; then
  exit 0
fi

mutation_dir=$(mktemp -d)
trap 'rm -rf "$mutation_dir"' EXIT HUP INT TERM

ruby -ryaml -e '
  source = YAML.safe_load(File.read(ARGV.fetch(0)), aliases: true)
  output_dir = ARGV.fetch(1)
  clone = -> { Marshal.load(Marshal.dump(source)) }

  File.write(File.join(output_dir, "control.yml"), YAML.dump(clone.call))

  reusable = clone.call
  reusable.fetch("jobs")["unpinned-reusable"] = {
    "uses" => "owner/repo/.github/workflows/reuse.yml@main"
  }
  File.write(File.join(output_dir, "reusable-job-uses.yml"), YAML.dump(reusable))

  missing_go_version = clone.call
  setup_go = missing_go_version.fetch("jobs").fetch("backup-recovery").fetch("steps").find do |step|
    step.fetch("uses", "").start_with?("actions/setup-go@")
  end
  setup_go.delete("with")
  File.write(File.join(output_dir, "missing-go-version-file.yml"), YAML.dump(missing_go_version))

  duplicate_url = clone.call
  url = duplicate_url.fetch("jobs").fetch("backup-recovery").fetch("env").fetch("TEST_DATABASE_URL")
  duplicate_url["env"] = {"TEST_DATABASE_URL" => url}
  File.write(File.join(output_dir, "duplicate-test-database-url.yml"), YAML.dump(duplicate_url))
' "$workflow_path" "$mutation_dir"

BACKUP_WORKFLOW_MUTATION_CHILD=1 sh "$0" "$mutation_dir/control.yml"

mutation_failures=0
for mutation in reusable-job-uses.yml missing-go-version-file.yml duplicate-test-database-url.yml; do
  if BACKUP_WORKFLOW_MUTATION_CHILD=1 sh "$0" "$mutation_dir/$mutation" >/dev/null 2>&1; then
    printf '%s\n' "workflow mutation accepted: $mutation" >&2
    mutation_failures=1
  fi
done
exit "$mutation_failures"
