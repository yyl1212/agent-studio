#!/bin/sh
set -eu

UPGRADE_ROLLBACK_DEADLINE_SECONDS=${UPGRADE_ROLLBACK_DEADLINE_SECONDS:-570}
case "$UPGRADE_ROLLBACK_DEADLINE_SECONDS" in ''|*[!0-9]*) printf '%s\n' 'UPGRADE_ROLLBACK_DEADLINE_SECONDS must be between 60 and 570' >&2; exit 2;; esac
[ "$UPGRADE_ROLLBACK_DEADLINE_SECONDS" -ge 60 ] && [ "$UPGRADE_ROLLBACK_DEADLINE_SECONDS" -le 570 ] || {
  printf '%s\n' 'UPGRADE_ROLLBACK_DEADLINE_SECONDS must be between 60 and 570' >&2; exit 2;
}
now_epoch_ms() { ruby -e 'puts (Time.now.to_f*1000).to_i'; }
start_epoch_ms=$(now_epoch_ms)
deadline_epoch_ms=$((start_epoch_ms + UPGRADE_ROLLBACK_DEADLINE_SECONDS * 1000))
curl_binary=$(command -v curl 2>/dev/null || true)
go_binary=$(command -v go 2>/dev/null || true)
git_binary=$(command -v git 2>/dev/null || true)
grep_binary=$(command -v grep 2>/dev/null || true)
tar_binary=$(command -v tar 2>/dev/null || true)

repo_root=$(CDPATH= cd -- "$(dirname "$0")/.." && pwd)
cd "$repo_root"

assert_clean_source_tree() {
  if ! source_tree_status=$("$git_binary" -C "$repo_root" status --porcelain --untracked-files=all 2>/dev/null); then
    printf '%s\n' 'unable to verify that the upgrade source tree is clean' >&2
    return 1
  fi
  if [ -n "$source_tree_status" ]; then
    printf '%s\n' 'upgrade rollback E2E requires a clean source tree' >&2
    return 1
  fi
}
assert_clean_source_tree

postgres_image=postgres:18
workflow_id=50000000-0000-4000-8000-000000000001
workflow_version_id=50000000-0000-4000-8000-000000000002
completed_run_id=50000000-0000-4000-8000-000000000003
running_run_id=50000000-0000-4000-8000-000000000004
cancelling_run_id=50000000-0000-4000-8000-000000000005
legacy_api_pid=
current_api_pid=
current_worker_pid=
old_commit=
dump_sha=
current_run_id=
current_request_key=
current_phase=initializing
last_safe_command_label=initializing
compose_attempted=0

run_root=$(mktemp -d "${TMPDIR:-/tmp}/agent-studio-v04-upgrade-rollback.XXXXXX")
chmod 700 "$run_root"
compose_file="$run_root/compose.yaml"
dump_file="$run_root/v040-before-upgrade.dump"
restore_list="$run_root/restore-list.log"
legacy_log="$run_root/legacy-api.log"
current_api_log="$run_root/current-api.log"
worker_log="$run_root/current-worker.log"
postgres_log="$run_root/postgres.log"
diagnostics_log="$run_root/database-diagnostics.log"
summary_log="$run_root/failure-summary.json"
completed_snapshot="$run_root/completed.snapshot"
cancelling_snapshot="$run_root/cancelling.snapshot"
running_snapshot="$run_root/running.snapshot"
legacy_api="$run_root/agent-studio-api-v040"
current_api="$run_root/agent-studio-api-current"
current_worker="$run_root/agent-studio-worker-current"

