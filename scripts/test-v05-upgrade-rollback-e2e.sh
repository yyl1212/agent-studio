#!/bin/sh
set -eu

repo_root=$(CDPATH= cd -- "$(dirname "$0")/.." && pwd)
cd "$repo_root"

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
phase=initializing
compose_started=0

run_root=$(mktemp -d "${TMPDIR:-/tmp}/agent-studio-v04-upgrade-rollback.XXXXXX")
chmod 700 "$run_root"
compose_file="$run_root/compose.yaml"
dump_file="$run_root/v040-before-upgrade.dump"
restore_list="$run_root/restore-list.log"
legacy_log="$run_root/legacy-api.log"
current_api_log="$run_root/current-api.log"
worker_log="$run_root/current-worker.log"
legacy_api="$run_root/agent-studio-api-v040"
current_api="$run_root/agent-studio-api-current"
current_worker="$run_root/agent-studio-worker-current"
deadline_epoch=$(( $(date +%s) + 570 ))

artifact_dir_value=${V04_UPGRADE_ROLLBACK_ARTIFACT_DIR:-artifacts/v04-upgrade-rollback}
case "$artifact_dir_value" in
  /*) artifact_dir=$artifact_dir_value ;;
  *) artifact_dir="$repo_root/$artifact_dir_value" ;;
esac

COMPOSE_PROJECT_NAME="agent_studio_v04_upgrade_rollback_$$"
export COMPOSE_PROJECT_NAME

before_deadline() {
  [ "$(date +%s)" -lt "$deadline_epoch" ]
}

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
  role=$1
  name=$(database_name "$role")
  printf 'postgres://agent:agent@127.0.0.1:%s/%s?sslmode=disable\n' "$db_port" "$name"
}

start_process() {
  binary=$1
  "$binary" >>"$process_log" 2>&1 &
  started_pid=$!
}

stop_process() {
  pid=$1
  [ -n "$pid" ] || return 0
  if kill -0 "$pid" 2>/dev/null; then
    kill "$pid" 2>/dev/null || true
    stop_deadline=$(( $(date +%s) + 10 ))
    while kill -0 "$pid" 2>/dev/null && [ "$(date +%s)" -lt "$stop_deadline" ] && before_deadline; do
      sleep 1
    done
    if kill -0 "$pid" 2>/dev/null; then
      kill -KILL "$pid" 2>/dev/null || true
    fi
  fi
  wait "$pid" 2>/dev/null || true
}

wait_ready() {
  target=$1
  attempts=0
  while [ "$attempts" -lt 60 ] && before_deadline; do
    case "$target" in
      http://*)
        curl --connect-timeout 1 --max-time 2 -fsS "$target" >/dev/null 2>&1 && return 0
        ;;
      log:*)
        grep -F '"msg":"worker ready"' "${target#log:}" >/dev/null 2>&1 && return 0
        ;;
      db:*)
        postgres_exec pg_isready -q -U agent -d postgres -p "$db_port" && return 0
        ;;
      *) return 2 ;;
    esac
    attempts=$((attempts + 1))
    sleep 1
  done
  return 1
}

assert_eq() {
  actual=$1
  expected=$2
  label=$3
  [ "$actual" = "$expected" ] || return 1
}

sha256_file() {
  if command -v sha256sum >/dev/null 2>&1; then
    sha256sum "$1" | awk '{print $1}'
  else
    shasum -a 256 "$1" | awk '{print $1}'
  fi
}

capture_failure_artifacts() {
  mkdir -p "$artifact_dir"
  ruby -rfileutils -e '%w[runtime.log summary.log restore-list.log].each { |name| path=File.join(ARGV.fetch(0),name); File.delete(path) if File.file?(path) }' "$artifact_dir"
  {
    printf 'phase=%s\n' "$phase"
    [ -z "$old_commit" ] || printf 'old_commit=%s\n' "$old_commit"
    [ -z "$dump_sha" ] || printf 'dump_sha256=%s\n' "$dump_sha"
  } >"$artifact_dir/summary.log"
  ruby -e '
    output=ARGV.shift
    forbidden=[ENV.fetch("RUN_PAYLOAD_ENCRYPTION_KEY", ""), "legacy-public-fixture", "current-smoke-private", ENV.fetch("CURRENT_REQUEST_KEY", "")].reject(&:empty?)
    data=ARGV.map { |path| File.file?(path) ? File.binread(path) : nil }.compact.join
    unsafe=forbidden.any? { |value| data.include?(value) } || data.match?(/postgres(?:ql)?:\/\//i) || data.match?(/authorization|ciphertext|password/i)
    File.binwrite(output, unsafe ? "runtime log withheld after sensitive-data scan\n" : data)
  ' "$artifact_dir/runtime.log" "$legacy_log" "$current_api_log" "$worker_log"
  if [ -s "$restore_list" ]; then
    cp "$restore_list" "$artifact_dir/restore-list.log"
  fi
}

cleanup() {
  status=$1
  trap - EXIT HUP INT TERM
  set +e
  [ -z "$current_worker_pid" ] || stop_process "$current_worker_pid"
  [ -z "$current_api_pid" ] || stop_process "$current_api_pid"
  [ -z "$legacy_api_pid" ] || stop_process "$legacy_api_pid"
  if [ "$status" -ne 0 ]; then
    CURRENT_REQUEST_KEY=$current_request_key
    export CURRENT_REQUEST_KEY
    capture_failure_artifacts
  fi
  if [ "$compose_started" -eq 1 ]; then
    docker compose -f "$compose_file" down --remove-orphans >/dev/null 2>&1 || true
  fi
  rm -rf "$run_root"
  exit "$status"
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
  role=$1
  name=$(database_name "$role")
  postgres_exec psql -U agent -p "$db_port" --dbname=postgres -X -v ON_ERROR_STOP=1 -c "CREATE DATABASE $name" >/dev/null
  actual_tables=$(postgres_exec psql --dbname="$(database_url "$role")" -X -v ON_ERROR_STOP=1 -Atc "SELECT count(*) FROM pg_catalog.pg_tables WHERE schemaname='public'")
  assert_eq "$actual_tables" 0 empty_database
}

start_legacy_api() {
  role=$1
  url=$(database_url "$role")
  process_log=$legacy_log
  HTTP_ADDR="127.0.0.1:$api_port"
  MODEL_PROVIDER=mock
  export HTTP_ADDR MODEL_PROVIDER
  DATABASE_URL="$url" start_process "$legacy_api"
  legacy_api_pid=$started_pid
  wait_ready "$base_url/readyz"
}

assert_schema() {
  role=$1
  expected=$2
  actual=$(postgres_exec psql --dbname="$(database_url "$role")" -X -v ON_ERROR_STOP=1 -Atc 'SELECT max(version) FROM schema_migrations')
  assert_eq "$actual" "$expected" schema_version
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
VALUES ('50000000-0000-4000-8000-000000000003','50000000-0000-4000-8000-000000000001','50000000-0000-4000-8000-000000000002','published','completed','{"value":"legacy-public-fixture"}'::jsonb,'{"result":"legacy-completed"}'::jsonb,clock_timestamp()-interval '3 minutes',clock_timestamp()-interval '2 minutes',NULL,NULL);
INSERT INTO runs (id,workflow_id,draft_revision,graph_snapshot,mode,status,input,started_at,cancel_requested_at,heartbeat_at)
SELECT fixture.id,w.id,1,w.draft_graph,'test',fixture.status,fixture.input,fixture.started_at,fixture.cancel_requested_at,clock_timestamp()
FROM workflows w
CROSS JOIN (VALUES
  ('50000000-0000-4000-8000-000000000004'::uuid,'running','{"value":"legacy-running"}'::jsonb,clock_timestamp()-interval '2 minutes',NULL::timestamptz),
  ('50000000-0000-4000-8000-000000000005'::uuid,'cancelling','{"value":"legacy-cancelling"}'::jsonb,clock_timestamp()-interval '1 minute',clock_timestamp())
) AS fixture(id,status,input,started_at,cancel_requested_at)
WHERE w.id='50000000-0000-4000-8000-000000000001';
INSERT INTO run_events (run_id,sequence,type,status,input,output,data_bytes,timestamp)
VALUES
  ('50000000-0000-4000-8000-000000000003',1,'run.started','running','{}'::jsonb,NULL,0,clock_timestamp()-interval '3 minutes'),
  ('50000000-0000-4000-8000-000000000003',2,'run.completed','completed',NULL,'{}'::jsonb,0,clock_timestamp()-interval '2 minutes'),
  ('50000000-0000-4000-8000-000000000004',1,'run.started','running','{}'::jsonb,NULL,0,clock_timestamp()-interval '2 minutes'),
  ('50000000-0000-4000-8000-000000000005',1,'run.started','running','{}'::jsonb,NULL,0,clock_timestamp()-interval '1 minute');
COMMIT;
SQL
  curl --connect-timeout 1 --max-time 3 -fsS "$base_url/readyz" >/dev/null
  curl --connect-timeout 1 --max-time 3 -fsS "$base_url/api/workflows/$workflow_id" | jq -e --arg id "$workflow_id" '.id == $id' >/dev/null
  curl --connect-timeout 1 --max-time 3 -fsS "$base_url/api/runs/$completed_run_id" | jq -e '.run.status == "completed"' >/dev/null
  curl --connect-timeout 1 --max-time 3 -fsS "$base_url/api/runs/$running_run_id" | jq -e '.run.status == "running"' >/dev/null
  curl --connect-timeout 1 --max-time 3 -fsS "$base_url/api/runs/$cancelling_run_id" | jq -e '.run.status == "cancelling" and .run.cancelRequestedAt != null' >/dev/null
}

stop_legacy_api() {
  stop_process "$legacy_api_pid"
  legacy_api_pid=
}

dump_database() {
  role=$1
  [ "$role" = upgrade_source ]
  postgres_exec pg_dump --format=custom --no-owner --no-privileges --dbname="$(database_url "$role")" >"$dump_file"
  [ -s "$dump_file" ]
  postgres_exec pg_restore --list <"$dump_file" >"$restore_list"
  [ -s "$restore_list" ]
  dump_sha=$(sha256_file "$dump_file")
  [ "${#dump_sha}" -eq 64 ]
}

start_current_api() {
  role=$1
  url=$(database_url "$role")
  process_log=$current_api_log
  HTTP_ADDR="127.0.0.1:$api_port"
  MODEL_PROVIDER=mock
  export HTTP_ADDR MODEL_PROVIDER
  DATABASE_URL="$url" start_process "$current_api"
  current_api_pid=$started_pid
  wait_ready "$base_url/readyz"
}

assert_legacy_transition() {
  status=$(postgres_exec psql --dbname="$(database_url upgrade_source)" -X -v ON_ERROR_STOP=1 -Atc "SELECT status FROM runs WHERE id='$running_run_id'")
  reason=$(postgres_exec psql --dbname="$(database_url upgrade_source)" -X -v ON_ERROR_STOP=1 -Atc "SELECT recovery_reason FROM runs WHERE id='$running_run_id'")
  cancelling=$(postgres_exec psql --dbname="$(database_url upgrade_source)" -X -v ON_ERROR_STOP=1 -Atc "SELECT status FROM runs WHERE id='$cancelling_run_id'")
  cancelling_requested=$(postgres_exec psql --dbname="$(database_url upgrade_source)" -X -v ON_ERROR_STOP=1 -Atc "SELECT cancel_requested_at IS NOT NULL FROM runs WHERE id='$cancelling_run_id'")
  completed=$(postgres_exec psql --dbname="$(database_url upgrade_source)" -X -v ON_ERROR_STOP=1 -Atc "SELECT status FROM runs WHERE id='$completed_run_id'")
  completed_terminal=$(postgres_exec psql --dbname="$(database_url upgrade_source)" -X -v ON_ERROR_STOP=1 -Atc "SELECT count(*) FROM run_events WHERE run_id='$completed_run_id' AND type='run.completed'")
  legacy_protocol=$(postgres_exec psql --dbname="$(database_url upgrade_source)" -X -v ON_ERROR_STOP=1 -Atc "SELECT execution_protocol FROM runs WHERE id='$running_run_id'")
  legacy_lease=$(postgres_exec psql --dbname="$(database_url upgrade_source)" -X -v ON_ERROR_STOP=1 -Atc "SELECT lease_owner IS NULL FROM runs WHERE id='$running_run_id'")
  payload_count=$(postgres_exec psql --dbname="$(database_url upgrade_source)" -X -v ON_ERROR_STOP=1 -Atc "SELECT count(*) FROM run_payloads WHERE run_id IN ('$completed_run_id','$running_run_id','$cancelling_run_id')")
  public_input=$(postgres_exec psql --dbname="$(database_url upgrade_source)" -X -v ON_ERROR_STOP=1 -Atc "SELECT input->>'value' FROM runs WHERE id='$completed_run_id'")
  assert_eq "$status" recovery_required legacy_status
  assert_eq "$reason" legacy_active_run legacy_reason
  assert_eq "$cancelling" cancelling cancelling_status
  assert_eq "$cancelling_requested" t cancelling_intent
  assert_eq "$completed" completed completed_status
  assert_eq "$completed_terminal" 1 completed_terminal_event
  assert_eq "$legacy_protocol" 0 legacy_protocol
  assert_eq "$legacy_lease" t legacy_lease
  assert_eq "$payload_count" 0 legacy_payloads
  assert_eq "$public_input" legacy-public-fixture legacy_public_input
  curl --connect-timeout 1 --max-time 3 -fsS "$base_url/api/runs/$completed_run_id" | jq -e '.run.input.value == "legacy-public-fixture" and .run.status == "completed"' >/dev/null
}

start_current_worker() {
  role=$1
  url=$(database_url "$role")
  process_log=$worker_log
  MODEL_PROVIDER=mock
  WORKER_CLAIM_INTERVAL=100ms
  WORKER_QUEUE_SAMPLE_INTERVAL=1s
  export MODEL_PROVIDER WORKER_CLAIM_INTERVAL WORKER_QUEUE_SAMPLE_INTERVAL
  DATABASE_URL="$url" start_process "$current_worker"
  current_worker_pid=$started_pid
  wait_ready "log:$worker_log"
}

assert_cancelling_cancelled() {
  status=$(postgres_exec psql --dbname="$(database_url upgrade_source)" -X -v ON_ERROR_STOP=1 -Atc "SELECT status FROM runs WHERE id='$cancelling_run_id'")
  attempts=0
  while [ "$status" != cancelled ] && [ "$attempts" -lt 60 ] && before_deadline; do
    sleep 1
    status=$(postgres_exec psql --dbname="$(database_url upgrade_source)" -X -v ON_ERROR_STOP=1 -Atc "SELECT status FROM runs WHERE id='$cancelling_run_id'")
    attempts=$((attempts + 1))
  done
  node_count=$(postgres_exec psql --dbname="$(database_url upgrade_source)" -X -v ON_ERROR_STOP=1 -Atc "SELECT count(*) FROM node_runs WHERE run_id='$cancelling_run_id'")
  terminal_count=$(postgres_exec psql --dbname="$(database_url upgrade_source)" -X -v ON_ERROR_STOP=1 -Atc "SELECT count(*) FROM run_events WHERE run_id='$cancelling_run_id' AND type IN ('run.completed','run.failed','run.cancelled')")
  cancelled_count=$(postgres_exec psql --dbname="$(database_url upgrade_source)" -X -v ON_ERROR_STOP=1 -Atc "SELECT count(*) FROM run_events WHERE run_id='$cancelling_run_id' AND type='run.cancelled'")
  running_status=$(postgres_exec psql --dbname="$(database_url upgrade_source)" -X -v ON_ERROR_STOP=1 -Atc "SELECT status FROM runs WHERE id='$running_run_id'")
  running_reason=$(postgres_exec psql --dbname="$(database_url upgrade_source)" -X -v ON_ERROR_STOP=1 -Atc "SELECT recovery_reason FROM runs WHERE id='$running_run_id'")
  running_lease=$(postgres_exec psql --dbname="$(database_url upgrade_source)" -X -v ON_ERROR_STOP=1 -Atc "SELECT lease_owner IS NULL FROM runs WHERE id='$running_run_id'")
  assert_eq "$status" cancelled cancelling_status
  assert_eq "$node_count" 0 cancelling_node_runs
  assert_eq "$terminal_count" 1 cancelling_terminal_events
  assert_eq "$cancelled_count" 1 cancelling_cancelled_events
  assert_eq "$running_status" recovery_required legacy_running_status
  assert_eq "$running_reason" legacy_active_run legacy_running_reason
  assert_eq "$running_lease" t legacy_running_lease
}

smoke_current_run() {
  slug="v05-upgrade-smoke-$$"
  created=$(jq -nc --arg slug "$slug" '{name:"v0.5 upgrade smoke",slug:$slug,description:"upgrade smoke"}' |
    curl --connect-timeout 1 --max-time 5 -fsS -H 'Content-Type: application/json' --data-binary @- "$base_url/api/workflows")
  smoke_workflow_id=$(printf '%s' "$created" | jq -er '.id')
  revision=$(printf '%s' "$created" | jq -er '.draftRevision')
  graph=$(jq -nc '{schemaVersion:1,nodes:[
    {id:"start",type:"start",typeVersion:"1",position:{x:0,y:0},config:{fields:[{key:"value",label:"Value",type:"text",required:true}]}},
    {id:"end",type:"end",typeVersion:"1",position:{x:300,y:0},config:{}}
  ],edges:[
    {id:"start-end",source:"start",sourcePort:"value",target:"end",targetPort:"result"}
  ]}')
  saved=$(jq -nc --argjson revision "$revision" --argjson graph "$graph" '{draftRevision:$revision,graph:$graph}' |
    curl --connect-timeout 1 --max-time 5 -fsS -X PUT -H 'Content-Type: application/json' --data-binary @- "$base_url/api/workflows/$smoke_workflow_id")
  revision=$(printf '%s' "$saved" | jq -er '.draftRevision')
  published=$(jq -nc --argjson revision "$revision" '{draftRevision:$revision}' |
    curl --connect-timeout 1 --max-time 5 -fsS -H 'Content-Type: application/json' --data-binary @- "$base_url/api/workflows/$smoke_workflow_id/publish")
  smoke_version_id=$(printf '%s' "$published" | jq -er '.id')
  current_request_key=$(ruby -rsecurerandom -e 'puts SecureRandom.uuid')
  response=$(jq -nc --arg version "$smoke_version_id" '{workflowVersionId:$version,input:{value:"current-smoke-private"}}' |
    curl --connect-timeout 1 --max-time 5 -fsS -H 'Content-Type: application/json' -H 'Prefer: respond-async' -H "Idempotency-Key: $current_request_key" --data-binary @- "$base_url/api/agents/$slug/runs")
  current_run_id=$(printf '%s' "$response" | jq -er '.runId')
  run_status=$(curl --connect-timeout 1 --max-time 3 -fsS "$base_url/api/runs/$current_run_id" | jq -er '.run.status')
  attempts=0
  while [ "$run_status" != completed ] && [ "$attempts" -lt 60 ] && before_deadline; do
    sleep 1
    run_status=$(curl --connect-timeout 1 --max-time 3 -fsS "$base_url/api/runs/$current_run_id" | jq -er '.run.status')
    attempts=$((attempts + 1))
  done
  terminal_count=$(postgres_exec psql --dbname="$(database_url upgrade_source)" -X -v ON_ERROR_STOP=1 -Atc "SELECT count(*) FROM run_events WHERE run_id='$current_run_id' AND type IN ('run.completed','run.failed','run.cancelled')")
  lease_count=$(postgres_exec psql --dbname="$(database_url upgrade_source)" -X -v ON_ERROR_STOP=1 -Atc "SELECT count(*) FROM runs WHERE id='$current_run_id' AND (lease_owner IS NOT NULL OR lease_expires_at IS NOT NULL)")
  protocol=$(postgres_exec psql --dbname="$(database_url upgrade_source)" -X -v ON_ERROR_STOP=1 -Atc "SELECT execution_protocol FROM runs WHERE id='$current_run_id'")
  assert_eq "$run_status" completed current_run_status
  assert_eq "$terminal_count" 1 current_terminal_events
  assert_eq "$lease_count" 0 current_leases
  assert_eq "$protocol" 1 current_protocol
  if grep -F "$RUN_PAYLOAD_ENCRYPTION_KEY" "$current_api_log" "$worker_log" >/dev/null 2>&1; then return 1; fi
  if grep -F 'current-smoke-private' "$current_api_log" "$worker_log" >/dev/null 2>&1; then return 1; fi
  if grep -F "$current_request_key" "$current_api_log" "$worker_log" >/dev/null 2>&1; then return 1; fi
}

stop_current_runtime() {
  stop_process "$current_worker_pid"
  current_worker_pid=
  stop_process "$current_api_pid"
  current_api_pid=
}

restore_database() {
  role=$1
  [ "$role" = rollback_target ]
  postgres_exec pg_restore --exit-on-error --no-owner --no-privileges --dbname="$(database_url "$role")" <"$dump_file"
}

assert_rollback_records() {
  schema=$(postgres_exec psql --dbname="$(database_url rollback_target)" -X -v ON_ERROR_STOP=1 -Atc 'SELECT max(version) FROM schema_migrations')
  payload_table=$(postgres_exec psql --dbname="$(database_url rollback_target)" -X -v ON_ERROR_STOP=1 -Atc "SELECT to_regclass('public.run_payloads') IS NULL")
  legacy_count=$(postgres_exec psql --dbname="$(database_url rollback_target)" -X -v ON_ERROR_STOP=1 -Atc "SELECT count(*) FROM runs WHERE id IN ('$completed_run_id','$running_run_id','$cancelling_run_id')")
  workflow_count=$(postgres_exec psql --dbname="$(database_url rollback_target)" -X -v ON_ERROR_STOP=1 -Atc "SELECT count(*) FROM workflows WHERE id='$workflow_id' AND published_version_id='$workflow_version_id'")
  current_count=$(postgres_exec psql --dbname="$(database_url rollback_target)" -X -v ON_ERROR_STOP=1 -Atc "SELECT count(*) FROM runs WHERE id='$current_run_id'")
  assert_eq "$schema" 6 rollback_schema
  assert_eq "$payload_table" t no_run_payloads
  assert_eq "$legacy_count" 3 rollback_records
  assert_eq "$workflow_count" 1 rollback_workflow
  assert_eq "$current_count" 0 rollback_current_run
  curl --connect-timeout 1 --max-time 3 -fsS "$base_url/api/workflows/$workflow_id" | jq -e --arg id "$workflow_id" '.id == $id' >/dev/null
  curl --connect-timeout 1 --max-time 3 -fsS "$base_url/api/runs/$completed_run_id" | jq -e '.run.status == "completed"' >/dev/null
  curl --connect-timeout 1 --max-time 3 -fsS "$base_url/api/runs/$running_run_id" | jq -e '.run.status == "running"' >/dev/null
  curl --connect-timeout 1 --max-time 3 -fsS "$base_url/api/runs/$cancelling_run_id" | jq -e '.run.status == "cancelling"' >/dev/null
  missing_code=$(curl --connect-timeout 1 --max-time 3 -sS -o /dev/null -w '%{http_code}' "$base_url/api/runs/$current_run_id")
  assert_eq "$missing_code" 404 rollback_current_run_http
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

for command in docker curl jq ruby go git tar awk; do
  command -v "$command" >/dev/null 2>&1 || {
    printf '%s is required\n' "$command" >&2
    exit 2
  }
done
docker compose version >/dev/null
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

ruby -rfileutils -e 'FileUtils.mkdir_p(ARGV.fetch(0)); %w[runtime.log summary.log restore-list.log].each { |name| path=File.join(ARGV.fetch(0),name); File.delete(path) if File.file?(path) }' "$artifact_dir"

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

actual_services=$(docker compose -f "$compose_file" config --services)
assert_eq "$actual_services" db compose_services
if [ "${V04_UPGRADE_ROLLBACK_CONTRACT_ONLY:-}" = 1 ]; then
  printf '%s\n' 'v0.4 upgrade/rollback lightweight contract passed'
  exit 0
fi

phase=database_start
docker compose -f "$compose_file" up -d db >/dev/null
compose_started=1
wait_ready "db:$db_port"

phase=upgrade_and_rollback
run_upgrade_rollback
stop_legacy_api

phase=complete
printf 'v0.4.0 peeled commit: %s\n' "$old_commit"
printf 'pre-upgrade dump sha256: %s\n' "$dump_sha"
printf '%s\n' 'v0.4 migration 6 upgrade, current smoke, and second-database rollback passed'
