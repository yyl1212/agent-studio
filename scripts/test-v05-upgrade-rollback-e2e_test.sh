#!/bin/sh
set -eu
repo_root=$(CDPATH= cd -- "$(dirname "$0")/.." && pwd)
cd "$repo_root"
test_root=$(mktemp -d "${TMPDIR:-/tmp}/agent-studio-v05-upgrade-contract.XXXXXX")
trap 'rm -rf "$test_root"' EXIT HUP INT TERM
validator="$test_root/validate.rb"
cat >"$validator" <<'RUBY'
def reject!(message)
  warn message
  exit 1
end
def effective_code(source)
  source.each_line.map do |line|
    output = +""
    quote = nil
    escaped = false
    line.each_char do |character|
      if escaped
        output << character
        escaped = false
      elsif character == "\\" && quote != "'"
        output << character
        escaped = true
      elsif quote
        output << character
        quote = nil if character == quote
      elsif character == "'" || character == '"'
        output << character
        quote = character
      elsif character == "#"
        break
      else
        output << character
      end
    end
    output << "\n" unless output.end_with?("\n")
    output
  end.join
end
def function_body(code, name)
  code[/^#{Regexp.escape(name)}\(\)\s*\{\s*\n(?<body>.*?)^\}\s*$/m, :body] || reject!("#{name} helper missing")
end
def executable_lines(body)
  commands = []
  current = +""
  quote = nil
  escaped = false
  body.each_char do |character|
    if escaped then current << character; escaped = false
    elsif character == "\\" && quote != "'" then current << character; escaped = true
    elsif quote then current << character; quote = nil if character == quote
    elsif character == "'" || character == '"' then current << character; quote = character
    elsif character == ";" || character == "\n" then commands << current.strip; current = +""
    else current << character
    end
  end
  commands << current.strip
  commands.reject { |command| command.empty? || command.start_with?(":") }
end
def require_body(body, requirements, message)
  lines = executable_lines(body)
  reject!(message) unless requirements.all? { |required| lines.any? { |line| required.is_a?(Regexp) ? line.match?(required) : line.include?(required) } }
end
def validate_script(path)
  code = effective_code(File.read(path)).gsub(/\\\n[ \t]*/, " ")
  executable = executable_lines(code).join("\n")
  forbidden = [/down\.sql/i, /drop\s+column/i, /(?:delete\s+from|update)\s+(?:[a-z_]+\.)?["']?schema_migrations/i,
               /(?:\bgoose\b|\batlas\s+migrate\b|\bmigrate\b)[^\n]*(?:\bdown\b|\brollback\b)/i]
  reject!("forbidden inverse migration command") if forbidden.any? { |pattern| executable.match?(pattern) }
  require_body(function_body(code, "postgres_exec"), [/docker\s+compose\b/, /\bexec\s+-T\s+db\s+["']?\$@["']?/], "postgres_exec must use the isolated database container")
  annotated = function_body(code, "assert_annotated_v040")
  require_body(annotated, [/["']?\$\(git cat-file -t v0\.4\.0\)["']?\s+=\s+["']?tag/, /git rev-parse\s+["']v0\.4\.0\^\{\}["']/, /git cat-file -t ["']?\$old_commit["']?.*commit/], "annotated v0.4.0 and peeled commit assertions missing")
  build = function_body(code, "build_legacy_api")
  require_body(build, [/mkdir\s+-m\s+700\s+["']?\$run_root\/v040/, /git archive v0\.4\.0\s*\|\s*tar\s+-x\s+-C\s+["']?\$run_root\/v040/, /cd\s+["']?\$run_root\/v040["']?.*CGO_ENABLED=0 go build.*\.\/apps\/api\/cmd\/server/], "legacy API archive/build contract missing")
  database_name = executable_lines(function_body(code, "database_name"))
  upgrade_name = database_name.map { |line| line[/\Aupgrade_source\)\s*printf\s+["']?%s\\n["']?\s+([a-z0-9_]+)/, 1] }.compact.first
  rollback_name = database_name.map { |line| line[/\Arollback_target\)\s*printf\s+["']?%s\\n["']?\s+([a-z0-9_]+)/, 1] }.compact.first
  unless upgrade_name == "upgrade_source" && rollback_name == "rollback_target" && upgrade_name != rollback_name
    reject!("database_name must map the two roles to distinct fixed database names")
  end
  database_url = function_body(code, "database_url")
  require_body(database_url, [/\Adatabase_url_role=\$1\z/, /\Adatabase_url_name=\$\(database_name ["']\$database_url_role["']\)\z/, /\Aprintf\b.*\$database_url_name/], "database_url must consume database_name and construct its URL")
  start_lines = executable_lines(function_body(code, "start_process"))
  launch = start_lines.index { |line| line.match?(/\A(?:[A-Z_][A-Z0-9_]*=\S+\s+)*["']\$process_binary["'].*&\z/) }
  reject!("start_process must launch the binary then capture its pid") unless launch && start_lines[launch + 1] == 'started_pid=$!'
  require_body(function_body(code, "stop_process"), [/\Astop_pid=\$1\z/, /\Akill\s+["']\$stop_pid["'](?:\s|\z)/, /\Await\s+["']\$stop_pid["'](?:\s|\z)/], "stop_process must execute kill and wait")
  require_body(function_body(code, "wait_ready"), [/\Aready_target=\$1\z/, /\A(?:while|for)\b/, /\A(?:curl|grep)\b/, /\Areturn\s+1\z/], "wait_ready must loop on a real readiness command")
  assert_eq = function_body(code, "assert_eq")
  require_body(assert_eq, [/\Aassert_actual=\$1\z/, /\Aassert_expected=\$2\z/, /\Aassert_label=\$3\z/, /\A\[ ["']\$assert_actual["'] = ["']\$assert_expected["'] \] \|\| (?:return|exit) 1\z/], "assert_eq must execute an actual/expected comparison")
  legacy = function_body(code, "start_legacy_api")
  require_body(legacy, [/\Alegacy_start_role=\$1\z/, /\Alegacy_start_url=\$\(database_url ["']\$legacy_start_role["']\)\z/, /\ADATABASE_URL=["']\$legacy_start_url["'] start_process ["']\$legacy_api["']\z/, /\Alegacy_api_pid=\$started_pid\z/, /\Await_ready\b/], "start_legacy_api must execute role resolution, start and wait")
  reject!("legacy API must resolve its role exactly once") unless legacy.scan(/database_url\s+["']?\$legacy_start_role["']?/).length == 1
  current_api = function_body(code, "start_current_api")
  require_body(current_api, [/\Acurrent_api_start_role=\$1\z/, /\Acurrent_api_start_url=\$\(database_url ["']\$current_api_start_role["']\)\z/, /\ADATABASE_URL=["']\$current_api_start_url["'] start_process ["']\$current_api["']\z/, /\Acurrent_api_pid=\$started_pid\z/, /\Await_ready\b/], "start_current_api must execute role resolution, start and wait")
  worker = function_body(code, "start_current_worker")
  require_body(worker, [/\Acurrent_worker_start_role=\$1\z/, /\Acurrent_worker_start_url=\$\(database_url ["']\$current_worker_start_role["']\)\z/, /\ADATABASE_URL=["']\$current_worker_start_url["'] start_process ["']\$current_worker["']\z/, /\Acurrent_worker_pid=\$started_pid\z/, /\Await_ready\b/], "start_current_worker must execute role resolution, start and wait")
  require_body(function_body(code, "stop_legacy_api"), [/\Astop_process ["']\$legacy_api_pid["'] ["']\$deadline_epoch_ms["']\z/, /\Alegacy_api_pid=\z/], "stop_legacy_api must stop and clear its pid")
  require_body(function_body(code, "stop_current_runtime"), [/\Astop_process ["']\$current_worker_pid["'] ["']\$deadline_epoch_ms["']\z/, /\Astop_process ["']\$current_api_pid["'] ["']\$deadline_epoch_ms["']\z/, /\Acurrent_worker_pid=\z/, /\Acurrent_api_pid=\z/], "stop_current_runtime must stop and clear both pids")
  create = function_body(code, "create_database")
  require_body(create, [/\Acreate_database_role=\$1\z/, /\Acreate_database_name=\$\(database_name ["']\$create_database_role["']\)\z/, /\Apostgres_exec\s+psql\b.*CREATE DATABASE.*\$create_database_name/, /\Acreate_database_table_count=\$\(postgres_exec\s+psql\b.*database_url ["']\$create_database_role["']/, /\Aassert_eq ["']\$create_database_table_count["'] 0 empty_database\z/], "create_database must execute mapped creation and empty assertion")
  dump = function_body(code, "dump_database")
  require_body(dump, [/["']?\$dump_role["']?\s+=\s+upgrade_source/, /postgres_exec\s+pg_dump\s+--format=custom/, /--dbname=["']?\$\(database_url ["']?\$dump_role["']?\)/], "dump_database must custom-dump upgrade_source in the container")
  restore = function_body(code, "restore_database")
  require_body(restore, [/["']?\$restore_role["']?\s+=\s+rollback_target/, /postgres_exec\s+pg_restore\s+--exit-on-error/, /--dbname=["']?\$\(database_url ["']?\$restore_role["']?\)/], "restore_database must restore only rollback_target in the container")
  assertion_contracts = {
    "assert_schema" => [/\Aschema_actual=\$\(postgres_exec\s+psql\b/, /schema_migrations/, /\Aschema_expected=\$2\z/, /\Aassert_eq ["']\$schema_actual["'] ["']\$schema_expected["'] schema_version\z/],
    "seed_legacy_runs" => [/postgres_exec\s+psql\b/, /INSERT/i, /running/, /cancelling/],
    "assert_legacy_transition" => [/\Atransition_status=\$\(postgres_exec\s+psql/, /\Atransition_reason=\$\(postgres_exec\s+psql/, /\Atransition_cancelling=\$\(postgres_exec\s+psql/, /\Aassert_eq ["']\$transition_status["'] recovery_required/, /\Aassert_eq ["']\$transition_reason["'] legacy_active_run/, /\Aassert_eq ["']\$transition_cancelling["'] cancelling/],
    "assert_cancelling_cancelled" => [/\Aworker_assert_status=\$\(postgres_exec\s+psql/, /\Aworker_assert_node_count=\$\(postgres_exec\s+psql/, /\Aassert_eq ["']\$worker_assert_status["'] cancelled/, /\Aassert_eq ["']\$worker_assert_node_count["'] 0/],
    "smoke_current_run" => [/\Asmoke_run_status=\$\(curl\b/, /\Asmoke_terminal_count=\$\(postgres_exec\s+psql/, /\Asmoke_lease_count=\$\(postgres_exec\s+psql/, /\Aassert_eq ["']\$smoke_run_status["'] completed/, /\Aassert_eq ["']\$smoke_terminal_count["'] 1/, /\Aassert_eq ["']\$smoke_lease_count["'] 0/],
    "assert_rollback_records" => [/\Arollback_schema=\$\(postgres_exec\s+psql/, /\Arollback_payload_table=\$\(postgres_exec\s+psql/, /\Arollback_legacy_count=\$\(postgres_exec\s+psql/, /\Aassert_eq ["']\$rollback_schema["'] 6/, /\Aassert_eq ["']\$rollback_payload_table["'] t/, /\Aassert_eq ["']\$rollback_legacy_count["'] 3/],
  }
  assertion_contracts.each { |name, markers| require_body(function_body(code, name), markers, "#{name} must query actual values and call assert_eq") }
  executable_lines(code).each_with_index do |line, index|
    client = line.index(/\b(?:psql|pg_dump|pg_restore)\b/)
    next unless client
    wrapper = line.index(/\bpostgres_(?:cleanup_)?exec\b/)
    reject!("PostgreSQL client bypasses a bounded postgres wrapper at line #{index + 1}") unless wrapper && wrapper < client
  end
  reject!("isolated PostgreSQL 18 marker missing") unless executable_lines(code).any? { |line| line == "postgres_image=postgres:18" }
  expected = [
    "assert_annotated_v040", "build_legacy_api", "create_database upgrade_source", "start_legacy_api upgrade_source", "assert_schema upgrade_source 6",
    "seed_legacy_runs", "stop_legacy_api", "dump_database upgrade_source", "start_current_api upgrade_source", "assert_schema upgrade_source 7",
    "assert_legacy_transition", "start_current_worker upgrade_source", "assert_cancelling_cancelled", "smoke_current_run", "stop_current_runtime",
    "create_database rollback_target", "restore_database rollback_target", "start_legacy_api rollback_target", "assert_schema rollback_target 6", "assert_rollback_records",
  ]
  flow = function_body(code, "run_upgrade_rollback")
  helper_names = expected.map { |call| call.split.first }.uniq
  calls = executable_lines(flow).select { |line| helper_names.include?(line.split.first) }
  reject!("fixed helper actions must appear once in the required order") unless calls == expected
end
def validate_workflow(path)
  job = YAML.safe_load(File.read(path), aliases: true).fetch("jobs").fetch("v04-upgrade-rollback")
  reject!("v04 job cannot be conditional or continue on error") if job.key?("if") || job.key?("continue-on-error")
  reject!("v04 job timeout/CGO contract missing") unless job.fetch("timeout-minutes").to_i.between?(1, 10) && job.dig("env", "CGO_ENABLED").to_s == "0"
  reject!("v04 job must use the fixed non-production key") unless job.dig("env", "RUN_PAYLOAD_ENCRYPTION_KEY") == "MDEyMzQ1Njc4OWFiY2RlZjAxMjM0NTY3ODlhYmNkZWY="
  reject!("v04 artifact directory must be fixed") unless job.dig("env", "V04_UPGRADE_ROLLBACK_ARTIFACT_DIR") == "artifacts/v04-upgrade-rollback"
  steps = job.fetch("steps")
  checkout = steps.find { |step| step["uses"].to_s.start_with?("actions/checkout@") }
  setup = steps.find { |step| step["uses"].to_s.start_with?("actions/setup-go@") }
  contract = steps.find { |step| step["run"] == "sh scripts/test-v05-upgrade-rollback-e2e_test.sh" }
  exercise = steps.find { |step| step["run"] == "timeout 10m make test-v05-upgrade-rollback-e2e" }
  uploads = steps.select { |step| step["uses"].to_s.start_with?("actions/upload-artifact@") }
  upload = uploads.first
  reject!("checkout must be pinned and fetch annotated tags") unless checkout && checkout["uses"].match?(/@[0-9a-f]{40}\z/) && checkout.dig("with", "fetch-depth").to_i == 0
  reject!("setup-go must use the fixed full SHA") unless setup && setup["uses"] == "actions/setup-go@b7ad1dad31e06c5925ef5d2fc7ad053ef454303e"
  reject!("contract and bounded exercise steps missing") unless contract && exercise && exercise["timeout-minutes"].to_i.between?(1, 10)
  reject!("v04 steps cannot continue on error") if steps.any? { |step| step.key?("continue-on-error") }
  reject!("v04 execution steps cannot be conditional") if steps.reject { |step| step.equal?(upload) }.any? { |step| step.key?("if") }
  reject!("workflow must never upload a database dump") if job.to_s.match?(/(?:\*\.dump|\.dump\b)/i)
  upload_paths = upload&.dig("with", "path").to_s.lines.map(&:strip).reject(&:empty?)
  safe_upload = uploads.length == 1 && upload["if"] == "failure()" && upload.dig("with", "name") == "v04-upgrade-rollback-logs" &&
                upload["uses"].match?(/\Aactions\/upload-artifact@[0-9a-f]{40}\z/) && upload_paths == ["artifacts/v04-upgrade-rollback/*.log", "artifacts/v04-upgrade-rollback/failure-summary.json"] &&
                upload.dig("with", "retention-days").to_i == 7
  reject!("failure artifact must use the exact safe log contract") unless safe_upload && !upload.key?("continue-on-error")
end
mode, path = ARGV
begin
  mode == "script" ? validate_script(path) : validate_workflow(path)
rescue KeyError => error
  reject!(error.message)
end
RUBY

valid_script="$test_root/valid.sh"
cat >"$valid_script" <<'FIXTURE'
#!/bin/sh
set -eu
postgres_image=postgres:18
postgres_exec() {
  docker compose -f "$compose_file" exec -T db "$@"
}
database_name() {
  database_role=$1
  case "$database_role" in
    upgrade_source) printf '%s\n' upgrade_source ;;
    rollback_target) printf '%s\n' rollback_target ;;
    *) return 2 ;;
  esac
}
database_url() {
  database_url_role=$1; database_url_name=$(database_name "$database_url_role"); printf 'postgresql://agent:agent@localhost/%s?sslmode=disable\n' "$database_url_name"
}
start_process() {
  process_binary=$1
  "$process_binary" >>"$process_log" 2>&1 &
  started_pid=$!
}
stop_process() {
  stop_pid=$1; kill "$stop_pid" 2>/dev/null || true; wait "$stop_pid" 2>/dev/null || true
}
wait_ready() {
  ready_target=$1
  ready_attempts=0
  while [ "$ready_attempts" -lt 30 ]; do
    curl -fsS "$ready_target" && return 0
    ready_attempts=$((ready_attempts + 1))
  done
  return 1
}
assert_eq() {
  assert_actual=$1; assert_expected=$2; assert_label=$3; [ "$assert_actual" = "$assert_expected" ] || return 1
}
assert_annotated_v040() {
  [ "$(git cat-file -t v0.4.0)" = tag ]; old_commit=$(git rev-parse 'v0.4.0^{}'); [ "$(git cat-file -t "$old_commit")" = commit ]
}
build_legacy_api() {
  mkdir -m 700 "$run_root/v040"; git archive v0.4.0 | tar -x -C "$run_root/v040"; (cd "$run_root/v040" && CGO_ENABLED=0 go build -o "$legacy_api" ./apps/api/cmd/server)
}
create_database() {
  create_database_role=$1; create_database_name=$(database_name "$create_database_role"); postgres_exec psql --dbname=postgres -v ON_ERROR_STOP=1 -c "CREATE DATABASE $create_database_name"; create_database_table_count=$(postgres_exec psql --dbname="$(database_url "$create_database_role")" -Atc 'SELECT count(*) FROM pg_catalog.pg_tables'); assert_eq "$create_database_table_count" 0 empty_database
}
start_legacy_api() {
  legacy_start_role=$1; legacy_start_url=$(database_url "$legacy_start_role"); DATABASE_URL="$legacy_start_url" start_process "$legacy_api"; legacy_api_pid=$started_pid; wait_ready "$legacy_api_url/readyz"
}
assert_schema() {
  schema_role=$1; schema_expected=$2; schema_actual=$(postgres_exec psql --dbname="$(database_url "$schema_role")" -Atc 'SELECT max(version) FROM schema_migrations'); assert_eq "$schema_actual" "$schema_expected" schema_version
}
seed_legacy_runs() {
  postgres_exec psql --dbname="$(database_url upgrade_source)" -c "INSERT INTO runs(status) VALUES ('running'), ('cancelling')"
}
stop_legacy_api() {
  stop_process "$legacy_api_pid" "$deadline_epoch_ms"; legacy_api_pid=
}
dump_database() {
  dump_role=$1; [ "$dump_role" = upgrade_source ]; postgres_exec pg_dump --format=custom --dbname="$(database_url "$dump_role")" --file="$run_root/v040.dump"
}
start_current_api() {
  current_api_start_role=$1; current_api_start_url=$(database_url "$current_api_start_role"); DATABASE_URL="$current_api_start_url" start_process "$current_api"; current_api_pid=$started_pid; wait_ready "$current_api_url/readyz"
}
assert_legacy_transition() {
  transition_status=$(postgres_exec psql --dbname="$(database_url upgrade_source)" -Atc 'SELECT status FROM runs WHERE id = legacy_running'); transition_reason=$(postgres_exec psql --dbname="$(database_url upgrade_source)" -Atc 'SELECT recovery_reason FROM runs WHERE id = legacy_running'); transition_cancelling=$(postgres_exec psql --dbname="$(database_url upgrade_source)" -Atc 'SELECT status FROM runs WHERE id = legacy_cancelling'); assert_eq "$transition_status" recovery_required legacy_status; assert_eq "$transition_reason" legacy_active_run legacy_reason; assert_eq "$transition_cancelling" cancelling cancelling_status
}
start_current_worker() {
  current_worker_start_role=$1; current_worker_start_url=$(database_url "$current_worker_start_role"); DATABASE_URL="$current_worker_start_url" start_process "$current_worker"; current_worker_pid=$started_pid; wait_ready "$worker_log"
}
assert_cancelling_cancelled() {
  worker_assert_status=$(postgres_exec psql --dbname="$(database_url upgrade_source)" -Atc 'SELECT status FROM runs WHERE id = legacy_cancelling'); worker_assert_node_count=$(postgres_exec psql --dbname="$(database_url upgrade_source)" -Atc 'SELECT count(*) FROM node_runs WHERE run_id = legacy_cancelling'); assert_eq "$worker_assert_status" cancelled cancelling_status; assert_eq "$worker_assert_node_count" 0 cancelling_node_runs
}
smoke_current_run() {
  smoke_run_status=$(curl -fsS "$current_api_url/api/runs/current" | jq -r .status); smoke_terminal_count=$(postgres_exec psql --dbname="$(database_url upgrade_source)" -Atc 'SELECT count(*) FROM run_events WHERE terminal'); smoke_lease_count=$(postgres_exec psql --dbname="$(database_url upgrade_source)" -Atc 'SELECT count(*) FROM runs WHERE lease_owner IS NOT NULL'); assert_eq "$smoke_run_status" completed current_run_status; assert_eq "$smoke_terminal_count" 1 current_terminal_events; assert_eq "$smoke_lease_count" 0 current_leases
}
stop_current_runtime() {
  stop_process "$current_worker_pid" "$deadline_epoch_ms"; current_worker_pid=; stop_process "$current_api_pid" "$deadline_epoch_ms"; current_api_pid=
}
restore_database() {
  restore_role=$1; [ "$restore_role" = rollback_target ]; postgres_exec pg_restore --exit-on-error --dbname="$(database_url "$restore_role")" "$run_root/v040.dump"
}
assert_rollback_records() {
  rollback_schema=$(postgres_exec psql --dbname="$(database_url rollback_target)" -Atc 'SELECT max(version) FROM schema_migrations'); rollback_payload_table=$(postgres_exec psql --dbname="$(database_url rollback_target)" -Atc "SELECT to_regclass('run_payloads') IS NULL"); rollback_legacy_count=$(postgres_exec psql --dbname="$(database_url rollback_target)" -Atc 'SELECT count(*) FROM runs'); assert_eq "$rollback_schema" 6 rollback_schema; assert_eq "$rollback_payload_table" t no_run_payloads; assert_eq "$rollback_legacy_count" 3 rollback_records
}
run_upgrade_rollback() {
  assert_annotated_v040
  build_legacy_api
  create_database upgrade_source
  start_legacy_api upgrade_source
  assert_schema upgrade_source 6
  seed_legacy_runs
  stop_legacy_api
  dump_database upgrade_source
  start_current_api upgrade_source
  assert_schema upgrade_source 7
  assert_legacy_transition
  start_current_worker upgrade_source
  assert_cancelling_cancelled
  smoke_current_run
  stop_current_runtime
  create_database rollback_target
  restore_database rollback_target
  start_legacy_api rollback_target
  assert_schema rollback_target 6
  assert_rollback_records
}
FIXTURE

valid_workflow="$test_root/valid.yml"
cat >"$valid_workflow" <<'YAML'
jobs:
  v04-upgrade-rollback:
    timeout-minutes: 10
    env: {CGO_ENABLED: "0", RUN_PAYLOAD_ENCRYPTION_KEY: "MDEyMzQ1Njc4OWFiY2RlZjAxMjM0NTY3ODlhYmNkZWY=", V04_UPGRADE_ROLLBACK_ARTIFACT_DIR: artifacts/v04-upgrade-rollback}
    steps:
      - {name: Checkout, uses: "actions/checkout@3d3c42e5aac5ba805825da76410c181273ba90b1", with: {fetch-depth: 0}}
      - {name: Set up Go, uses: "actions/setup-go@b7ad1dad31e06c5925ef5d2fc7ad053ef454303e"}
      - {name: Verify contract, run: "sh scripts/test-v05-upgrade-rollback-e2e_test.sh"}
      - {name: Exercise, timeout-minutes: 10, run: "timeout 10m make test-v05-upgrade-rollback-e2e"}
      - name: Preserve failure logs
        if: failure()
        uses: "actions/upload-artifact@043fb46d1a93c77aae656e7c1c64a875d1fc6a0a"
        with: {name: v04-upgrade-rollback-logs, path: "artifacts/v04-upgrade-rollback/*.log\nartifacts/v04-upgrade-rollback/failure-summary.json\n", retention-days: 7}
YAML

sh -n "$valid_script"
ruby -c "$validator" >/dev/null
ruby -ryaml "$validator" script "$valid_script"
ruby -ryaml "$validator" workflow "$valid_workflow"
expect_rejected() {
  mode=$1 candidate=$2 expected=$3 error_file="$candidate.error"
  if ruby -ryaml "$validator" "$mode" "$candidate" >"$error_file" 2>&1; then printf '%s\n' "fixture unexpectedly accepted: $candidate" >&2; exit 1; fi
  grep -F "$expected" "$error_file" >/dev/null || { cat "$error_file" >&2; exit 1; }
}
replace() {
  ruby -e 's=File.read(ARGV[1]); old=ARGV[2]; abort "mutation source missing" unless s.include?(old); File.write(ARGV[0], s.sub(old, ARGV[3]))' "$@"
}
swap() {
  ruby -e 's=File.read(ARGV[1]); a=ARGV[2]; b=ARGV[3]; abort "swap source missing" unless s.include?(a) && s.include?(b); File.write(ARGV[0], s.sub(a,"__SWAP__").sub(b,a).sub("__SWAP__",b))' "$@"
}
noop_helper() {
  ruby -e 's=File.read(ARGV[1]); n=ARGV[2]; b=ARGV[3]||"  :"; p=/^#{Regexp.escape(n)}\(\)\s*\{.*?^\}$/m; abort "helper source missing" unless s.match?(p); File.write(ARGV[0], s.sub(p, "#{n}() {\n#{b}\n}"))' "$@"
}

commented="$test_root/commented.sh"; replace "$commented" "$valid_script" '  build_legacy_api' '  : # build_legacy_api'; sh -n "$commented"; expect_rejected script "$commented" "required order"
wrong_order="$test_root/wrong-order.sh"; swap "$wrong_order" "$valid_script" '  stop_legacy_api' '  dump_database upgrade_source'; sh -n "$wrong_order"; expect_rejected script "$wrong_order" "required order"
same_database="$test_root/same-database.sh"; replace "$same_database" "$valid_script" "rollback_target) printf '%s\\n' rollback_target" "rollback_target) printf '%s\\n' upgrade_source"; sh -n "$same_database"; expect_rejected script "$same_database" "distinct fixed database names"
wrong_legacy="$test_root/wrong-legacy.sh"; replace "$wrong_legacy" "$valid_script" '  start_legacy_api rollback_target' '  start_legacy_api upgrade_source'; sh -n "$wrong_legacy"; expect_rejected script "$wrong_legacy" "required order"
for helper in start_process stop_process wait_ready start_current_worker; do noop="$test_root/noop-$helper.sh"; noop_helper "$noop" "$valid_script" "$helper"; sh -n "$noop"; expect_rejected script "$noop" "$helper"; done
for spec in 'start_process|  : "binary=$1"; : "\"$binary\" &"; : "started_pid=$!"' 'stop_process|  : "pid=$1"; : "kill \"$pid\""; : "wait \"$pid\""' 'wait_ready|  : "target=$1"; : "while curl; return 1"' 'start_current_worker|  : "role=$1"; : "url=$(database_url \"$role\")"; : "start_process wait_ready"'; do helper=${spec%%|*}; body=${spec#*|}; inert="$test_root/inert-$helper.sh"; noop_helper "$inert" "$valid_script" "$helper" "$body"; sh -n "$inert"; expect_rejected script "$inert" "$helper"; done
inert_assert="$test_root/inert-assert.sh"; noop_helper "$inert_assert" "$valid_script" assert_eq '  : "actual=$1"; : "expected=$2"; : "label=$3"; : "[ \"$actual\" = \"$expected\" ] || return 1"'; sh -n "$inert_assert"; expect_rejected script "$inert_assert" "assert_eq must execute"
missing_assert="$test_root/missing-assert.sh"; replace "$missing_assert" "$valid_script" 'assert_eq "$transition_reason" legacy_active_run legacy_reason' ':'; sh -n "$missing_assert"; expect_rejected script "$missing_assert" "assert_legacy_transition"
noop_compare="$test_root/noop-compare.sh"; replace "$noop_compare" "$valid_script" '[ "$assert_actual" = "$assert_expected" ] || return 1' '[ 0 = 0 ] || return 1'; sh -n "$noop_compare"; expect_rejected script "$noop_compare" "assert_eq must execute"
hardcoded_database="$test_root/hardcoded-database.sh"; replace "$hardcoded_database" "$valid_script" 'create_database_role=$1; create_database_name=$(database_name "$create_database_role"); postgres_exec psql' 'create_database_role=$1; create_database_name=upgrade_source; postgres_exec psql'; sh -n "$hardcoded_database"; expect_rejected script "$hardcoded_database" "create_database must execute"
ignored_name="$test_root/ignored-name.sh"; replace "$ignored_name" "$valid_script" "printf 'postgresql://agent:agent@localhost/%s?sslmode=disable\\n' \"\$database_url_name\"" ": \"\$database_url_name\"; printf 'postgresql://agent:agent@localhost/upgrade_source?sslmode=disable\\n'"; sh -n "$ignored_name"; expect_rejected script "$ignored_name" "database_url must consume"
inert_create="$test_root/inert-create.sh"; replace "$inert_create" "$valid_script" 'postgres_exec psql --dbname=postgres -v ON_ERROR_STOP=1 -c "CREATE DATABASE $create_database_name"' ': "postgres_exec psql CREATE DATABASE $create_database_name"'; sh -n "$inert_create"; expect_rejected script "$inert_create" "create_database must execute"
missing_cancelling="$test_root/missing-cancelling.sh"; replace "$missing_cancelling" "$valid_script" 'assert_eq "$transition_cancelling" cancelling cancelling_status' ':'; sh -n "$missing_cancelling"; expect_rejected script "$missing_cancelling" "assert_legacy_transition"
dangerous="$test_root/dangerous.sh"; cp "$valid_script" "$dangerous"; printf '%s\n' "postgres_exec psql -c 'ALTER TABLE runs DROP COLUMN status'" >>"$dangerous"; sh -n "$dangerous"; expect_rejected script "$dangerous" "forbidden inverse"
unsafe_artifact="$test_root/unsafe-artifact.yml"; replace "$unsafe_artifact" "$valid_workflow" 'name: v04-upgrade-rollback-logs' 'name: unsafe-database-artifact'; expect_rejected workflow "$unsafe_artifact" "exact safe log contract"
dump_upload="$test_root/dump-upload.yml"; replace "$dump_upload" "$valid_workflow" 'artifacts/v04-upgrade-rollback/*.log' 'artifacts/v04-upgrade-rollback/*.dump'; expect_rejected workflow "$dump_upload" "never upload a database dump"
other_json="$test_root/other-json.yml"; replace "$other_json" "$valid_workflow" 'artifacts/v04-upgrade-rollback/failure-summary.json' 'artifacts/v04-upgrade-rollback/*.json'; expect_rejected workflow "$other_json" "exact safe log contract"

ruby -e 'm=File.read("Makefile"); raise unless m.match?(/^test-v05-upgrade-rollback-e2e:\n\tsh scripts\/test-v05-upgrade-rollback-e2e\.sh$/) && m.lines.grep(/^\.PHONY:/).join.include?("test-v05-upgrade-rollback-e2e")'
make --no-print-directory -n test-v05-upgrade-rollback-e2e >/dev/null
failures=0
script=scripts/test-v05-upgrade-rollback-e2e.sh
if [ ! -f "$script" ]; then printf '%s\n' "$script is missing" >&2; failures=1
elif ! sh -n "$script" || ! ruby -ryaml "$validator" script "$script"; then failures=1; fi
if ! ruby -ryaml "$validator" workflow .github/workflows/ci.yml; then failures=1; fi
[ "$failures" -eq 0 ] || exit 1

review_validator="$test_root/review-validator.rb"
cat >"$review_validator" <<'RUBY'
source=File.read(ARGV.fetch(0))
def require_match(source, pattern, message)
  abort message unless source.match?(pattern)
end
%w[now_epoch_ms remaining_budget_ms wait_bounded run_bounded run_cleanup_bounded assert_port_unused assert_process_identity sensitive_literals contains_sensitive_data collect_postgres_logs postgres_cleanup_exec collect_database_diagnostics capture_failure_artifacts capture_legacy_snapshot].each do |name|
  require_match(source, /^#{Regexp.escape(name)}\(\)\s*\{/m, "#{name} helper missing")
end
require_match(source, /UPGRADE_ROLLBACK_DEADLINE_SECONDS=\$\{UPGRADE_ROLLBACK_DEADLINE_SECONDS:-570\}/, "570-second internal deadline default missing")
require_match(source, /UPGRADE_ROLLBACK_DEADLINE_SECONDS[^\n]*(?:60|\[ [^\]]+ -lt 60 \])/, "deadline minimum missing")
require_match(source, /UPGRADE_ROLLBACK_DEADLINE_SECONDS[^\n]*(?:570|\[ [^\]]+ -gt 570 \])/, "deadline maximum missing")
clean_tree_guard_call=source.index("\nassert_clean_source_tree\n") or abort "clean-tree guard invocation missing"
run_root_creation=source.index('run_root=$(mktemp') or abort "run-root creation missing"
abort "clean-tree guard must run before run-root creation" unless clean_tree_guard_call < run_root_creation
cleanup=source[/^cleanup\(\)\s*\{\n(.*?)^\}/m,1] or abort "cleanup missing"
abort "cleanup must use attempted compose state" unless cleanup.include?("compose_attempted")
abort "cleanup must collect PostgreSQL logs before down" unless cleanup.index("collect_postgres_logs") && cleanup.index("docker compose") && cleanup.index("collect_postgres_logs") < cleanup.rindex("docker compose")
abort "cleanup down must be independently bounded" unless cleanup.match?(/run_cleanup_bounded.*docker compose.*down --remove-orphans/m)
abort "cleanup must not remove volumes" if cleanup.match?(/down[^\n]*(?:--volumes|\s-v(?:\s|$))/)
%w[cleanup_total_deadline_ms cleanup_stop_deadline_ms cleanup_logs_deadline_ms cleanup_diagnostics_deadline_ms cleanup_artifact_deadline_ms cleanup_down_deadline_ms cleanup_remove_deadline_ms].each { |name| abort "#{name} missing" unless cleanup.include?(name) }
abort "cleanup must collect bounded database diagnostics before artifact capture" unless cleanup.index("collect_database_diagnostics") && cleanup.index("capture_failure_artifacts") && cleanup.index("collect_database_diagnostics") < cleanup.index("capture_failure_artifacts")
abort "stop_process must accept a shared absolute deadline" unless source[/^stop_process\(\)\s*\{\n(.*?)^\}/m,1].include?('stop_process_deadline_ms=$2')
up=source.index('docker compose -f "$compose_file" up -d db') or abort "compose up missing"
attempt=source.rindex("compose_attempted=1",up) or abort "compose attempt must be marked before up"
abort "compose attempt marker too early to bind up" if source[attempt...up].include?("compose_attempted=0")
wait=source[/^wait_ready\(\)\s*\{\n(.*?)^\}/m,1] or abort "wait_ready missing"
abort "wait_ready pid missing" unless wait.match?(/^\s*(?:pid|ready_pid)=\$2$/)
wait_pid_probe=wait.index('kill -0 "$ready_pid"')
abort "wait_ready must check pid before readiness" unless wait_pid_probe && (wait.index("bounded_curl") || wait.index("curl")) && wait_pid_probe < (wait.index("bounded_curl") || wait.index("curl"))
%w[2026-01-02T03:04:05Z 2026-01-02T03:05:05Z 2026-01-02T03:06:05Z 2026-01-02T03:07:05Z 2026-01-02T03:08:05Z].each { |stamp| abort "fixed UTC timestamp #{stamp} missing" unless source.include?(stamp) }
sensitive=source[/^sensitive_literals\(\)\s*\{\n(.*?)^\}/m,1] or abort "sensitive_literals missing"
%w[legacy-public-fixture legacy-running legacy-cancelling legacy-completed current-smoke-private ciphertext-marker].each { |literal| abort "sensitive literal #{literal} missing" unless sensitive.include?(literal) }
transition=source[/^assert_legacy_transition\(\)\s*\{\n(.*?)^\}/m,1] or abort "transition assertion missing"
%w[completed_snapshot cancelling_snapshot run.started run.completed].each { |marker| abort "transition #{marker} assertion missing" unless transition.include?(marker) }
cancelled=source[/^assert_cancelling_cancelled\(\)\s*\{\n(.*?)^\}/m,1] or abort "worker assertion missing"
%w[lease_token running_node_count running_event_count running_terminal_count worker_assert_event_sequence run.started,run.cancelled].each { |marker| abort "legacy unclaimed #{marker} assertion missing" unless cancelled.include?(marker) }
scan=source[/^contains_sensitive_data\(\)\s*\{\n(.*?)^\}/m,1] or abort "sensitive scanner missing"
abort "scanner errors must be distinct" unless scan.include?('exit 2')
artifacts=source[/^capture_failure_artifacts\(\)\s*\{\n(.*?)^\}/m,1] or abort "artifact capture missing"
abort "artifact scan must fail closed" unless artifacts.match?(/case .*scan.* in/m) && artifacts.include?('failure_artifact_unsafe=1')
diagnostics=source[/^collect_database_diagnostics\(\)\s*\{\n(.*?)^\}/m,1] or abort "database diagnostics missing"
%w[upgrade_source current rollback_target migration_version run_status_counts tables unavailable_or_error].each { |marker| abort "database diagnostics #{marker} missing" unless diagnostics.include?(marker) }
abort "database diagnostics must use bounded PostgreSQL execution" unless diagnostics.include?('postgres_cleanup_exec')
abort "database diagnostics must read the maximum applied migration version" unless diagnostics.match?(/max\(version\)::text FROM schema_migrations/)
abort "database diagnostics must aggregate run status counts from runs" unless diagnostics.match?(/FROM runs GROUP BY status/)
abort "database diagnostics must list only public tables" unless diagnostics.match?(/information_schema\.tables WHERE table_schema='public'/)
rollback=source[/^assert_rollback_records\(\)\s*\{\n(.*?)^\}/m,1] or abort "rollback assertion missing"
%w[completed_snapshot cancelling_snapshot running_snapshot run.started run.completed].each { |marker| abort "rollback #{marker} content assertion missing" unless rollback.include?(marker) }
require_match(source, /elapsed_ms=.*now_epoch_ms.*start_epoch_ms/m, "elapsed measurement missing")
require_match(source, /\[ ["']?\$elapsed_ms["']? -le 570000 \]/, "570000ms success assertion missing")
require_match(source, /last_safe_command_label=/, "safe command label state missing")
require_match(source, /set_safe_command_label\(\).*case.*compose_up.*postgres_client.*legacy_build/m, "safe command labels must be enumerated")
%w[01_annotated_tag 02_legacy_build 03_create_upgrade_source 04_start_legacy_source 05_schema6_source 06_seed_legacy 07_stop_legacy 08_dump_source 09_start_current_api 10_schema7_source 11_assert_transition 12_start_current_worker 13_assert_worker 14_current_smoke 15_stop_current 16_create_rollback_target 17_restore_target 18_start_legacy_target 19_schema6_target 20_assert_rollback].each { |phase| abort "fixed phase #{phase} missing" unless source.include?("current_phase=#{phase}") }
require_match(source, /Process\.spawn\(\*ARGV,pgroup:true\)/, "bounded supervisor must pass an argv array and own a process group")
require_match(source, /go\(\).*legacy_build.*run_bounded.*env CGO_ENABLED=.*\$go_binary.*\$@/, "legacy build environment or argv forwarding missing")
require_match(source, /run_bounded postgres_client docker compose -f .* exec -T db .*\$@/, "PostgreSQL argv forwarding missing")
RUBY
ruby "$review_validator" "$script"

relocated_guard="$test_root/relocated-clean-tree-guard.sh"
ruby -e '
  source=File.read(ARGV.fetch(0))
  invocation="assert_clean_source_tree\n"
  abort "clean-tree guard invocation missing" unless source.scan(invocation).length == 1
  source=source.sub(invocation, "")
  trap_line="trap '\''cleanup $?'\'' EXIT\n"
  abort "cleanup trap missing" unless source.include?(trap_line)
  File.write(ARGV.fetch(1), source.sub(trap_line, trap_line + invocation))
' "$script" "$relocated_guard"
if ruby "$review_validator" "$relocated_guard" >"$test_root/relocated-guard.out" 2>&1; then
  printf '%s\n' 'contract accepted a clean-tree guard relocated after run-root creation' >&2
  exit 1
fi

wrong_migration_query="$test_root/wrong-diagnostics-migration.sh"
replace "$wrong_migration_query" "$script" 'max(version)::text FROM schema_migrations' 'min(version)::text FROM schema_migrations'
if ruby "$review_validator" "$wrong_migration_query" >/dev/null 2>&1; then printf '%s\n' 'contract accepted the wrong migration-version query' >&2; exit 1; fi
wrong_status_query="$test_root/wrong-diagnostics-status.sh"
replace "$wrong_status_query" "$script" 'FROM runs GROUP BY status' 'FROM workflows GROUP BY status'
if ruby "$review_validator" "$wrong_status_query" >/dev/null 2>&1; then printf '%s\n' 'contract accepted run status counts from the wrong table' >&2; exit 1; fi
wrong_namespace_query="$test_root/wrong-diagnostics-namespace.sh"
replace "$wrong_namespace_query" "$script" "table_schema='public'" "table_schema='private' OR table_schema='public-never'"
if ruby "$review_validator" "$wrong_namespace_query" >/dev/null 2>&1; then printf '%s\n' 'contract accepted a non-public table namespace query' >&2; exit 1; fi

clean_tree_fixture="$test_root/clean-tree-fixture"
clean_tree_bin="$test_root/clean-tree-bin"
mkdir -p "$clean_tree_fixture/scripts" "$clean_tree_bin"
cp "$script" "$clean_tree_fixture/scripts/test-v05-upgrade-rollback-e2e.sh"
printf '%s\n' tracked >"$clean_tree_fixture/tracked.txt"
printf '%s\n' ignored-local >"$clean_tree_fixture/.gitignore"
git -C "$clean_tree_fixture" init -q
git -C "$clean_tree_fixture" add scripts/test-v05-upgrade-rollback-e2e.sh tracked.txt .gitignore
git -C "$clean_tree_fixture" -c user.name=contract -c user.email=contract@example.invalid commit -qm fixture
printf '%s\n' ignored >"$clean_tree_fixture/ignored-local"
cat >"$clean_tree_bin/docker" <<'SH'
#!/bin/sh
printf '%s\n' "$*" >>"$CLEAN_TREE_DOCKER_LOG"
exit 19
SH
chmod +x "$clean_tree_bin/docker"

exercise_clean_tree_guard() {
  clean_tree_case=$1
  clean_tree_log="$test_root/$clean_tree_case-docker.log"
  clean_tree_tmp="$test_root/$clean_tree_case-tmp"
  mkdir "$clean_tree_tmp"
  rm -f "$clean_tree_log"
  set +e
  PATH="$clean_tree_bin:$PATH" TMPDIR="$clean_tree_tmp" CLEAN_TREE_DOCKER_LOG="$clean_tree_log" \
    RUN_PAYLOAD_ENCRYPTION_KEY=contract-test-key V04_UPGRADE_ROLLBACK_ARTIFACT_DIR="$test_root/$clean_tree_case-artifacts" \
    sh "$clean_tree_fixture/scripts/test-v05-upgrade-rollback-e2e.sh" >"$test_root/$clean_tree_case.out" 2>&1
  clean_tree_status=$?
  set -e
  [ "$clean_tree_status" -ne 0 ]
}

exercise_clean_tree_guard clean
[ -s "$test_root/clean-docker.log" ] || { printf '%s\n' 'clean source tree was rejected before preflight' >&2; exit 1; }

printf '%s\n' dirty >>"$clean_tree_fixture/tracked.txt"
exercise_clean_tree_guard dirty-tracked
[ ! -e "$test_root/dirty-tracked-docker.log" ] || { printf '%s\n' 'dirty tracked source reached Docker preflight' >&2; exit 1; }
[ -z "$(find "$test_root/dirty-tracked-tmp" -name 'agent-studio-v04-upgrade-rollback.*' -print -quit)" ] || { printf '%s\n' 'dirty tracked source created a run root' >&2; exit 1; }
git -C "$clean_tree_fixture" checkout -q -- tracked.txt

printf '%s\n' dirty >"$clean_tree_fixture/untracked.txt"
exercise_clean_tree_guard dirty-untracked
[ ! -e "$test_root/dirty-untracked-docker.log" ] || { printf '%s\n' 'dirty untracked source reached Docker preflight' >&2; exit 1; }
[ -z "$(find "$test_root/dirty-untracked-tmp" -name 'agent-studio-v04-upgrade-rollback.*' -print -quit)" ] || { printf '%s\n' 'dirty untracked source created a run root' >&2; exit 1; }
rm "$clean_tree_fixture/untracked.txt"

mv "$clean_tree_fixture/.git" "$clean_tree_fixture/.git-hidden"
exercise_clean_tree_guard git-status-error
[ ! -e "$test_root/git-status-error-docker.log" ] || { printf '%s\n' 'git status failure reached Docker preflight' >&2; exit 1; }
[ -z "$(find "$test_root/git-status-error-tmp" -name 'agent-studio-v04-upgrade-rollback.*' -print -quit)" ] || { printf '%s\n' 'git status failure created a run root' >&2; exit 1; }
mv "$clean_tree_fixture/.git-hidden" "$clean_tree_fixture/.git"

fake_bin="$test_root/fake-bin"
mkdir -p "$fake_bin" "$test_root/fake-tmp"
fake_log="$test_root/fake-docker.log"
up_marker="$test_root/up.marker"
down_marker="$test_root/down.marker"
run_root_record="$test_root/run-root"
cat >"$fake_bin/docker" <<'SH'
#!/bin/sh
printf '%s\n' "$*" >>"$FAKE_DOCKER_LOG"
case "$*" in
  "compose version") exit 0 ;;
  *"config --services"*) printf '%s\n' db; exit 0 ;;
  *"up -d db"*)
    compose_path=
    previous=
    for argument in "$@"; do
      if [ "$previous" = -f ]; then compose_path=$argument; break; fi
      previous=$argument
    done
    dirname "$compose_path" >"$FAKE_RUN_ROOT_RECORD"
    : >"$FAKE_UP_MARKER"
    exit 17
    ;;
  *"logs --no-color db"*) printf '%s\n' 'safe fake postgres log'; exit 0 ;;
  *"down --remove-orphans"*) : >"$FAKE_DOWN_MARKER"; exit 0 ;;
esac
exit 0
SH
chmod +x "$fake_bin/docker"
set +e
PATH="$fake_bin:$PATH" TMPDIR="$test_root/fake-tmp" FAKE_DOCKER_LOG="$fake_log" FAKE_UP_MARKER="$up_marker" FAKE_DOWN_MARKER="$down_marker" FAKE_RUN_ROOT_RECORD="$run_root_record" \
  RUN_PAYLOAD_ENCRYPTION_KEY=contract-test-key V04_UPGRADE_ROLLBACK_API_PORT=41001 V04_UPGRADE_ROLLBACK_DB_PORT=41002 \
  V04_UPGRADE_ROLLBACK_ARTIFACT_DIR="$test_root/fake-artifacts" sh "$clean_tree_fixture/scripts/test-v05-upgrade-rollback-e2e.sh" >"$test_root/fake-up.out" 2>&1
fake_status=$?
set -e
[ "$fake_status" -ne 0 ]
[ -f "$up_marker" ]
[ -f "$down_marker" ]
[ -f "$test_root/fake-artifacts/database-diagnostics.log" ]
for diagnostic_role in upgrade_source current rollback_target; do grep -F "role=$diagnostic_role" "$test_root/fake-artifacts/database-diagnostics.log" >/dev/null; done
fake_run_root=$(cat "$run_root_record")
[ ! -e "$fake_run_root" ]

set +e
deadline_output=$(UPGRADE_ROLLBACK_DEADLINE_SECONDS=570001 sh "$script" 2>&1)
deadline_status=$?
set -e
[ "$deadline_status" -ne 0 ]
case "$deadline_output" in *'UPGRADE_ROLLBACK_DEADLINE_SECONDS must be between 60 and 570'*) ;; *) printf '%s\n' '570001 deadline was not rejected explicitly' >&2; exit 1;; esac

extract_functions() {
  output=$1
  shift
  ruby -e '
    source=File.read(ARGV.shift)
    output=ARGV.shift
    names=ARGV
    bodies=names.map do |name|
      source[/^#{Regexp.escape(name)}\(\)\s*\{[^\n]*\}\s*$/] ||
        source[/^#{Regexp.escape(name)}\(\)\s*\{.*?^\}\s*$/m] || abort("#{name} missing")
    end
    File.write(output,bodies.join("\n"))
  ' "$script" "$output" "$@"
}

ready_functions="$test_root/ready-functions.sh"
extract_functions "$ready_functions" now_epoch_ms remaining_budget_ms before_deadline database_name database_url wait_ready
cat >"$test_root/ready-harness.sh" <<SH
#!/bin/sh
set -eu
bounded_curl() { return 0; }
curl() { return 0; }
run_bounded() { "\$@"; }
$(cat "$ready_functions")
ready_fixture_calls=0; db_port=41002; deadline_epoch_ms=\$((\$(now_epoch_ms)+5000))
postgres_exec() { ready_fixture_calls=\$((ready_fixture_calls+1)); [ "\$ready_target" = db:41002 ]; [ "\$ready_fixture_calls" -ge 2 ]; }
wait_ready db:41002 ''; [ "\$ready_fixture_calls" -eq 2 ]
database_role=sentinel; database_url upgrade_source >"$test_root/database-url"; database_url_value=\$(cat "$test_root/database-url"); case "\$database_url_value" in *:41002/upgrade_source*) ;; *) exit 1;; esac
for nested_check in "\$ready_target|db:41002" "\$database_url_role|upgrade_source" "\$database_role|sentinel"; do nested_actual=\${nested_check%%|*}; nested_expected=\${nested_check#*|}; [ "\$nested_actual" = "\$nested_expected" ]; done
wait_ready http://127.0.0.1:1/readyz 999999
SH
chmod +x "$test_root/ready-harness.sh"
if sh "$test_root/ready-harness.sh"; then printf '%s\n' 'ready 200 accepted for dead pid' >&2; exit 1; fi

bounded_functions="$test_root/bounded-functions.sh"
extract_functions "$bounded_functions" now_epoch_ms remaining_budget_ms set_safe_command_label wait_bounded run_bounded run_bounded_until go
cat >"$test_root/bounded-harness.sh" <<SH
#!/bin/sh
set -eu
$(cat "$bounded_functions")
start_epoch_ms=\$(now_epoch_ms)
deadline_epoch_ms=\$((start_epoch_ms + 2000))
last_safe_command_label=
run_bounded legacy_build sh -c 'exit 0'
current_phase=02_legacy_build; go_binary=sh; CGO_ENABLED=0 go -c '[ "\$CGO_ENABLED" = 0 ] && [ "\$1" = "argument with spaces" ]' sh 'argument with spaces'
set +e; run_bounded postgres_client sh -c 'exit 17'; exit_status=\$?; set -e
[ "\$exit_status" -eq 17 ]
export PIPE_OUT="$test_root/pipe.out" PRODUCER_MARKER="$test_root/producer.marker" CONSUMER_MARKER="$test_root/consumer.marker"
sh -c 'printf payload; : >"\$PRODUCER_MARKER"' | run_bounded archive_extract sh -c 'cat >"\$PIPE_OUT"; : >"\$CONSUMER_MARKER"'; [ -f "\$PRODUCER_MARKER" ] && [ -f "\$CONSUMER_MARKER" ] && [ "\$(cat "\$PIPE_OUT")" = payload ]
printf file-input >"$test_root/stdin.in"; run_bounded artifact_io sh -c 'cat >"\$1"' sh "$test_root/stdin.out" <"$test_root/stdin.in"; [ "\$(cat "$test_root/stdin.out")" = file-input ]
mkdir "$test_root/cwd"; : >"$test_root/cwd/marker"; expected_cwd=\$(CDPATH= cd -- "$test_root/cwd" && pwd -P); export CWD_OUT="$test_root/cwd.out"; (cd "$test_root/cwd" && run_bounded legacy_build sh -c 'pwd >"\$CWD_OUT"; [ -f marker ]'); actual_cwd=\$(CDPATH= cd -- "\$(cat "\$CWD_OUT")" && pwd -P); [ "\$actual_cwd" = "\$expected_cwd" ]
export TERM_MARKER="$test_root/term.marker" CHILD_PID_FILE="$test_root/child.pid"
if run_bounded legacy_build sh -c 'printf "%s\n" "\$\$" >"\$CHILD_PID_FILE"; trap '\''touch "\$TERM_MARKER"; exit 143'\'' TERM; while :; do sleep 1; done'; then
  exit 1
else
  status=\$?
fi
[ "\$status" -eq 124 ]
[ -f "\$TERM_MARKER" ]
child_pid=\$(cat "\$CHILD_PID_FILE")
! kill -0 "\$child_pid" 2>/dev/null
elapsed=\$((\$(now_epoch_ms)-start_epoch_ms))
[ "\$elapsed" -lt 5000 ]
SH
[ -s "$test_root/bounded-harness.sh" ] || { printf '%s\n' 'bounded harness generation failed' >&2; exit 1; }
chmod +x "$test_root/bounded-harness.sh"
if ! sh "$test_root/bounded-harness.sh"; then printf '%s\n' 'bounded command fixture failed' >&2; exit 1; fi

sensitive_functions="$test_root/sensitive-functions.sh"
extract_functions "$sensitive_functions" sensitive_literals contains_sensitive_data capture_failure_artifacts
cat >"$test_root/sensitive-harness.sh" <<SH
#!/bin/sh
set -eu
$(cat "$sensitive_functions")
RUN_PAYLOAD_ENCRYPTION_KEY=contract-key
current_request_key=contract-idempotency
export RUN_PAYLOAD_ENCRYPTION_KEY
run_bounded_until() { shift 2; "\$@"; }
run_cleanup_bounded() { shift 2; "\$@"; }
for literal in contract-key contract-idempotency legacy-public-fixture legacy-running legacy-cancelling legacy-completed current-smoke-private ciphertext-marker; do
  printf '%s\n' "\$literal" >"$test_root/sensitive.log"
  contains_sensitive_data "$test_root/sensitive.log" 9999999999999
done
artifact_dir="$test_root/safe-artifacts"; legacy_log="$test_root/sensitive.log"; current_api_log="$test_root/empty-api.log"; worker_log="$test_root/empty-worker.log"; postgres_log="$test_root/empty-postgres.log"; restore_list="$test_root/empty-restore.log"; diagnostics_log="$test_root/database-diagnostics.log"; summary_log="$test_root/failure-summary.json"
: >"\$current_api_log"; : >"\$worker_log"; : >"\$postgres_log"; : >"\$restore_list"; printf '%s\n' 'role=upgrade_source' 'status=available' 'migration_version=7' 'run_status_counts=completed:2' 'tables=run_events,runs,schema_migrations' >"\$diagnostics_log"
for literal in contract-key contract-idempotency legacy-public-fixture legacy-running legacy-cancelling legacy-completed current-smoke-private ciphertext-marker; do printf '%s\n' "\$literal"; done >"\$legacy_log"
current_phase=09_start_current_api; start_epoch_ms=0; old_commit=; dump_sha=; compose_project_id=contract01
now_epoch_ms() { printf '%s\n' 1234; }
capture_failure_artifacts 17 9999999999999
summary="\$artifact_dir/failure-summary.json"; [ -f "\$summary" ]; jq -e '.phase=="09_start_current_api" and .exitCode==17 and .elapsedMs==1234 and .logsWithheld==true and .composeProjectId=="contract01" and (keys|sort)==["composeProjectId","elapsedMs","exitCode","logsWithheld","phase"]' "\$summary" >/dev/null
for literal in contract-key contract-idempotency legacy-public-fixture legacy-running legacy-cancelling legacy-completed current-smoke-private ciphertext-marker 'sh -c' '--dbname'; do ! grep -F -- "\$literal" "\$summary" >/dev/null; done
contains_sensitive_data() { grep -F 'do-not-publish' "\$1" >/dev/null 2>&1 && return 0; [ "\${fake_scan_status:-1}" -eq 2 ] && return 2; [ "\$1" = "\$summary_log" ] && return 1; [ "\$1" = "\$legacy_log" ] && return "\$fake_scan_status"; return 1; }
for scan_case in '2|true|no' '1|false|yes'; do fake_scan_status=\${scan_case%%|*}; scan_rest=\${scan_case#*|}; expected_withheld=\${scan_rest%%|*}; expected_runtime=\${scan_rest#*|}; artifact_dir="$test_root/scan-\$fake_scan_status"; summary_log="$test_root/summary-\$fake_scan_status.json"; printf safe-log >"\$legacy_log"; capture_failure_artifacts 17 9999999999999; jq -e --argjson expected "\$expected_withheld" '.logsWithheld==\$expected' "\$artifact_dir/failure-summary.json" >/dev/null; if [ "\$expected_runtime" = yes ]; then [ -f "\$artifact_dir/runtime.log" ] && [ -f "\$artifact_dir/database-diagnostics.log" ]; else [ ! -f "\$artifact_dir/runtime.log" ] && [ ! -f "\$artifact_dir/database-diagnostics.log" ]; fi; done
artifact_dir="$test_root/unsafe-diagnostics"; summary_log="$test_root/unsafe-diagnostics-summary.json"; fake_scan_status=1; printf '%s\n' 'role=current' 'password=do-not-publish' >"\$diagnostics_log"; capture_failure_artifacts 17 9999999999999; [ -f "\$artifact_dir/withheld.log" ]; [ ! -e "\$artifact_dir/database-diagnostics.log" ]; ! grep -R -F 'do-not-publish' "\$artifact_dir" >/dev/null
SH
[ -s "$test_root/sensitive-harness.sh" ] || { printf '%s\n' 'sensitive harness generation failed' >&2; exit 1; }
chmod +x "$test_root/sensitive-harness.sh"
if ! sh "$test_root/sensitive-harness.sh"; then printf '%s\n' 'sensitive literal fixture failed' >&2; exit 1; fi

diagnostics_functions="$test_root/diagnostics-functions.sh"
extract_functions "$diagnostics_functions" database_name database_url collect_database_diagnostics
cat >"$test_root/diagnostics-harness.sh" <<SH
#!/bin/sh
set -eu
$(cat "$diagnostics_functions")
db_port=41002; compose_attempted=1; run_root="$test_root/diagnostics-run-root"; mkdir "\$run_root"; diagnostics_log="$test_root/collected-diagnostics.log"; diagnostics_calls="$test_root/diagnostics-calls.log"
run_cleanup_bounded() { shift 2; "\$@"; }
postgres_cleanup_exec() {
  printf '%s\n' "\$*" >>"\$diagnostics_calls"
  case "\$*" in
    *rollback_target*) return 17 ;;
    *) printf '%s\n' 'migration_version=7' 'run_status_counts=cancelled:1,completed:2' 'tables=run_events,runs,schema_migrations' ;;
  esac
}
collect_database_diagnostics 9999999999999
grep -F 'role=upgrade_source' "\$diagnostics_log" >/dev/null
grep -F 'role=current' "\$diagnostics_log" >/dev/null
grep -A1 -F 'role=rollback_target' "\$diagnostics_log" | grep -F 'status=unavailable_or_error' >/dev/null
grep -F 'migration_version=7' "\$diagnostics_log" >/dev/null
grep -F 'run_status_counts=cancelled:1,completed:2' "\$diagnostics_log" >/dev/null
grep -F 'tables=run_events,runs,schema_migrations' "\$diagnostics_log" >/dev/null
grep -F 'schema_migrations' "\$diagnostics_calls" >/dev/null
grep -F 'GROUP BY status' "\$diagnostics_calls" >/dev/null
grep -F 'information_schema.tables' "\$diagnostics_calls" >/dev/null
[ "\$(grep -c '/upgrade_source?' "\$diagnostics_calls")" -eq 2 ]
[ "\$(grep -c '/rollback_target?' "\$diagnostics_calls")" -eq 1 ]
! grep -E 'payload|secret|ciphertext|password|postgres(ql)?://' "\$diagnostics_log" >/dev/null
compose_attempted=0; diagnostics_log="$test_root/unavailable-diagnostics.log"; : >"\$diagnostics_calls"; collect_database_diagnostics 9999999999999
[ "\$(grep -c '^status=unavailable_or_error$' "\$diagnostics_log")" -eq 3 ]
[ ! -s "\$diagnostics_calls" ]
SH
chmod +x "$test_root/diagnostics-harness.sh"
if ! sh "$test_root/diagnostics-harness.sh"; then printf '%s\n' 'database diagnostics fixture failed' >&2; exit 1; fi

cleanup_functions="$test_root/cleanup-functions.sh"
extract_functions "$cleanup_functions" now_epoch_ms remaining_budget_ms set_safe_command_label wait_bounded run_bounded_until run_cleanup_bounded stop_process collect_postgres_logs cleanup
cat >"$fake_bin/docker" <<'SH'
#!/bin/sh
case "$*" in *"logs --no-color db"*) sleep 30;; *"down --remove-orphans"*) : >"$CLEANUP_DOWN_MARKER";; esac
SH
chmod +x "$fake_bin/docker"
cat >"$test_root/cleanup-harness.sh" <<SH
#!/bin/sh
set -eu
$(cat "$cleanup_functions")
run_root="$test_root/hanging-run-root"; mkdir "\$run_root"; compose_file="\$run_root/compose.yaml"; postgres_log="\$run_root/postgres.log"; compose_attempted=1; current_request_key=; last_safe_command_label=initializing
sh -c 'trap "" TERM; while :; do sleep 1; done' & current_worker_pid=\$!; sh -c 'trap "" TERM; while :; do sleep 1; done' & current_api_pid=\$!; sh -c 'trap "" TERM; while :; do sleep 1; done' & legacy_api_pid=\$!
cleanup 0
SH
chmod +x "$test_root/cleanup-harness.sh"
cleanup_started=$(ruby -e 'puts (Time.now.to_f*1000).to_i'); PATH="$fake_bin:$PATH" CLEANUP_DOWN_MARKER="$test_root/cleanup-down.marker" sh "$test_root/cleanup-harness.sh"; cleanup_elapsed=$(($(ruby -e 'puts (Time.now.to_f*1000).to_i')-cleanup_started))
[ -f "$test_root/cleanup-down.marker" ] && [ ! -e "$test_root/hanging-run-root" ] && [ "$cleanup_elapsed" -lt 20000 ]

printf '%s\n' 'v0.4 upgrade/rollback E2E contract passed'
