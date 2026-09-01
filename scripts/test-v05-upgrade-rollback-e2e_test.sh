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
  require_body(database_url, [/\Arole=\$1\z/, /\Aname=\$\(database_name ["']\$role["']\)\z/, /\Aprintf\b.*\$name/], "database_url must consume database_name and construct its URL")
  start_lines = executable_lines(function_body(code, "start_process"))
  launch = start_lines.index { |line| line.match?(/\A(?:[A-Z_][A-Z0-9_]*=\S+\s+)*["']\$binary["'].*&\z/) }
  reject!("start_process must launch the binary then capture its pid") unless launch && start_lines[launch + 1] == 'started_pid=$!'
  require_body(function_body(code, "stop_process"), [/\Apid=\$1\z/, /\Akill\s+["']\$pid["'](?:\s|\z)/, /\Await\s+["']\$pid["'](?:\s|\z)/], "stop_process must execute kill and wait")
  require_body(function_body(code, "wait_ready"), [/\Atarget=\$1\z/, /\A(?:while|for)\b/, /\A(?:curl|grep)\b/, /\Areturn\s+1\z/], "wait_ready must loop on a real readiness command")
  assert_eq = function_body(code, "assert_eq")
  require_body(assert_eq, [/\Aactual=\$1\z/, /\Aexpected=\$2\z/, /\Alabel=\$3\z/, /\A\[ ["']\$actual["'] = ["']\$expected["'] \] \|\| (?:return|exit) 1\z/], "assert_eq must execute an actual/expected comparison")
  legacy = function_body(code, "start_legacy_api")
  require_body(legacy, [/\Arole=\$1\z/, /\Aurl=\$\(database_url ["']\$role["']\)\z/, /\ADATABASE_URL=["']\$url["'] start_process ["']\$legacy_api["']\z/, /\Alegacy_api_pid=\$started_pid\z/, /\Await_ready\b/], "start_legacy_api must execute role resolution, start and wait")
  reject!("legacy API must resolve its role exactly once") unless legacy.scan(/database_url\s+["']?\$role["']?/).length == 1
  current_api = function_body(code, "start_current_api")
  require_body(current_api, [/\Arole=\$1\z/, /\Aurl=\$\(database_url ["']\$role["']\)\z/, /\ADATABASE_URL=["']\$url["'] start_process ["']\$current_api["']\z/, /\Acurrent_api_pid=\$started_pid\z/, /\Await_ready\b/], "start_current_api must execute role resolution, start and wait")
  worker = function_body(code, "start_current_worker")
  require_body(worker, [/\Arole=\$1\z/, /\Aurl=\$\(database_url ["']\$role["']\)\z/, /\ADATABASE_URL=["']\$url["'] start_process ["']\$current_worker["']\z/, /\Acurrent_worker_pid=\$started_pid\z/, /\Await_ready\b/], "start_current_worker must execute role resolution, start and wait")
  require_body(function_body(code, "stop_legacy_api"), [/\Astop_process ["']\$legacy_api_pid["']\z/, /\Alegacy_api_pid=\z/], "stop_legacy_api must stop and clear its pid")
  require_body(function_body(code, "stop_current_runtime"), [/\Astop_process ["']\$current_worker_pid["']\z/, /\Astop_process ["']\$current_api_pid["']\z/, /\Acurrent_worker_pid=\z/, /\Acurrent_api_pid=\z/], "stop_current_runtime must stop and clear both pids")
  create = function_body(code, "create_database")
  require_body(create, [/\Arole=\$1\z/, /\Aname=\$\(database_name ["']\$role["']\)\z/, /\Apostgres_exec\s+psql\b.*CREATE DATABASE.*\$name/, /\Aactual_tables=\$\(postgres_exec\s+psql\b.*database_url ["']\$role["']/, /\Aassert_eq ["']\$actual_tables["'] 0 empty_database\z/], "create_database must execute mapped creation and empty assertion")
  dump = function_body(code, "dump_database")
  require_body(dump, [/["']?\$role["']?\s+=\s+upgrade_source/, /postgres_exec\s+pg_dump\s+--format=custom/, /--dbname=["']?\$\(database_url ["']?\$role["']?\)/], "dump_database must custom-dump upgrade_source in the container")
  restore = function_body(code, "restore_database")
  require_body(restore, [/["']?\$role["']?\s+=\s+rollback_target/, /postgres_exec\s+pg_restore\s+--exit-on-error/, /--dbname=["']?\$\(database_url ["']?\$role["']?\)/], "restore_database must restore only rollback_target in the container")
  assertion_contracts = {
    "assert_schema" => [/\Aactual=\$\(postgres_exec\s+psql\b/, /schema_migrations/, /\Aexpected=\$2\z/, /\Aassert_eq ["']\$actual["'] ["']\$expected["'] schema_version\z/],
    "seed_legacy_runs" => [/postgres_exec\s+psql\b/, /INSERT/i, /running/, /cancelling/],
    "assert_legacy_transition" => [/\Astatus=\$\(postgres_exec\s+psql/, /\Areason=\$\(postgres_exec\s+psql/, /\Acancelling=\$\(postgres_exec\s+psql/, /\Aassert_eq ["']\$status["'] recovery_required/, /\Aassert_eq ["']\$reason["'] legacy_active_run/, /\Aassert_eq ["']\$cancelling["'] cancelling/],
    "assert_cancelling_cancelled" => [/\Astatus=\$\(postgres_exec\s+psql/, /\Anode_count=\$\(postgres_exec\s+psql/, /\Aassert_eq ["']\$status["'] cancelled/, /\Aassert_eq ["']\$node_count["'] 0/],
    "smoke_current_run" => [/\Arun_status=\$\(curl\b/, /\Aterminal_count=\$\(postgres_exec\s+psql/, /\Alease_count=\$\(postgres_exec\s+psql/, /\Aassert_eq ["']\$run_status["'] completed/, /\Aassert_eq ["']\$terminal_count["'] 1/, /\Aassert_eq ["']\$lease_count["'] 0/],
    "assert_rollback_records" => [/\Aschema=\$\(postgres_exec\s+psql/, /\Apayload_table=\$\(postgres_exec\s+psql/, /\Alegacy_count=\$\(postgres_exec\s+psql/, /\Aassert_eq ["']\$schema["'] 6/, /\Aassert_eq ["']\$payload_table["'] t/, /\Aassert_eq ["']\$legacy_count["'] 3/],
  }
  assertion_contracts.each { |name, markers| require_body(function_body(code, name), markers, "#{name} must query actual values and call assert_eq") }
  executable_lines(code).each_with_index do |line, index|
    client = line.index(/\b(?:psql|pg_dump|pg_restore)\b/)
    next unless client
    wrapper = line.index(/\bpostgres_exec\b/)
    reject!("PostgreSQL client bypasses postgres_exec at line #{index + 1}") unless wrapper && wrapper < client
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
  safe_upload = uploads.length == 1 && upload["if"] == "failure()" && upload.dig("with", "name") == "v04-upgrade-rollback-logs" &&
                upload["uses"].match?(/\Aactions\/upload-artifact@[0-9a-f]{40}\z/) && upload.dig("with", "path") == "artifacts/v04-upgrade-rollback/*.log" &&
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
  role=$1
  case "$role" in
    upgrade_source) printf '%s\n' upgrade_source ;;
    rollback_target) printf '%s\n' rollback_target ;;
    *) return 2 ;;
  esac
}
database_url() {
  role=$1; name=$(database_name "$role"); printf 'postgresql://agent:agent@localhost/%s?sslmode=disable\n' "$name"
}
start_process() {
  binary=$1
  "$binary" >>"$process_log" 2>&1 &
  started_pid=$!
}
stop_process() {
  pid=$1; kill "$pid" 2>/dev/null || true; wait "$pid" 2>/dev/null || true
}
wait_ready() {
  target=$1
  attempts=0
  while [ "$attempts" -lt 30 ]; do
    curl -fsS "$target" && return 0
    attempts=$((attempts + 1))
  done
  return 1
}
assert_eq() {
  actual=$1; expected=$2; label=$3; [ "$actual" = "$expected" ] || return 1
}
assert_annotated_v040() {
  [ "$(git cat-file -t v0.4.0)" = tag ]; old_commit=$(git rev-parse 'v0.4.0^{}'); [ "$(git cat-file -t "$old_commit")" = commit ]
}
build_legacy_api() {
  mkdir -m 700 "$run_root/v040"; git archive v0.4.0 | tar -x -C "$run_root/v040"; (cd "$run_root/v040" && CGO_ENABLED=0 go build -o "$legacy_api" ./apps/api/cmd/server)
}
create_database() {
  role=$1; name=$(database_name "$role"); postgres_exec psql --dbname=postgres -v ON_ERROR_STOP=1 -c "CREATE DATABASE $name"; actual_tables=$(postgres_exec psql --dbname="$(database_url "$role")" -Atc 'SELECT count(*) FROM pg_catalog.pg_tables'); assert_eq "$actual_tables" 0 empty_database
}
start_legacy_api() {
  role=$1; url=$(database_url "$role"); DATABASE_URL="$url" start_process "$legacy_api"; legacy_api_pid=$started_pid; wait_ready "$legacy_api_url/readyz"
}
assert_schema() {
  role=$1; expected=$2; actual=$(postgres_exec psql --dbname="$(database_url "$role")" -Atc 'SELECT max(version) FROM schema_migrations'); assert_eq "$actual" "$expected" schema_version
}
seed_legacy_runs() {
  postgres_exec psql --dbname="$(database_url upgrade_source)" -c "INSERT INTO runs(status) VALUES ('running'), ('cancelling')"
}
stop_legacy_api() {
  stop_process "$legacy_api_pid"; legacy_api_pid=
}
dump_database() {
  role=$1; [ "$role" = upgrade_source ]; postgres_exec pg_dump --format=custom --dbname="$(database_url "$role")" --file="$run_root/v040.dump"
}
start_current_api() {
  role=$1; url=$(database_url "$role"); DATABASE_URL="$url" start_process "$current_api"; current_api_pid=$started_pid; wait_ready "$current_api_url/readyz"
}
assert_legacy_transition() {
  status=$(postgres_exec psql --dbname="$(database_url upgrade_source)" -Atc 'SELECT status FROM runs WHERE id = legacy_running'); reason=$(postgres_exec psql --dbname="$(database_url upgrade_source)" -Atc 'SELECT recovery_reason FROM runs WHERE id = legacy_running'); cancelling=$(postgres_exec psql --dbname="$(database_url upgrade_source)" -Atc 'SELECT status FROM runs WHERE id = legacy_cancelling'); assert_eq "$status" recovery_required legacy_status; assert_eq "$reason" legacy_active_run legacy_reason; assert_eq "$cancelling" cancelling cancelling_status
}
start_current_worker() {
  role=$1; url=$(database_url "$role"); DATABASE_URL="$url" start_process "$current_worker"; current_worker_pid=$started_pid; wait_ready "$worker_log"
}
assert_cancelling_cancelled() {
  status=$(postgres_exec psql --dbname="$(database_url upgrade_source)" -Atc 'SELECT status FROM runs WHERE id = legacy_cancelling'); node_count=$(postgres_exec psql --dbname="$(database_url upgrade_source)" -Atc 'SELECT count(*) FROM node_runs WHERE run_id = legacy_cancelling'); assert_eq "$status" cancelled cancelling_status; assert_eq "$node_count" 0 cancelling_node_runs
}
smoke_current_run() {
  run_status=$(curl -fsS "$current_api_url/api/runs/current" | jq -r .status); terminal_count=$(postgres_exec psql --dbname="$(database_url upgrade_source)" -Atc 'SELECT count(*) FROM run_events WHERE terminal'); lease_count=$(postgres_exec psql --dbname="$(database_url upgrade_source)" -Atc 'SELECT count(*) FROM runs WHERE lease_owner IS NOT NULL'); assert_eq "$run_status" completed current_run_status; assert_eq "$terminal_count" 1 current_terminal_events; assert_eq "$lease_count" 0 current_leases
}
stop_current_runtime() {
  stop_process "$current_worker_pid"; current_worker_pid=; stop_process "$current_api_pid"; current_api_pid=
}
restore_database() {
  role=$1; [ "$role" = rollback_target ]; postgres_exec pg_restore --exit-on-error --dbname="$(database_url "$role")" "$run_root/v040.dump"
}
assert_rollback_records() {
  schema=$(postgres_exec psql --dbname="$(database_url rollback_target)" -Atc 'SELECT max(version) FROM schema_migrations'); payload_table=$(postgres_exec psql --dbname="$(database_url rollback_target)" -Atc "SELECT to_regclass('run_payloads') IS NULL"); legacy_count=$(postgres_exec psql --dbname="$(database_url rollback_target)" -Atc 'SELECT count(*) FROM runs'); assert_eq "$schema" 6 rollback_schema; assert_eq "$payload_table" t no_run_payloads; assert_eq "$legacy_count" 3 rollback_records
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
        with: {name: v04-upgrade-rollback-logs, path: "artifacts/v04-upgrade-rollback/*.log", retention-days: 7}
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
missing_assert="$test_root/missing-assert.sh"; replace "$missing_assert" "$valid_script" 'assert_eq "$reason" legacy_active_run legacy_reason' ':'; sh -n "$missing_assert"; expect_rejected script "$missing_assert" "assert_legacy_transition"
noop_compare="$test_root/noop-compare.sh"; replace "$noop_compare" "$valid_script" '[ "$actual" = "$expected" ] || return 1' '[ 0 = 0 ] || return 1'; sh -n "$noop_compare"; expect_rejected script "$noop_compare" "assert_eq must execute"
hardcoded_database="$test_root/hardcoded-database.sh"; replace "$hardcoded_database" "$valid_script" 'role=$1; name=$(database_name "$role"); postgres_exec psql' 'role=$1; name=upgrade_source; postgres_exec psql'; sh -n "$hardcoded_database"; expect_rejected script "$hardcoded_database" "create_database must execute"
ignored_name="$test_root/ignored-name.sh"; replace "$ignored_name" "$valid_script" "printf 'postgresql://agent:agent@localhost/%s?sslmode=disable\\n' \"\$name\"" ": \"\$name\"; printf 'postgresql://agent:agent@localhost/upgrade_source?sslmode=disable\\n'"; sh -n "$ignored_name"; expect_rejected script "$ignored_name" "database_url must consume"
inert_create="$test_root/inert-create.sh"; replace "$inert_create" "$valid_script" 'postgres_exec psql --dbname=postgres -v ON_ERROR_STOP=1 -c "CREATE DATABASE $name"' ': "postgres_exec psql CREATE DATABASE $name"'; sh -n "$inert_create"; expect_rejected script "$inert_create" "create_database must execute"
missing_cancelling="$test_root/missing-cancelling.sh"; replace "$missing_cancelling" "$valid_script" 'assert_eq "$cancelling" cancelling cancelling_status' ':'; sh -n "$missing_cancelling"; expect_rejected script "$missing_cancelling" "assert_legacy_transition"
dangerous="$test_root/dangerous.sh"; cp "$valid_script" "$dangerous"; printf '%s\n' "postgres_exec psql -c 'ALTER TABLE runs DROP COLUMN status'" >>"$dangerous"; sh -n "$dangerous"; expect_rejected script "$dangerous" "forbidden inverse"
unsafe_artifact="$test_root/unsafe-artifact.yml"; replace "$unsafe_artifact" "$valid_workflow" 'name: v04-upgrade-rollback-logs' 'name: unsafe-database-artifact'; expect_rejected workflow "$unsafe_artifact" "exact safe log contract"
dump_upload="$test_root/dump-upload.yml"; replace "$dump_upload" "$valid_workflow" 'artifacts/v04-upgrade-rollback/*.log' 'artifacts/v04-upgrade-rollback/*.dump'; expect_rejected workflow "$dump_upload" "never upload a database dump"

ruby -e 'm=File.read("Makefile"); raise unless m.match?(/^test-v05-upgrade-rollback-e2e:\n\tsh scripts\/test-v05-upgrade-rollback-e2e\.sh$/) && m.lines.grep(/^\.PHONY:/).join.include?("test-v05-upgrade-rollback-e2e")'
make --no-print-directory -n test-v05-upgrade-rollback-e2e >/dev/null
failures=0
script=scripts/test-v05-upgrade-rollback-e2e.sh
if [ ! -f "$script" ]; then printf '%s\n' "$script is missing" >&2; failures=1
elif ! sh -n "$script" || ! ruby -ryaml "$validator" script "$script"; then failures=1; fi
if ! ruby -ryaml "$validator" workflow .github/workflows/ci.yml; then failures=1; fi
[ "$failures" -eq 0 ] || exit 1
printf '%s\n' 'v0.4 upgrade/rollback E2E contract passed'