artifact_dir_value=${V04_UPGRADE_ROLLBACK_ARTIFACT_DIR:-artifacts/v04-upgrade-rollback}
case "$artifact_dir_value" in
  /*) artifact_dir=$artifact_dir_value ;;
  *) artifact_dir="$repo_root/$artifact_dir_value" ;;
esac

COMPOSE_PROJECT_NAME="agent_studio_v04_upgrade_rollback_$$"
export COMPOSE_PROJECT_NAME
compose_project_id=${COMPOSE_PROJECT_NAME##*_}

remaining_budget_ms() { budget_target_ms=${1:-$deadline_epoch_ms}; budget_remaining_ms=$((budget_target_ms - $(now_epoch_ms))); [ "$budget_remaining_ms" -gt 0 ] || return 124; printf '%s\n' "$budget_remaining_ms"; }
before_deadline() { remaining_budget_ms >/dev/null; }
wait_bounded() { wait_bounded_pid=$1; if wait "$wait_bounded_pid"; then return 0; else wait_bounded_status=$?; return "$wait_bounded_status"; fi; }
set_safe_command_label() { safe_command_label_candidate=$1; case "$safe_command_label_candidate" in initializing|compose_version|compose_config|compose_up|compose_logs|compose_down|postgres_client|http_request|legacy_build|current_build|repo_read|archive_extract|log_probe|port_probe|process_identity|artifact_io) last_safe_command_label=$safe_command_label_candidate;; *) return 2;; esac; }
run_bounded_until() {
  bounded_deadline_ms=$1; bounded_command_label=$2; shift 2
  set_safe_command_label "$bounded_command_label" || return 2
  bounded_command_budget_ms=$(remaining_budget_ms "$bounded_deadline_ms") || return 124
  ruby -e 'budget=ARGV.shift.to_i; child=nil; stop=lambda{|sig| begin Process.kill(sig,-child) if child rescue Errno::ESRCH end}; child=Process.spawn(*ARGV,pgroup:true); %w[HUP INT TERM].each{|sig| Signal.trap(sig){stop.call("TERM"); sleep 0.1; stop.call("KILL"); Process.waitpid(child) rescue nil; exit 143}}; deadline=Process.clock_gettime(Process::CLOCK_MONOTONIC)+budget/1000.0; grace=[1.0,budget/1000.0].min; term_at=deadline-grace; loop do; waited=Process.waitpid(child,Process::WNOHANG); exit($?.exitstatus || 128+$?.termsig) if waited; if Process.clock_gettime(Process::CLOCK_MONOTONIC)>=term_at; stop.call("TERM"); loop do; waited=Process.waitpid(child,Process::WNOHANG); exit 124 if waited; break if Process.clock_gettime(Process::CLOCK_MONOTONIC)>=deadline; sleep 0.05; end; stop.call("KILL"); Process.waitpid(child) rescue nil; exit 124; end; sleep 0.05; end' "$bounded_command_budget_ms" "$@" <&0 &
  bounded_supervisor_pid=$!
  wait_bounded "$bounded_supervisor_pid"
}
run_bounded() { bounded_label=$1; shift; run_bounded_until "$deadline_epoch_ms" "$bounded_label" "$@"; }
run_cleanup_bounded() { cleanup_command_deadline_ms=$1; cleanup_label=$2; shift 2; run_bounded_until "$cleanup_command_deadline_ms" "$cleanup_label" "$@"; }
curl() { run_bounded http_request "$curl_binary" "$@"; }
go() { [ "$current_phase" = 02_legacy_build ] && go_build_label=legacy_build || go_build_label=current_build; run_bounded "$go_build_label" env CGO_ENABLED="${CGO_ENABLED:-}" "$go_binary" "$@"; }
git() { run_bounded repo_read "$git_binary" "$@"; }
grep() { run_bounded log_probe "$grep_binary" "$@"; }
tar() { run_bounded archive_extract "$tar_binary" "$@"; }

postgres_exec() {
  run_bounded postgres_client docker compose -f "$compose_file" exec -T db "$@"
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
  database_url_role=$1
  database_url_name=$(database_name "$database_url_role")
  printf 'postgres://agent:agent@127.0.0.1:%s/%s?sslmode=disable\n' "$db_port" "$database_url_name"
}

start_process() {
  process_binary=$1
  "$process_binary" >>"$process_log" 2>&1 &
  started_pid=$!
}

stop_process() {
  stop_pid=$1
  stop_process_deadline_ms=$2
  [ -n "$stop_pid" ] || return 0
  if kill -0 "$stop_pid" 2>/dev/null; then
    kill "$stop_pid" 2>/dev/null || true
    while kill -0 "$stop_pid" 2>/dev/null && [ "$(now_epoch_ms)" -lt "$stop_process_deadline_ms" ]; do
      sleep 0.2
    done
    if kill -0 "$stop_pid" 2>/dev/null; then
      kill -KILL "$stop_pid" 2>/dev/null || true
    fi
  fi
  wait "$stop_pid" 2>/dev/null || true
}

wait_ready() {
  ready_target=$1
  ready_pid=$2
  ready_attempts=0
  while [ "$ready_attempts" -lt 60 ] && before_deadline; do
    [ -z "$ready_pid" ] || kill -0 "$ready_pid" 2>/dev/null || return 1
    case "$ready_target" in
      http://*)
        curl --connect-timeout 1 --max-time 2 -fsS "$ready_target" >/dev/null 2>&1 && { [ -z "$ready_pid" ] || kill -0 "$ready_pid" 2>/dev/null; } && return 0
        ;;
      log:*)
        grep -F '"msg":"worker ready"' "${ready_target#log:}" >/dev/null 2>&1 && { [ -z "$ready_pid" ] || kill -0 "$ready_pid" 2>/dev/null; } && return 0
        ;;
      db:*)
        postgres_exec pg_isready -q -U agent -d postgres -p "$db_port" && return 0
        ;;
      *) return 2 ;;
    esac
    ready_attempts=$((ready_attempts + 1))
    sleep 1
  done
  return 1
}

assert_port_unused() {
  port_probe_port=$1
  if run_bounded port_probe ruby -rsocket -e 'begin; socket=TCPSocket.new("127.0.0.1",ARGV.fetch(0).to_i); socket.close; exit 0; rescue Errno::ECONNREFUSED,Errno::EHOSTUNREACH; exit 1; end' "$port_probe_port"; then return 1; else port_probe_status=$?; [ "$port_probe_status" -eq 1 ]; fi
}

assert_process_identity() {
  process_identity_pid=$1
  process_identity_binary=$2
  kill -0 "$process_identity_pid" 2>/dev/null
  process_identity_command=$(run_bounded process_identity ps -p "$process_identity_pid" -o command=)
  case "$process_identity_command" in *"$process_identity_binary"*) return 0;; *) return 1;; esac
}

assert_eq() {
  assert_actual=$1
  assert_expected=$2
  assert_label=$3
  [ "$assert_actual" = "$assert_expected" ] || return 1
}

sha256_file() {
  if command -v sha256sum >/dev/null 2>&1; then
    sha256sum "$1" | awk '{print $1}'
  else
    shasum -a 256 "$1" | awk '{print $1}'
  fi
}

sensitive_literals() {
  printf '%s\n' "$RUN_PAYLOAD_ENCRYPTION_KEY" "$current_request_key" legacy-public-fixture legacy-running legacy-cancelling legacy-completed current-smoke-private ciphertext-marker
}

contains_sensitive_data() {
  sensitive_scan_file=$1
  sensitive_scan_deadline_ms=$2
  sensitive_literals | run_bounded_until "$sensitive_scan_deadline_ms" artifact_io ruby -e 'begin; data=File.binread(ARGV.fetch(0)); literals=STDIN.each_line(chomp:true).reject(&:empty?); exit 0 if literals.any?{|value| data.include?(value)} || data.match?(/postgres(?:ql)?:\/\/|authorization|ciphertext|password/i); exit 1; rescue StandardError; exit 2; end' "$sensitive_scan_file"
}

collect_postgres_logs() {
  postgres_logs_deadline_ms=$1
  run_cleanup_bounded "$postgres_logs_deadline_ms" compose_logs docker compose -f "$compose_file" logs --no-color db >"$postgres_log" 2>&1 || printf '%s\n' 'PostgreSQL log collection failed' >"$postgres_log"
}

postgres_cleanup_exec() {
  postgres_cleanup_deadline_ms=$1
  shift
  run_cleanup_bounded "$postgres_cleanup_deadline_ms" postgres_client docker compose -f "$compose_file" exec -T db "$@"
}

collect_database_diagnostics() {
  database_diagnostics_deadline_ms=$1
  : >"$diagnostics_log"
  for database_diagnostics_role in upgrade_source current rollback_target; do
    printf 'role=%s\n' "$database_diagnostics_role" >>"$diagnostics_log"
    case "$database_diagnostics_role" in
      upgrade_source|current) database_diagnostics_database=upgrade_source ;;
      rollback_target) database_diagnostics_database=rollback_target ;;
    esac
    database_diagnostics_part="$run_root/database-diagnostics-$database_diagnostics_role.part"
    if [ "$compose_attempted" -eq 1 ] && postgres_cleanup_exec "$database_diagnostics_deadline_ms" psql --dbname="$(database_url "$database_diagnostics_database")" -X -v ON_ERROR_STOP=1 -Atc "SELECT 'migration_version=' || COALESCE((SELECT max(version)::text FROM schema_migrations),'none') UNION ALL SELECT 'run_status_counts=' || COALESCE((SELECT string_agg(status || ':' || status_count,',' ORDER BY status) FROM (SELECT status,count(*)::text AS status_count FROM runs GROUP BY status) counts),'none') UNION ALL SELECT 'tables=' || COALESCE((SELECT string_agg(table_name,',' ORDER BY table_name) FROM information_schema.tables WHERE table_schema='public'),'none')" >"$database_diagnostics_part" 2>/dev/null; then
      run_cleanup_bounded "$database_diagnostics_deadline_ms" artifact_io sh -c 'printf "%s\n" status=available; cat "$1"' sh "$database_diagnostics_part" >>"$diagnostics_log" 2>/dev/null || printf '%s\n' 'status=unavailable_or_error' >>"$diagnostics_log"
    else
      printf '%s\n' 'status=unavailable_or_error' >>"$diagnostics_log"
    fi
    run_cleanup_bounded "$database_diagnostics_deadline_ms" artifact_io rm -f "$database_diagnostics_part" >/dev/null 2>&1 || true
  done
}

capture_failure_artifacts() {
  failure_artifact_status=$1
  failure_artifact_deadline_ms=$2
  run_cleanup_bounded "$failure_artifact_deadline_ms" artifact_io mkdir -p "$artifact_dir" || return 0
  failure_artifact_unsafe=0
  for failure_artifact_candidate in "$legacy_log" "$current_api_log" "$worker_log" "$postgres_log" "$restore_list" "$diagnostics_log"; do
    [ ! -f "$failure_artifact_candidate" ] && continue
    if contains_sensitive_data "$failure_artifact_candidate" "$failure_artifact_deadline_ms"; then failure_artifact_scan_status=0; else failure_artifact_scan_status=$?; fi
    case "$failure_artifact_scan_status" in 1) :;; 0|*) failure_artifact_unsafe=1;; esac
  done
  failure_artifact_elapsed_ms=$(( $(now_epoch_ms) - start_epoch_ms ))
  run_cleanup_bounded "$failure_artifact_deadline_ms" artifact_io jq -n --arg phase "$current_phase" --argjson exitCode "$failure_artifact_status" --argjson elapsedMs "$failure_artifact_elapsed_ms" --arg peeled "$old_commit" --arg dump "$dump_sha" --argjson logsWithheld "$([ "$failure_artifact_unsafe" -eq 1 ] && printf true || printf false)" --arg project "$compose_project_id" '{phase:$phase,exitCode:$exitCode,elapsedMs:$elapsedMs,logsWithheld:$logsWithheld,composeProjectId:$project}+(if $peeled=="" then {} else {peeledCommit:$peeled} end)+(if $dump=="" then {} else {dumpSha256:$dump} end)' >"$summary_log"
  if contains_sensitive_data "$summary_log" "$failure_artifact_deadline_ms"; then failure_summary_scan_status=0; else failure_summary_scan_status=$?; fi
  if [ "$failure_summary_scan_status" -ne 1 ]; then
    failure_artifact_unsafe=1
    run_cleanup_bounded "$failure_artifact_deadline_ms" artifact_io jq '.logsWithheld=true' "$summary_log" >"$summary_log.safe" && run_cleanup_bounded "$failure_artifact_deadline_ms" artifact_io mv "$summary_log.safe" "$summary_log"
  fi
  run_cleanup_bounded "$failure_artifact_deadline_ms" artifact_io rm -f "$artifact_dir/runtime.log" "$artifact_dir/postgres.log" "$artifact_dir/restore-list.log" "$artifact_dir/database-diagnostics.log" "$artifact_dir/summary.log" "$artifact_dir/withheld.log" "$artifact_dir/failure-summary.json" || true
  run_cleanup_bounded "$failure_artifact_deadline_ms" artifact_io cp "$summary_log" "$artifact_dir/failure-summary.json" || return 0
  if [ "$failure_artifact_unsafe" -eq 1 ]; then printf '%s\n' 'failure logs withheld after sensitive-data scan' >"$artifact_dir/withheld.log"; return 0; fi
  for failure_runtime_log in "$legacy_log" "$current_api_log" "$worker_log"; do
    [ ! -f "$failure_runtime_log" ] || run_cleanup_bounded "$failure_artifact_deadline_ms" artifact_io sh -c 'cat "$1" >>"$2"' sh "$failure_runtime_log" "$artifact_dir/runtime.log" || true
  done
  [ ! -f "$postgres_log" ] || run_cleanup_bounded "$failure_artifact_deadline_ms" artifact_io cp "$postgres_log" "$artifact_dir/postgres.log" || true
  [ ! -s "$restore_list" ] || run_cleanup_bounded "$failure_artifact_deadline_ms" artifact_io cp "$restore_list" "$artifact_dir/restore-list.log" || true
  [ ! -s "$diagnostics_log" ] || run_cleanup_bounded "$failure_artifact_deadline_ms" artifact_io cp "$diagnostics_log" "$artifact_dir/database-diagnostics.log" || true
}

cleanup() {
  cleanup_status=$1
  trap - EXIT HUP INT TERM
  set +e
  cleanup_started_ms=$(now_epoch_ms); cleanup_total_deadline_ms=$((cleanup_started_ms + 20000)); cleanup_stop_deadline_ms=$((cleanup_started_ms + 4000)); cleanup_logs_deadline_ms=$((cleanup_started_ms + 7000)); cleanup_diagnostics_deadline_ms=$((cleanup_started_ms + 9000)); cleanup_artifact_deadline_ms=$((cleanup_started_ms + 10000)); cleanup_down_deadline_ms=$((cleanup_started_ms + 18000)); cleanup_remove_deadline_ms=$cleanup_total_deadline_ms
  [ -z "$current_worker_pid" ] || stop_process "$current_worker_pid" "$cleanup_stop_deadline_ms"
  [ -z "$current_api_pid" ] || stop_process "$current_api_pid" "$cleanup_stop_deadline_ms"
  [ -z "$legacy_api_pid" ] || stop_process "$legacy_api_pid" "$cleanup_stop_deadline_ms"
  [ "$compose_attempted" -eq 0 ] || collect_postgres_logs "$cleanup_logs_deadline_ms"
  if [ "$cleanup_status" -ne 0 ]; then
    collect_database_diagnostics "$cleanup_diagnostics_deadline_ms"
    capture_failure_artifacts "$cleanup_status" "$cleanup_artifact_deadline_ms"
  fi
  if [ "$compose_attempted" -eq 1 ]; then
    run_cleanup_bounded "$cleanup_down_deadline_ms" compose_down docker compose -f "$compose_file" down --remove-orphans >/dev/null 2>&1 || true
  fi
  run_cleanup_bounded "$cleanup_remove_deadline_ms" artifact_io rm -rf "$run_root" >/dev/null 2>&1 || true
  exit "$cleanup_status"
}

trap 'cleanup $?' EXIT
trap 'exit 129' HUP
trap 'exit 130' INT
trap 'exit 143' TERM

assert_annotated_v040() {
  [ "$(git cat-file -t v0.4.0)" = tag ]
  old_commit=$(git rev-parse 'v0.4.0^{}')
  [ "$(git cat-file -t "$old_commit")" = commit ]
  git cat-file -e v0.4.0:apps/api/cmd/server/main.go
  git cat-file -e v0.4.0:apps/api/migrations/000006_workflow_version_governance.sql
}

build_legacy_api() {
  mkdir -m 700 "$run_root/v040"
  git archive v0.4.0 | tar -x -C "$run_root/v040"
  (cd "$run_root/v040" && CGO_ENABLED=0 go build -trimpath -o "$legacy_api" ./apps/api/cmd/server)
  CGO_ENABLED=0 go build -trimpath -o "$current_api" ./apps/api/cmd/server
  CGO_ENABLED=0 go build -trimpath -o "$current_worker" ./apps/api/cmd/worker
}

create_database() {
  create_database_role=$1
  create_database_name=$(database_name "$create_database_role")
  postgres_exec psql -U agent -p "$db_port" --dbname=postgres -X -v ON_ERROR_STOP=1 -c "CREATE DATABASE $create_database_name" >/dev/null
  create_database_table_count=$(postgres_exec psql --dbname="$(database_url "$create_database_role")" -X -v ON_ERROR_STOP=1 -Atc "SELECT count(*) FROM pg_catalog.pg_tables WHERE schemaname='public'")
  assert_eq "$create_database_table_count" 0 empty_database
}

start_legacy_api() {
  legacy_start_role=$1
  legacy_start_url=$(database_url "$legacy_start_role")
  assert_port_unused "$api_port"
  process_log=$legacy_log
  HTTP_ADDR="127.0.0.1:$api_port"
  MODEL_PROVIDER=mock
  export HTTP_ADDR MODEL_PROVIDER
  DATABASE_URL="$legacy_start_url" start_process "$legacy_api"
  legacy_api_pid=$started_pid
  wait_ready "$base_url/readyz" "$legacy_api_pid"
  assert_process_identity "$legacy_api_pid" "$legacy_api"
}

assert_schema() {
  schema_role=$1
  schema_expected=$2
  schema_actual=$(postgres_exec psql --dbname="$(database_url "$schema_role")" -X -v ON_ERROR_STOP=1 -Atc 'SELECT max(version) FROM schema_migrations')
  assert_eq "$schema_actual" "$schema_expected" schema_version
}

seed_legacy_runs() {
  postgres_exec psql --dbname="$(database_url upgrade_source)" -X -v ON_ERROR_STOP=1 <<'SQL' >/dev/null
BEGIN;
INSERT INTO workflows (id,name,slug,description,draft_graph,draft_revision,published_version_id,agent_presentation)
VALUES (
  '50000000-0000-4000-8000-000000000001',
  'v0.4 upgrade fixture',
  'v04-upgrade-fixture',
  'upgrade rollback fixture',
  '{"schemaVersion":1,"nodes":[{"id":"start","type":"start","typeVersion":"1","position":{"x":0,"y":0},"config":{"fields":[{"key":"value","label":"Value","type":"text","required":true}]}},{"id":"end","type":"end","typeVersion":"1","position":{"x":300,"y":0},"config":{}}],"edges":[{"id":"start-end","source":"start","sourcePort":"value","target":"end","targetPort":"result"}]}'::jsonb,
  1,
  NULL,
  '{"title":"v0.4 fixture","description":"upgrade fixture","accent":"indigo","submitLabel":"Run","resultMode":"auto"}'::jsonb
);
INSERT INTO workflow_versions (id,workflow_id,version,graph,input_schema,agent_presentation)
SELECT
  '50000000-0000-4000-8000-000000000002',
  id,
  1,
  draft_graph,
  '{"type":"object","properties":{"value":{"type":"string"}},"required":["value"],"additionalProperties":false}'::jsonb,
  agent_presentation
FROM workflows
WHERE id='50000000-0000-4000-8000-000000000001';
UPDATE workflows
SET published_version_id='50000000-0000-4000-8000-000000000002'
WHERE id='50000000-0000-4000-8000-000000000001';
INSERT INTO runs (id,workflow_id,workflow_version_id,mode,status,input,output,started_at,ended_at,cancel_requested_at,heartbeat_at)
VALUES ('50000000-0000-4000-8000-000000000003','50000000-0000-4000-8000-000000000001','50000000-0000-4000-8000-000000000002','published','completed','{"value":"legacy-public-fixture"}'::jsonb,'{"result":"legacy-completed"}'::jsonb,'2026-01-02T03:04:05Z','2026-01-02T03:05:05Z',NULL,NULL);
INSERT INTO runs (id,workflow_id,draft_revision,graph_snapshot,mode,status,input,started_at,cancel_requested_at,heartbeat_at)
SELECT fixture.id,w.id,1,w.draft_graph,'test',fixture.status,fixture.input,fixture.started_at,fixture.cancel_requested_at,clock_timestamp()
FROM workflows w
CROSS JOIN (VALUES
  ('50000000-0000-4000-8000-000000000004'::uuid,'running','{"value":"legacy-running"}'::jsonb,'2026-01-02T03:06:05Z'::timestamptz,NULL::timestamptz),
  ('50000000-0000-4000-8000-000000000005'::uuid,'cancelling','{"value":"legacy-cancelling"}'::jsonb,'2026-01-02T03:07:05Z'::timestamptz,'2026-01-02T03:08:05Z'::timestamptz)
) AS fixture(id,status,input,started_at,cancel_requested_at)
WHERE w.id='50000000-0000-4000-8000-000000000001';
INSERT INTO run_events (run_id,sequence,type,status,input,output,data_bytes,timestamp)
VALUES
  ('50000000-0000-4000-8000-000000000003',1,'run.started','running','{}'::jsonb,NULL,0,'2026-01-02T03:04:05Z'),
  ('50000000-0000-4000-8000-000000000003',2,'run.completed','completed',NULL,'{}'::jsonb,0,'2026-01-02T03:05:05Z'),
  ('50000000-0000-4000-8000-000000000004',1,'run.started','running','{}'::jsonb,NULL,0,'2026-01-02T03:06:05Z'),
  ('50000000-0000-4000-8000-000000000005',1,'run.started','running','{}'::jsonb,NULL,0,'2026-01-02T03:07:05Z');
COMMIT;
SQL
  curl --connect-timeout 1 --max-time 3 -fsS "$base_url/readyz" >/dev/null
  curl --connect-timeout 1 --max-time 3 -fsS "$base_url/api/workflows/$workflow_id" | jq -e --arg id "$workflow_id" '.id == $id' >/dev/null
  curl --connect-timeout 1 --max-time 3 -fsS "$base_url/api/runs/$completed_run_id" | jq -e '.run.status == "completed"' >/dev/null
  curl --connect-timeout 1 --max-time 3 -fsS "$base_url/api/runs/$running_run_id" | jq -e '.run.status == "running"' >/dev/null
  curl --connect-timeout 1 --max-time 3 -fsS "$base_url/api/runs/$cancelling_run_id" | jq -e '.run.status == "cancelling" and .run.cancelRequestedAt != null' >/dev/null
  capture_legacy_snapshot upgrade_source
}

capture_legacy_snapshot() {
  snapshot_role=$1
  snapshot_url=$(database_url "$snapshot_role")
  postgres_exec psql --dbname="$snapshot_url" -X -v ON_ERROR_STOP=1 -AtF '|' -c "SELECT r.status,r.input->>'value',r.output->>'result',to_char(r.started_at AT TIME ZONE 'UTC','YYYY-MM-DD\"T\"HH24:MI:SS\"Z\"'),to_char(r.ended_at AT TIME ZONE 'UTC','YYYY-MM-DD\"T\"HH24:MI:SS\"Z\"'),(SELECT string_agg(e.type,',' ORDER BY e.sequence) FROM run_events e WHERE e.run_id=r.id) FROM runs r WHERE r.id='$completed_run_id'" >"$completed_snapshot"
  postgres_exec psql --dbname="$snapshot_url" -X -v ON_ERROR_STOP=1 -AtF '|' -c "SELECT r.status,r.input->>'value',to_char(r.started_at AT TIME ZONE 'UTC','YYYY-MM-DD\"T\"HH24:MI:SS\"Z\"'),to_char(r.cancel_requested_at AT TIME ZONE 'UTC','YYYY-MM-DD\"T\"HH24:MI:SS\"Z\"'),(SELECT string_agg(e.type,',' ORDER BY e.sequence) FROM run_events e WHERE e.run_id=r.id) FROM runs r WHERE r.id='$cancelling_run_id'" >"$cancelling_snapshot"
  postgres_exec psql --dbname="$snapshot_url" -X -v ON_ERROR_STOP=1 -AtF '|' -c "SELECT r.status,r.input->>'value',to_char(r.started_at AT TIME ZONE 'UTC','YYYY-MM-DD\"T\"HH24:MI:SS\"Z\"'),(SELECT string_agg(e.type,',' ORDER BY e.sequence) FROM run_events e WHERE e.run_id=r.id) FROM runs r WHERE r.id='$running_run_id'" >"$running_snapshot"
  IFS= read -r snapshot_completed_before <"$completed_snapshot"; assert_eq "$snapshot_completed_before" 'completed|legacy-public-fixture|legacy-completed|2026-01-02T03:04:05Z|2026-01-02T03:05:05Z|run.started,run.completed' legacy_completed_snapshot
  IFS= read -r snapshot_cancelling_before <"$cancelling_snapshot"; assert_eq "$snapshot_cancelling_before" 'cancelling|legacy-cancelling|2026-01-02T03:07:05Z|2026-01-02T03:08:05Z|run.started' legacy_cancelling_snapshot
  IFS= read -r snapshot_running_before <"$running_snapshot"; assert_eq "$snapshot_running_before" 'running|legacy-running|2026-01-02T03:06:05Z|run.started' legacy_running_snapshot
}

stop_legacy_api() {
  stop_process "$legacy_api_pid" "$deadline_epoch_ms"
  legacy_api_pid=
}

dump_database() {
  dump_role=$1
  [ "$dump_role" = upgrade_source ]
  postgres_exec pg_dump --format=custom --no-owner --no-privileges --dbname="$(database_url "$dump_role")" >"$dump_file"
  [ -s "$dump_file" ]
  postgres_exec pg_restore --list <"$dump_file" >"$restore_list"
  [ -s "$restore_list" ]
  dump_sha=$(sha256_file "$dump_file")
  [ "${#dump_sha}" -eq 64 ]
}

start_current_api() {
  current_api_start_role=$1
  current_api_start_url=$(database_url "$current_api_start_role")
  assert_port_unused "$api_port"
  process_log=$current_api_log
  HTTP_ADDR="127.0.0.1:$api_port"
  MODEL_PROVIDER=mock
  export HTTP_ADDR MODEL_PROVIDER
  DATABASE_URL="$current_api_start_url" start_process "$current_api"
  current_api_pid=$started_pid
  wait_ready "$base_url/readyz" "$current_api_pid"
  assert_process_identity "$current_api_pid" "$current_api"
}

assert_legacy_transition() {
  transition_status=$(postgres_exec psql --dbname="$(database_url upgrade_source)" -X -v ON_ERROR_STOP=1 -Atc "SELECT status FROM runs WHERE id='$running_run_id'")
  transition_reason=$(postgres_exec psql --dbname="$(database_url upgrade_source)" -X -v ON_ERROR_STOP=1 -Atc "SELECT recovery_reason FROM runs WHERE id='$running_run_id'")
  transition_cancelling=$(postgres_exec psql --dbname="$(database_url upgrade_source)" -X -v ON_ERROR_STOP=1 -Atc "SELECT status FROM runs WHERE id='$cancelling_run_id'")
  transition_cancelling_requested=$(postgres_exec psql --dbname="$(database_url upgrade_source)" -X -v ON_ERROR_STOP=1 -Atc "SELECT cancel_requested_at IS NOT NULL FROM runs WHERE id='$cancelling_run_id'")
  transition_completed=$(postgres_exec psql --dbname="$(database_url upgrade_source)" -X -v ON_ERROR_STOP=1 -Atc "SELECT status FROM runs WHERE id='$completed_run_id'")
  transition_completed_terminal=$(postgres_exec psql --dbname="$(database_url upgrade_source)" -X -v ON_ERROR_STOP=1 -Atc "SELECT count(*) FROM run_events WHERE run_id='$completed_run_id' AND type='run.completed'")
  transition_legacy_protocol=$(postgres_exec psql --dbname="$(database_url upgrade_source)" -X -v ON_ERROR_STOP=1 -Atc "SELECT execution_protocol FROM runs WHERE id='$running_run_id'")
  transition_legacy_lease=$(postgres_exec psql --dbname="$(database_url upgrade_source)" -X -v ON_ERROR_STOP=1 -Atc "SELECT lease_owner IS NULL FROM runs WHERE id='$running_run_id'")
  transition_payload_count=$(postgres_exec psql --dbname="$(database_url upgrade_source)" -X -v ON_ERROR_STOP=1 -Atc "SELECT count(*) FROM run_payloads WHERE run_id IN ('$completed_run_id','$running_run_id','$cancelling_run_id')")
  transition_public_input=$(postgres_exec psql --dbname="$(database_url upgrade_source)" -X -v ON_ERROR_STOP=1 -Atc "SELECT input->>'value' FROM runs WHERE id='$completed_run_id'")
  transition_completed_after=$(postgres_exec psql --dbname="$(database_url upgrade_source)" -X -v ON_ERROR_STOP=1 -AtF '|' -c "SELECT r.status,r.input->>'value',r.output->>'result',to_char(r.started_at AT TIME ZONE 'UTC','YYYY-MM-DD\"T\"HH24:MI:SS\"Z\"'),to_char(r.ended_at AT TIME ZONE 'UTC','YYYY-MM-DD\"T\"HH24:MI:SS\"Z\"'),(SELECT string_agg(e.type,',' ORDER BY e.sequence) FROM run_events e WHERE e.run_id=r.id) FROM runs r WHERE r.id='$completed_run_id'")
  transition_cancelling_after=$(postgres_exec psql --dbname="$(database_url upgrade_source)" -X -v ON_ERROR_STOP=1 -AtF '|' -c "SELECT r.status,r.input->>'value',to_char(r.started_at AT TIME ZONE 'UTC','YYYY-MM-DD\"T\"HH24:MI:SS\"Z\"'),to_char(r.cancel_requested_at AT TIME ZONE 'UTC','YYYY-MM-DD\"T\"HH24:MI:SS\"Z\"'),(SELECT string_agg(e.type,',' ORDER BY e.sequence) FROM run_events e WHERE e.run_id=r.id) FROM runs r WHERE r.id='$cancelling_run_id'")
  transition_running_content=$(postgres_exec psql --dbname="$(database_url upgrade_source)" -X -v ON_ERROR_STOP=1 -AtF '|' -c "SELECT input->>'value',to_char(started_at AT TIME ZONE 'UTC','YYYY-MM-DD\"T\"HH24:MI:SS\"Z\"'),(SELECT string_agg(e.type,',' ORDER BY e.sequence) FROM run_events e WHERE e.run_id=runs.id) FROM runs WHERE id='$running_run_id'")
  IFS= read -r transition_completed_before <"$completed_snapshot"; IFS= read -r transition_cancelling_before <"$cancelling_snapshot"
  assert_eq "$transition_status" recovery_required legacy_status
  assert_eq "$transition_reason" legacy_active_run legacy_reason
  assert_eq "$transition_cancelling" cancelling cancelling_status
  assert_eq "$transition_cancelling_requested" t cancelling_intent
  assert_eq "$transition_completed" completed completed_status
  assert_eq "$transition_completed_terminal" 1 completed_terminal_event
  assert_eq "$transition_legacy_protocol" 0 legacy_protocol
  assert_eq "$transition_legacy_lease" t legacy_lease
  assert_eq "$transition_payload_count" 0 legacy_payloads
  assert_eq "$transition_public_input" legacy-public-fixture legacy_public_input
  assert_eq "$transition_completed_after" "$transition_completed_before" completed_snapshot_unchanged
  assert_eq "$transition_completed_after" 'completed|legacy-public-fixture|legacy-completed|2026-01-02T03:04:05Z|2026-01-02T03:05:05Z|run.started,run.completed' completed_snapshot_exact
  assert_eq "$transition_cancelling_after" "$transition_cancelling_before" cancelling_snapshot_unchanged
  assert_eq "$transition_running_content" 'legacy-running|2026-01-02T03:06:05Z|run.started' running_snapshot_content
  curl --connect-timeout 1 --max-time 3 -fsS "$base_url/api/runs/$completed_run_id" | jq -e '.run.input.value == "legacy-public-fixture" and .run.status == "completed"' >/dev/null
}

start_current_worker() {
  current_worker_start_role=$1
  current_worker_start_url=$(database_url "$current_worker_start_role")
  process_log=$worker_log
  MODEL_PROVIDER=mock
  WORKER_CLAIM_INTERVAL=100ms
  WORKER_QUEUE_SAMPLE_INTERVAL=1s
  export MODEL_PROVIDER WORKER_CLAIM_INTERVAL WORKER_QUEUE_SAMPLE_INTERVAL
  DATABASE_URL="$current_worker_start_url" start_process "$current_worker"
  current_worker_pid=$started_pid
  wait_ready "log:$worker_log" "$current_worker_pid"
  assert_process_identity "$current_worker_pid" "$current_worker"
}

assert_cancelling_cancelled() {
  worker_assert_status=$(postgres_exec psql --dbname="$(database_url upgrade_source)" -X -v ON_ERROR_STOP=1 -Atc "SELECT status FROM runs WHERE id='$cancelling_run_id'")
  worker_assert_attempts=0
  while [ "$worker_assert_status" != cancelled ] && [ "$worker_assert_attempts" -lt 60 ] && before_deadline; do
    sleep 1
    worker_assert_status=$(postgres_exec psql --dbname="$(database_url upgrade_source)" -X -v ON_ERROR_STOP=1 -Atc "SELECT status FROM runs WHERE id='$cancelling_run_id'")
    worker_assert_attempts=$((worker_assert_attempts + 1))
  done
  worker_assert_node_count=$(postgres_exec psql --dbname="$(database_url upgrade_source)" -X -v ON_ERROR_STOP=1 -Atc "SELECT count(*) FROM node_runs WHERE run_id='$cancelling_run_id'")
  worker_assert_terminal_count=$(postgres_exec psql --dbname="$(database_url upgrade_source)" -X -v ON_ERROR_STOP=1 -Atc "SELECT count(*) FROM run_events WHERE run_id='$cancelling_run_id' AND type IN ('run.completed','run.failed','run.cancelled')")
  worker_assert_cancelled_count=$(postgres_exec psql --dbname="$(database_url upgrade_source)" -X -v ON_ERROR_STOP=1 -Atc "SELECT count(*) FROM run_events WHERE run_id='$cancelling_run_id' AND type='run.cancelled'")
  worker_assert_event_count=$(postgres_exec psql --dbname="$(database_url upgrade_source)" -X -v ON_ERROR_STOP=1 -Atc "SELECT count(*) FROM run_events WHERE run_id='$cancelling_run_id'")
  worker_assert_event_sequence=$(postgres_exec psql --dbname="$(database_url upgrade_source)" -X -v ON_ERROR_STOP=1 -Atc "SELECT CASE WHEN count(*)=2 AND min(sequence)=1 AND max(sequence)=2 THEN string_agg(type,',' ORDER BY sequence) ELSE 'invalid' END FROM run_events WHERE run_id='$cancelling_run_id'")
  worker_assert_running_status=$(postgres_exec psql --dbname="$(database_url upgrade_source)" -X -v ON_ERROR_STOP=1 -Atc "SELECT status FROM runs WHERE id='$running_run_id'")
  worker_assert_running_reason=$(postgres_exec psql --dbname="$(database_url upgrade_source)" -X -v ON_ERROR_STOP=1 -Atc "SELECT recovery_reason FROM runs WHERE id='$running_run_id'")
  worker_assert_running_lease=$(postgres_exec psql --dbname="$(database_url upgrade_source)" -X -v ON_ERROR_STOP=1 -Atc "SELECT lease_owner IS NULL FROM runs WHERE id='$running_run_id'")
  worker_assert_lease_token=$(postgres_exec psql --dbname="$(database_url upgrade_source)" -X -v ON_ERROR_STOP=1 -Atc "SELECT lease_token FROM runs WHERE id='$running_run_id'")
  worker_assert_running_node_count=$(postgres_exec psql --dbname="$(database_url upgrade_source)" -X -v ON_ERROR_STOP=1 -Atc "SELECT count(*) FROM node_runs WHERE run_id='$running_run_id'")
  worker_assert_running_event_count=$(postgres_exec psql --dbname="$(database_url upgrade_source)" -X -v ON_ERROR_STOP=1 -Atc "SELECT count(*) FROM run_events WHERE run_id='$running_run_id'")
  worker_assert_running_terminal_count=$(postgres_exec psql --dbname="$(database_url upgrade_source)" -X -v ON_ERROR_STOP=1 -Atc "SELECT count(*) FROM run_events WHERE run_id='$running_run_id' AND type<>'run.started'")
  assert_eq "$worker_assert_status" cancelled cancelling_status
  assert_eq "$worker_assert_node_count" 0 cancelling_node_runs
  assert_eq "$worker_assert_terminal_count" 1 cancelling_terminal_events
  assert_eq "$worker_assert_cancelled_count" 1 cancelling_cancelled_events
  assert_eq "$worker_assert_event_count" 2 cancelling_event_count
  assert_eq "$worker_assert_event_sequence" run.started,run.cancelled cancelling_event_sequence
  assert_eq "$worker_assert_running_status" recovery_required legacy_running_status
  assert_eq "$worker_assert_running_reason" legacy_active_run legacy_running_reason
  assert_eq "$worker_assert_running_lease" t legacy_running_lease
  assert_eq "$worker_assert_lease_token" 0 legacy_running_lease_token
  assert_eq "$worker_assert_running_node_count" 0 legacy_running_nodes
  assert_eq "$worker_assert_running_event_count" 1 legacy_running_started_event
  assert_eq "$worker_assert_running_terminal_count" 0 legacy_running_other_events
}

smoke_current_run() {
  smoke_slug="v05-upgrade-smoke-$$"
  smoke_created=$(jq -nc --arg slug "$smoke_slug" '{name:"v0.5 upgrade smoke",slug:$slug,description:"upgrade smoke"}' |
    curl --connect-timeout 1 --max-time 5 -fsS -H 'Content-Type: application/json' --data-binary @- "$base_url/api/workflows")
  smoke_workflow_id=$(printf '%s' "$smoke_created" | jq -er '.id')
  smoke_revision=$(printf '%s' "$smoke_created" | jq -er '.draftRevision')
  smoke_graph=$(jq -nc '{schemaVersion:1,nodes:[
    {id:"start",type:"start",typeVersion:"1",position:{x:0,y:0},config:{fields:[{key:"value",label:"Value",type:"text",required:true}]}},
    {id:"end",type:"end",typeVersion:"1",position:{x:300,y:0},config:{}}
  ],edges:[
    {id:"start-end",source:"start",sourcePort:"value",target:"end",targetPort:"result"}
  ]}')
  smoke_saved=$(jq -nc --argjson revision "$smoke_revision" --argjson graph "$smoke_graph" '{draftRevision:$revision,graph:$graph}' |
    curl --connect-timeout 1 --max-time 5 -fsS -X PUT -H 'Content-Type: application/json' --data-binary @- "$base_url/api/workflows/$smoke_workflow_id")
  smoke_revision=$(printf '%s' "$smoke_saved" | jq -er '.draftRevision')
  smoke_published=$(jq -nc --argjson revision "$smoke_revision" '{draftRevision:$revision}' |
    curl --connect-timeout 1 --max-time 5 -fsS -H 'Content-Type: application/json' --data-binary @- "$base_url/api/workflows/$smoke_workflow_id/publish")
  smoke_version_id=$(printf '%s' "$smoke_published" | jq -er '.id')
  current_request_key=$(ruby -rsecurerandom -e 'puts SecureRandom.uuid')
  smoke_response=$(jq -nc --arg version "$smoke_version_id" '{workflowVersionId:$version,input:{value:"current-smoke-private"}}' |
    curl --connect-timeout 1 --max-time 5 -fsS -H 'Content-Type: application/json' -H 'Prefer: respond-async' -H "Idempotency-Key: $current_request_key" --data-binary @- "$base_url/api/agents/$smoke_slug/runs")
  current_run_id=$(printf '%s' "$smoke_response" | jq -er '.runId')
  smoke_run_status=$(curl --connect-timeout 1 --max-time 3 -fsS "$base_url/api/runs/$current_run_id" | jq -er '.run.status')
  smoke_attempts=0
  while [ "$smoke_run_status" != completed ] && [ "$smoke_attempts" -lt 60 ] && before_deadline; do
    sleep 1
    smoke_run_status=$(curl --connect-timeout 1 --max-time 3 -fsS "$base_url/api/runs/$current_run_id" | jq -er '.run.status')
    smoke_attempts=$((smoke_attempts + 1))
  done
  smoke_terminal_count=$(postgres_exec psql --dbname="$(database_url upgrade_source)" -X -v ON_ERROR_STOP=1 -Atc "SELECT count(*) FROM run_events WHERE run_id='$current_run_id' AND type IN ('run.completed','run.failed','run.cancelled')")
  smoke_lease_count=$(postgres_exec psql --dbname="$(database_url upgrade_source)" -X -v ON_ERROR_STOP=1 -Atc "SELECT count(*) FROM runs WHERE id='$current_run_id' AND (lease_owner IS NOT NULL OR lease_expires_at IS NOT NULL)")
  smoke_protocol=$(postgres_exec psql --dbname="$(database_url upgrade_source)" -X -v ON_ERROR_STOP=1 -Atc "SELECT execution_protocol FROM runs WHERE id='$current_run_id'")
  assert_eq "$smoke_run_status" completed current_run_status
  assert_eq "$smoke_terminal_count" 1 current_terminal_events
  assert_eq "$smoke_lease_count" 0 current_leases
  assert_eq "$smoke_protocol" 1 current_protocol
  if grep -F "$RUN_PAYLOAD_ENCRYPTION_KEY" "$current_api_log" "$worker_log" >/dev/null 2>&1; then return 1; fi
  if grep -F 'current-smoke-private' "$current_api_log" "$worker_log" >/dev/null 2>&1; then return 1; fi
  if grep -F "$current_request_key" "$current_api_log" "$worker_log" >/dev/null 2>&1; then return 1; fi
}

stop_current_runtime() {
  stop_process "$current_worker_pid" "$deadline_epoch_ms"
  current_worker_pid=
  stop_process "$current_api_pid" "$deadline_epoch_ms"
  current_api_pid=
}

restore_database() {
  restore_role=$1
  [ "$restore_role" = rollback_target ]
  postgres_exec pg_restore --exit-on-error --no-owner --no-privileges --dbname="$(database_url "$restore_role")" <"$dump_file"
}

assert_rollback_records() {
  rollback_schema=$(postgres_exec psql --dbname="$(database_url rollback_target)" -X -v ON_ERROR_STOP=1 -Atc 'SELECT max(version) FROM schema_migrations')
  rollback_payload_table=$(postgres_exec psql --dbname="$(database_url rollback_target)" -X -v ON_ERROR_STOP=1 -Atc "SELECT to_regclass('public.run_payloads') IS NULL")
  rollback_legacy_count=$(postgres_exec psql --dbname="$(database_url rollback_target)" -X -v ON_ERROR_STOP=1 -Atc "SELECT count(*) FROM runs WHERE id IN ('$completed_run_id','$running_run_id','$cancelling_run_id')")
  rollback_workflow_count=$(postgres_exec psql --dbname="$(database_url rollback_target)" -X -v ON_ERROR_STOP=1 -Atc "SELECT count(*) FROM workflows WHERE id='$workflow_id' AND published_version_id='$workflow_version_id'")
  rollback_current_count=$(postgres_exec psql --dbname="$(database_url rollback_target)" -X -v ON_ERROR_STOP=1 -Atc "SELECT count(*) FROM runs WHERE id='$current_run_id'")
  rollback_completed_after=$(postgres_exec psql --dbname="$(database_url rollback_target)" -X -v ON_ERROR_STOP=1 -AtF '|' -c "SELECT r.status,r.input->>'value',r.output->>'result',to_char(r.started_at AT TIME ZONE 'UTC','YYYY-MM-DD\"T\"HH24:MI:SS\"Z\"'),to_char(r.ended_at AT TIME ZONE 'UTC','YYYY-MM-DD\"T\"HH24:MI:SS\"Z\"'),(SELECT string_agg(e.type,',' ORDER BY e.sequence) FROM run_events e WHERE e.run_id=r.id) FROM runs r WHERE r.id='$completed_run_id'")
  rollback_cancelling_after=$(postgres_exec psql --dbname="$(database_url rollback_target)" -X -v ON_ERROR_STOP=1 -AtF '|' -c "SELECT r.status,r.input->>'value',to_char(r.started_at AT TIME ZONE 'UTC','YYYY-MM-DD\"T\"HH24:MI:SS\"Z\"'),to_char(r.cancel_requested_at AT TIME ZONE 'UTC','YYYY-MM-DD\"T\"HH24:MI:SS\"Z\"'),(SELECT string_agg(e.type,',' ORDER BY e.sequence) FROM run_events e WHERE e.run_id=r.id) FROM runs r WHERE r.id='$cancelling_run_id'")
  rollback_running_after=$(postgres_exec psql --dbname="$(database_url rollback_target)" -X -v ON_ERROR_STOP=1 -AtF '|' -c "SELECT r.status,r.input->>'value',to_char(r.started_at AT TIME ZONE 'UTC','YYYY-MM-DD\"T\"HH24:MI:SS\"Z\"'),(SELECT string_agg(e.type,',' ORDER BY e.sequence) FROM run_events e WHERE e.run_id=r.id) FROM runs r WHERE r.id='$running_run_id'")
  IFS= read -r rollback_completed_before <"$completed_snapshot"; IFS= read -r rollback_cancelling_before <"$cancelling_snapshot"; IFS= read -r rollback_running_before <"$running_snapshot"
  assert_eq "$rollback_schema" 6 rollback_schema
  assert_eq "$rollback_payload_table" t no_run_payloads
  assert_eq "$rollback_legacy_count" 3 rollback_records
  assert_eq "$rollback_workflow_count" 1 rollback_workflow
  assert_eq "$rollback_current_count" 0 rollback_current_run
  assert_eq "$rollback_completed_after" "$rollback_completed_before" rollback_completed_snapshot
  assert_eq "$rollback_cancelling_after" "$rollback_cancelling_before" rollback_cancelling_snapshot
  assert_eq "$rollback_running_after" "$rollback_running_before" rollback_running_snapshot
  assert_eq "$rollback_completed_after" 'completed|legacy-public-fixture|legacy-completed|2026-01-02T03:04:05Z|2026-01-02T03:05:05Z|run.started,run.completed' rollback_completed_exact
  curl --connect-timeout 1 --max-time 3 -fsS "$base_url/api/workflows/$workflow_id" | jq -e --arg id "$workflow_id" '.id == $id' >/dev/null
  curl --connect-timeout 1 --max-time 3 -fsS "$base_url/api/runs/$completed_run_id" | jq -e '.run.status == "completed"' >/dev/null
  curl --connect-timeout 1 --max-time 3 -fsS "$base_url/api/runs/$running_run_id" | jq -e '.run.status == "running"' >/dev/null
  curl --connect-timeout 1 --max-time 3 -fsS "$base_url/api/runs/$cancelling_run_id" | jq -e '.run.status == "cancelling"' >/dev/null
  rollback_missing_code=$(curl --connect-timeout 1 --max-time 3 -sS -o /dev/null -w '%{http_code}' "$base_url/api/runs/$current_run_id")
  assert_eq "$rollback_missing_code" 404 rollback_current_run_http
}

run_upgrade_rollback() {
  current_phase=01_annotated_tag; assert_annotated_v040
  current_phase=02_legacy_build; build_legacy_api
  current_phase=03_create_upgrade_source; create_database upgrade_source
  current_phase=04_start_legacy_source; start_legacy_api upgrade_source
  current_phase=05_schema6_source; assert_schema upgrade_source 6
  current_phase=06_seed_legacy; seed_legacy_runs
  current_phase=07_stop_legacy; stop_legacy_api
  current_phase=08_dump_source; dump_database upgrade_source
  current_phase=09_start_current_api; start_current_api upgrade_source
  current_phase=10_schema7_source; assert_schema upgrade_source 7
  current_phase=11_assert_transition; assert_legacy_transition
  current_phase=12_start_current_worker; start_current_worker upgrade_source
  current_phase=13_assert_worker; assert_cancelling_cancelled
  current_phase=14_current_smoke; smoke_current_run
  current_phase=15_stop_current; stop_current_runtime
  current_phase=16_create_rollback_target; create_database rollback_target
  current_phase=17_restore_target; restore_database rollback_target
  current_phase=18_start_legacy_target; start_legacy_api rollback_target
  current_phase=19_schema6_target; assert_schema rollback_target 6
  current_phase=20_assert_rollback; assert_rollback_records
}

for command in docker jq ruby awk; do
  command -v "$command" >/dev/null 2>&1 || {
    printf '%s is required\n' "$command" >&2
    exit 2
  }
done
for binary in "$curl_binary" "$go_binary" "$git_binary" "$grep_binary" "$tar_binary"; do [ -n "$binary" ] || { printf '%s\n' 'required command is missing' >&2; exit 2; }; done
run_bounded compose_version docker compose version >/dev/null
[ -n "${RUN_PAYLOAD_ENCRYPTION_KEY:-}" ] || {
  printf '%s\n' 'RUN_PAYLOAD_ENCRYPTION_KEY is required' >&2
  exit 2
}
export RUN_PAYLOAD_ENCRYPTION_KEY

random_port() {
  ruby -rsecurerandom -e 'puts SecureRandom.random_number(40000)+20000'
}
api_port=${V04_UPGRADE_ROLLBACK_API_PORT:-$(random_port)}
db_port=${V04_UPGRADE_ROLLBACK_DB_PORT:-$(random_port)}
case "$api_port:$db_port" in
  *[!0-9:]*) printf '%s\n' 'upgrade/rollback ports must be integers' >&2; exit 2 ;;
esac
[ "$api_port" -ge 1 ] && [ "$api_port" -le 65535 ]
[ "$db_port" -ge 1 ] && [ "$db_port" -le 65535 ]
[ "$api_port" -ne "$db_port" ]
base_url="http://127.0.0.1:$api_port"

run_bounded artifact_io ruby -rfileutils -e 'FileUtils.mkdir_p(ARGV.fetch(0)); %w[runtime.log postgres.log summary.log restore-list.log withheld.log failure-summary.json].each { |name| path=File.join(ARGV.fetch(0),name); File.delete(path) if File.file?(path) }' "$artifact_dir"

cat >"$compose_file" <<YAML
services:
  db:
    image: $postgres_image
    command: ["postgres", "-p", "$db_port"]
    environment:
      POSTGRES_DB: postgres
      POSTGRES_USER: agent
      POSTGRES_PASSWORD: agent
    ports:
      - "127.0.0.1:$db_port:$db_port"
    tmpfs:
      - /var/lib/postgresql
YAML

current_phase=compose_config
actual_services=$(run_bounded compose_config docker compose -f "$compose_file" config --services)
assert_eq "$actual_services" db compose_services
if [ "${V04_UPGRADE_ROLLBACK_CONTRACT_ONLY:-}" = 1 ]; then
  printf '%s\n' 'v0.4 upgrade/rollback lightweight contract passed'
  exit 0
fi

current_phase=database_start
compose_attempted=1
run_bounded compose_up docker compose -f "$compose_file" up -d db >/dev/null
wait_ready "db:$db_port" ""

run_upgrade_rollback
stop_legacy_api

current_phase=complete
elapsed_ms=$(( $(now_epoch_ms) - start_epoch_ms ))
[ "$elapsed_ms" -le 570000 ]
printf 'v0.4.0 peeled commit: %s\n' "$old_commit"
printf 'pre-upgrade dump sha256: %s\n' "$dump_sha"
printf '%s\n' 'v0.4 migration 6 upgrade, current smoke, and second-database rollback passed'
