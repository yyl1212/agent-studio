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
  raise "postgres port must be localhost 5432" unless service.fetch("ports") == ["5432:5432"]
  expected_service_env = {
    "POSTGRES_DB" => "agent_studio",
    "POSTGRES_USER" => "agent",
    "POSTGRES_PASSWORD" => "agent"
  }
  raise "postgres credentials changed" unless service.fetch("env") == expected_service_env
  expected_health = "--health-cmd \"pg_isready -U agent -d agent_studio\" --health-interval 2s --health-timeout 3s --health-retries 30"
  actual_health = service.fetch("options").split.join(" ")
  raise "postgres health options changed" unless actual_health == expected_health
  env = job.fetch("env")
  raise "CGO disabled missing" unless env.fetch("CGO_ENABLED") == "0"
  raise "external database mode missing" unless env.fetch("EXTERNAL_DB") == "1"
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

scope_probe=$(mktemp)
trap 'rm -f "$scope_probe"' EXIT HUP INT TERM
cat >"$scope_probe" <<'EOF'
.PHONY: non-database-scope-probe

non-database-scope-probe:
	@if [ "$${EXPECTED_URL_STATE:-literal}" = unset ]; then \
		test -z "$${TEST_DATABASE_URL+x}"; \
	else \
		test "$$TEST_DATABASE_URL" = "$$EXPECTED_TEST_DATABASE_URL"; \
	fi

db-up:
	@:

backup-create:
	@test "$$TEST_DATABASE_URL" = "$$EXPECTED_TEST_DATABASE_URL"
EOF

assert_literal_is_not_evaluated() {
  source=$1
  mode=$2
  target=$3
  literal=$4
  dry_run=
  [ "$mode" != dry-run ] || dry_run=-n
  expected_state=literal
  if [ "$source" = command-line ] && [ "$mode" = actual ] && [ "$target" = non-database-scope-probe ]; then
    expected_state=unset
  fi
  set +e
  if [ "$source" = environment ]; then
    output=$(TEST_DATABASE_URL="$literal" EXPECTED_TEST_DATABASE_URL="$literal" EXPECTED_URL_STATE="$expected_state" \
      make --no-print-directory -f Makefile -f "$scope_probe" $dry_run "$target" 2>&1)
  else
    output=$(env -u TEST_DATABASE_URL EXPECTED_TEST_DATABASE_URL="$literal" EXPECTED_URL_STATE="$expected_state" \
      make --no-print-directory -f Makefile -f "$scope_probe" $dry_run "$target" \
      "TEST_DATABASE_URL=$literal" 2>&1)
  fi
  status=$?
  set -e
  if [ "$status" -ne 0 ]; then
    printf 'Make evaluated or changed a literal TEST_DATABASE_URL (%s, %s, %s)\n' \
      "$source" "$mode" "$target" >&2
    return 1
  fi
  case "$output" in
    *SHOULD_NOT_EXPAND*|*SENTINEL*)
      printf 'Make evaluated a literal TEST_DATABASE_URL (%s, %s, %s)\n' \
        "$source" "$mode" "$target" >&2
      return 1
      ;;
  esac
}

env -u TEST_DATABASE_URL EXPECTED_URL_STATE=unset \
  make --no-print-directory -f Makefile -f "$scope_probe" non-database-scope-probe >/dev/null 2>&1

error_literal='$(error SHOULD_NOT_EXPAND)'
info_literal='$(info SENTINEL)'
for source in environment command-line; do
  for literal in "$error_literal" "$info_literal"; do
    assert_literal_is_not_evaluated "$source" dry-run non-database-scope-probe "$literal"
    assert_literal_is_not_evaluated "$source" actual non-database-scope-probe "$literal"
    assert_literal_is_not_evaluated "$source" actual backup-create "$literal"
  done
done
rm -f "$scope_probe"

