#!/bin/sh
set -eu
repo_root=$(CDPATH= cd -- "$(dirname "$0")/.." && pwd)
cd "$repo_root"
test_root=$(mktemp -d "${TMPDIR:-/tmp}/agent-studio-v05-upgrade-contract.XXXXXX")
trap 'rm -rf "$test_root"' EXIT HUP INT TERM
validator="$test_root/validate-upgrade-contract.rb"
cat >"$validator" <<'RUBY'
source_path = ARGV.fetch(0)
source = File.read(source_path)
def reject!(message)
  warn message
  exit 1
end
required_markers = [
  "git cat-file -t v0.4.0", "git archive v0.4.0", "CGO_ENABLED=0 go build",
  "upgrade_source", "rollback_target", "pg_dump --format=custom",
  "pg_restore --exit-on-error", "legacy_active_run", "recovery_required",
  "cancelling", "cancelled", "postgres:18", "create_empty_database rollback_target",
]
missing = required_markers.reject { |marker| source.include?(marker) }
reject!("upgrade/rollback marker missing: #{missing.join(', ')}") unless missing.empty?
unless source.match?(/\[\s+["']?\$\(git cat-file -t v0\.4\.0\)["']?\s+=\s+["']?tag["']?\s+\]/) &&
       source.match?(/old_commit=\$\(git rev-parse\s+["']v0\.4\.0\^\{\}["']\)/) &&
       source.match?(/git cat-file -t ["']?\$old_commit["']?/) &&
       source.match?(/\[\s+["']?\$\(git cat-file -t ["']?\$old_commit["']?\)["']?\s+=\s+["']?commit["']?\s+\]/)
  reject!("peeled v0.4.0 object must be verified as a commit")
end
unless source.match?(/mkdir\s+-m\s+700\s+["']?\$run_root\/v040["']?/) &&
       source.match?(/git archive v0\.4\.0\s*\|\s*tar\s+-x\s+-C\s+["']?\$run_root\/v040["']?/) &&
       source.match?(/\(cd\s+["']?\$run_root\/v040["']?\s+&&\s+CGO_ENABLED=0 go build[^\n]*\.\/apps\/api\/cmd\/server\)/)
  reject!("v0.4.0 must be archived into mode 0700 storage and built there with CGO disabled")
end
forbidden = {
  /down\.sql/i => "down.sql", /drop\s+column/i => "DROP COLUMN",
  /delete\s+from\s+(?:[a-z_]+\.)?["']?schema_migrations["']?/i => "DELETE FROM schema_migrations",
  /update\s+(?:[a-z_]+\.)?["']?schema_migrations["']?/i => "UPDATE schema_migrations",
  /(?:\bgoose\b|\batlas\s+migrate\b|\bmigrate\b)[^\n]*(?:\bdown\b|\brollback\b)/i => "inverse migration",
}
forbidden.each do |pattern, label|
  reject!("forbidden upgrade/rollback command: #{label}") if source.match?(pattern)
end
postgres_exec = source[/^postgres_exec\(\)\s*\{\s*\n(?<body>.*?)^\}\s*$/m, :body]
unless postgres_exec &&
       postgres_exec.match?(/docker\s+compose\b/) &&
       postgres_exec.match?(/\bexec\s+-T\s+db\s+["']?\$@["']?/)
  reject!("postgres_exec must run clients inside the isolated PostgreSQL container")
end
logical_source = source.gsub(/\\\n[ \t]*/, " ")
client_lines = []
logical_source.lines.each_with_index do |line, index|
  stripped = line.strip
  next if stripped.empty? || stripped.start_with?("#")
  next unless stripped.match?(/\b(?:psql|pg_dump|pg_restore)\b/)
  client_lines << [stripped, index + 1]
end
client_lines.each do |line, number|
  wrapper = line.index(/\bpostgres_exec\b/)
  client = line.index(/\b(?:psql|pg_dump|pg_restore)\b/)
  unless wrapper && wrapper < client && line[wrapper..].match?(/\Apostgres_exec\s+(?:psql|pg_dump|pg_restore)\b/)
    reject!("PostgreSQL client must use postgres_exec at logical line #{number}")
  end
end
dump = client_lines.find { |line, _| line.match?(/\Apostgres_exec\s+pg_dump\b/) }&.first
unless dump && dump.include?("--format=custom") && dump.include?('--dbname="$upgrade_source_url"')
  reject!("custom dump must read only from upgrade_source")
end
restore = client_lines.find { |line, _| line.match?(/\Apostgres_exec\s+pg_restore\b/) }&.first
unless restore && restore.include?("--exit-on-error") && restore.include?('--dbname="$rollback_target_url"')
  reject!("restore must target only rollback_target with exit-on-error")
end
flow = source[/^run_upgrade_rollback\(\)\s*\{\s*\n(?<body>.*?)^\}\s*$/m, :body]
reject!("run_upgrade_rollback function missing") unless flow
reject!("fixed migration-7 run semantics must be asserted in the main flow") unless %w[legacy_active_run recovery_required cancelling cancelled].all? { |marker| flow.include?(marker) }
expected_stages = %w[legacy_api_stopped pre_upgrade_dumped current_api_started migration_7_asserted worker_cancellation_converged current_run_completed dump_restored legacy_api_rollback_started]
stage_calls = []
legacy_calls = []
flow.lines.each_with_index do |line, index|
  stage_calls << [Regexp.last_match(1), index] if line.match(/^\s*contract_stage\s+([a-z0-9_]+)\s*$/)
  legacy_calls << [Regexp.last_match(1), index] if line.match(/^\s*start_legacy_api\s+([a-z_]+)\s*$/)
end
unless stage_calls.map(&:first) == expected_stages
  reject!("upgrade/rollback stages must appear once in the required order")
end
unless legacy_calls.map(&:first) == %w[upgrade_source rollback_target]
  reject!("legacy API database roles must be upgrade_source then rollback_target only")
end
stage_positions = stage_calls.to_h
unless legacy_calls[0][1] < stage_positions.fetch("legacy_api_stopped") &&
       stage_positions.fetch("dump_restored") < legacy_calls[1][1] &&
       legacy_calls[1][1] < stage_positions.fetch("legacy_api_rollback_started")
  reject!("legacy API starts must bracket only the migration-6 setup and restored database smoke")
end
empty_database = flow.lines.index { |line| line.strip == "create_empty_database rollback_target" }
unless empty_database && stage_positions.fetch("current_run_completed") < empty_database && empty_database < stage_positions.fetch("dump_restored")
  reject!("rollback_target must be created empty immediately before restore")
end
RUBY

valid_fixture="$test_root/valid.sh"
cat >"$valid_fixture" <<'FIXTURE'
#!/bin/sh
set -eu
postgres_image=postgres:18
postgres_tmpfs=/var/lib/postgresql/data
postgres_bind=127.0.0.1
postgres_exec() {
  docker compose -f "$compose_file" exec -T db "$@"
}
contract_stage() { :; }
start_legacy_api() { :; }
stop_legacy_api() { :; }
[ "$(git cat-file -t v0.4.0)" = tag ]
old_commit=$(git rev-parse 'v0.4.0^{}')
[ "$(git cat-file -t "$old_commit")" = commit ]
run_root=$(mktemp -d "${TMPDIR:-/tmp}/fixture.XXXXXX")
mkdir -m 700 "$run_root/v040"
git archive v0.4.0 | tar -x -C "$run_root/v040"
(cd "$run_root/v040" && CGO_ENABLED=0 go build -o "$run_root/legacy" ./apps/api/cmd/server)
run_upgrade_rollback() {
  start_legacy_api upgrade_source
  stop_legacy_api
  contract_stage legacy_api_stopped
  postgres_exec pg_dump --format=custom --dbname="$upgrade_source_url" --file="$run_root/before.dump"
  contract_stage pre_upgrade_dumped
  start_current_api
  contract_stage current_api_started
  assert_schema 7 recovery_required legacy_active_run cancelling
  contract_stage migration_7_asserted
  start_current_worker
  assert_run_status cancelled
  contract_stage worker_cancellation_converged
  smoke_current_run completed
  contract_stage current_run_completed
  create_empty_database rollback_target
  postgres_exec pg_restore --exit-on-error --dbname="$rollback_target_url" "$run_root/before.dump"
  contract_stage dump_restored
  start_legacy_api rollback_target
  contract_stage legacy_api_rollback_started
}
run_upgrade_rollback
FIXTURE
sh -n "$valid_fixture"
ruby -c "$validator" >/dev/null
ruby "$validator" "$valid_fixture"
expect_rejected() {
  candidate=$1
  expected=$2
  error_file="$candidate.error"
  if ruby "$validator" "$candidate" >"$error_file" 2>&1; then
    printf '%s\n' "contract fixture unexpectedly accepted: $candidate" >&2
    exit 1
  fi
  grep -F "$expected" "$error_file" >/dev/null || {
    printf '%s\n' "contract fixture failed for the wrong reason: $candidate" >&2
    cat "$error_file" >&2
    exit 1
  }
}

wrong_order_fixture="$test_root/wrong-order.sh"
ruby -e '
  path = ARGV.fetch(0)
  source = File.read(ARGV.fetch(1))
  first = "  contract_stage current_api_started\n"
  second = "  contract_stage migration_7_asserted\n"
  source = source.sub(first, "__FIRST__\n").sub(second, first).sub("__FIRST__\n", second)
  File.write(path, source)
' "$wrong_order_fixture" "$valid_fixture"
sh -n "$wrong_order_fixture"
expect_rejected "$wrong_order_fixture" "required order"

wrong_role_fixture="$test_root/wrong-role.sh"
ruby -e '
  source = File.read(ARGV.fetch(1)).sub("start_legacy_api rollback_target", "start_legacy_api upgrade_source")
  File.write(ARGV.fetch(0), source)
' "$wrong_role_fixture" "$valid_fixture"
sh -n "$wrong_role_fixture"
expect_rejected "$wrong_role_fixture" "database roles"

dangerous_fixture="$test_root/dangerous.sh"
cp "$valid_fixture" "$dangerous_fixture"
printf '%s\n' "postgres_exec psql --dbname=\"\$rollback_target_url\" -c 'ALTER TABLE runs DROP COLUMN status'" >>"$dangerous_fixture"
sh -n "$dangerous_fixture"
expect_rejected "$dangerous_fixture" "forbidden upgrade/rollback command: DROP COLUMN"

ruby -e '
  makefile = File.read(ARGV.fetch(0))
  phony = makefile.lines.grep(/^\.PHONY:/).join(" ").split
  raise "test-v05-upgrade-rollback-e2e must be phony" unless phony.include?("test-v05-upgrade-rollback-e2e")
  recipe = makefile[/^test-v05-upgrade-rollback-e2e:\s*\n(?<body>(?:\t.*\n?)+)/, :body]
  raise "upgrade/rollback Make target missing" unless recipe
  commands = recipe.lines.map { |line| line.sub(/^\t/, "").strip }.reject(&:empty?)
  raise "upgrade/rollback Make target must have one fixed command" unless commands == ["sh scripts/test-v05-upgrade-rollback-e2e.sh"]
' Makefile
make --no-print-directory -n test-v05-upgrade-rollback-e2e >/dev/null

failures=0
script=scripts/test-v05-upgrade-rollback-e2e.sh
if [ ! -f "$script" ]; then
  printf '%s\n' "$script is missing" >&2
  failures=1
else
  if ! sh -n "$script"; then
    printf '%s\n' "$script has invalid shell syntax" >&2
    failures=1
  elif ! ruby "$validator" "$script"; then
    failures=1
  fi
fi

workflow=.github/workflows/ci.yml
if ! ruby -ryaml - "$workflow" <<'RUBY'
begin
  workflow = YAML.safe_load(File.read(ARGV.fetch(0)), aliases: true)
  job = workflow.fetch("jobs").fetch("v04-upgrade-rollback")
  timeout = Integer(job.fetch("timeout-minutes"))
  raise "v04 upgrade/rollback job timeout must be 1..10 minutes" unless timeout.between?(1, 10)
  raise "v04 upgrade/rollback job must disable CGO" unless job.fetch("env").fetch("CGO_ENABLED").to_s == "0"

  steps = job.fetch("steps")
  checkout = steps.find { |step| step["uses"].to_s.start_with?("actions/checkout@") }
  raise "v04 upgrade/rollback checkout must be pinned" unless checkout && checkout.fetch("uses").match?(/@[0-9a-f]{40}\z/)
  raise "v04 upgrade/rollback checkout must fetch annotated tags" unless checkout.fetch("with").fetch("fetch-depth").to_i == 0

  contract = steps.find { |step| step["run"] == "sh scripts/test-v05-upgrade-rollback-e2e_test.sh" }
  raise "v04 upgrade/rollback contract step missing" unless contract
  exercise = steps.find { |step| step["run"] == "timeout 10m make test-v05-upgrade-rollback-e2e" }
  raise "v04 upgrade/rollback bounded exercise step missing" unless exercise && Integer(exercise.fetch("timeout-minutes")).between?(1, 10)
  upload = steps.find { |step| step["uses"].to_s.start_with?("actions/upload-artifact@") }
  raise "v04 upgrade/rollback failure log upload missing" unless upload && upload.fetch("if") == "failure()" && upload.fetch("uses").match?(/@[0-9a-f]{40}\z/)
rescue StandardError => error
  warn error.message
  exit 1
end
RUBY
then
  failures=1
fi

[ "$failures" -eq 0 ] || exit 1
printf '%s\n' 'v0.4 upgrade/rollback E2E contract passed'