ruby -e '
  makefile = File.read(ARGV.fetch(0))
  global_database_exports = makefile.lines.select { |line| line.strip == "export TEST_DATABASE_URL" }
  raise "TEST_DATABASE_URL must not be exported globally" unless global_database_exports.empty?
  normalization = "override TEST_DATABASE_URL := $(value TEST_DATABASE_URL)"
  raise "TEST_DATABASE_URL input must be normalized without evaluation" unless makefile.lines.map(&:strip).include?(normalization)
  direct_expansion = /:\s*export\s+TEST_DATABASE_URL\s*:=\s*\$\(TEST_DATABASE_URL\)/
  raise "target-specific export must not directly expand TEST_DATABASE_URL" if makefile.match?(direct_expansion)
  expected_database_export = "test-api-integration verify backup-create backup-restore-dry-run backup-restore test-backup-e2e: export TEST_DATABASE_URL := $(value TEST_DATABASE_URL)"
  raise "database targets must export their scoped URL" unless makefile.lines.map(&:strip).include?(expected_database_export)
  expected = {
    "verify" => "@TEST_DATABASE_URL=\"$$TEST_DATABASE_URL\" CGO_ENABLED=0 go test -p 1 ./... -count=1",
    "verify-go-quick" => "CGO_ENABLED=0 go test -p 1 ./... -count=1"
  }
  failures = []
  expected.each do |target, command|
    match = makefile.match(/^#{Regexp.escape(target)}:[^\n]*\n((?:\t[^\n]*\n)+)/)
    unless match
      failures << "missing Make target #{target}"
      next
    end
    go_tests = match[1].lines.map(&:strip).select do |line|
      line.include?("go test") && line.include?("./...")
    end
    failures << "#{target} must run exactly #{command.inspect}, got #{go_tests.inspect}" unless go_tests == [command]
  end
  recipe_lines = makefile.lines.select { |line| line.start_with?("\t") }
  forbidden = ["$(OUTPUT)", "$(BACKUP)", "$(CONFIRM)", "$(TEST_DATABASE_URL)"]
  forbidden.each do |expansion|
    raise "Make recipe directly expands #{expansion}" if recipe_lines.any? { |line| line.include?(expansion) }
  end
  sensitive_commands = [
    "@TEST_DATABASE_URL=\"$$TEST_DATABASE_URL\" CGO_ENABLED=0 go test ./apps/api/internal/store/postgres -count=1 -v",
    "@TEST_DATABASE_URL=\"$$TEST_DATABASE_URL\" CGO_ENABLED=0 go test -p 1 ./... -count=1",
    "@DATABASE_URL=\"$$TEST_DATABASE_URL\" CGO_ENABLED=0 go run ./cmd/agent-studio backup create --output \"$$OUTPUT\"",
    "CGO_ENABLED=0 go run ./cmd/agent-studio backup inspect \"$$BACKUP\"",
    "@DATABASE_URL=\"$$TEST_DATABASE_URL\" CGO_ENABLED=0 go run ./cmd/agent-studio backup restore --dry-run \"$$BACKUP\"",
    "@DATABASE_URL=\"$$TEST_DATABASE_URL\" CGO_ENABLED=0 go run ./cmd/agent-studio backup restore --confirm-empty-instance \"$$BACKUP\"",
    "@TEST_DATABASE_URL=\"$$TEST_DATABASE_URL\" sh scripts/test-backup-e2e.sh"
  ]
  stripped_lines = recipe_lines.map(&:strip)
  sensitive_commands.each do |command|
    raise "missing safe Make recipe #{command.inspect}" unless stripped_lines.include?(command)
  end
  raise failures.join("\n") unless failures.empty?
' Makefile

ruby -e '
  wrapper = File.read(ARGV.fetch(0))
  expected = "go test ./internal/backup -run '\''^(TestBackupRestoreE2E|TestCurrentRuntimeRestoresV1Alpha1GoldenArchive)$'\'' -count=1 -v"
  raise "backup E2E wrapper must run round-trip and golden compatibility tests" unless wrapper.include?(expected)
' scripts/test-backup-e2e.sh

external_plan=$(EXTERNAL_DB=1 make -n test-backup-e2e)
case "$external_plan" in
  *"docker compose up -d --wait db"*)
    printf '%s\n' 'external database mode must not start the Compose database' >&2
    exit 1
    ;;
  *"sh scripts/test-backup-e2e.sh"*) ;;
  *)
    printf '%s\n' 'external database mode must run the backup E2E wrapper' >&2
    exit 1
    ;;
esac

local_plan=$(env -u EXTERNAL_DB make -n test-backup-e2e)
case "$local_plan" in
  *"docker compose up -d --wait db"*"sh scripts/test-backup-e2e.sh"*) ;;
  *)
    printf '%s\n' 'default local mode must start Compose before the backup E2E wrapper' >&2
    exit 1
    ;;
esac

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

  missing_ports = clone.call
  missing_ports.fetch("jobs").fetch("backup-recovery").fetch("services").fetch("postgres").delete("ports")
  File.write(File.join(output_dir, "missing-postgres-ports.yml"), YAML.dump(missing_ports))

  missing_health = clone.call
  missing_health.fetch("jobs").fetch("backup-recovery").fetch("services").fetch("postgres").delete("options")
  File.write(File.join(output_dir, "missing-postgres-health.yml"), YAML.dump(missing_health))

  wrong_credentials = clone.call
  wrong_credentials.fetch("jobs").fetch("backup-recovery").fetch("services").fetch("postgres").fetch("env")["POSTGRES_PASSWORD"] = "wrong"
  File.write(File.join(output_dir, "wrong-postgres-credentials.yml"), YAML.dump(wrong_credentials))

  missing_external_db = clone.call
  missing_external_db.fetch("jobs").fetch("backup-recovery").fetch("env").delete("EXTERNAL_DB")
  File.write(File.join(output_dir, "missing-external-db.yml"), YAML.dump(missing_external_db))
' "$workflow_path" "$mutation_dir"

BACKUP_WORKFLOW_MUTATION_CHILD=1 sh "$0" "$mutation_dir/control.yml"

mutation_failures=0
for mutation in \
  reusable-job-uses.yml \
  missing-go-version-file.yml \
  duplicate-test-database-url.yml \
  missing-postgres-ports.yml \
  missing-postgres-health.yml \
  wrong-postgres-credentials.yml \
  missing-external-db.yml; do
  if BACKUP_WORKFLOW_MUTATION_CHILD=1 sh "$0" "$mutation_dir/$mutation" >/dev/null 2>&1; then
    printf '%s\n' "workflow mutation accepted: $mutation" >&2
    mutation_failures=1
  fi
done
exit "$mutation_failures"
