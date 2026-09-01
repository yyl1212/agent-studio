#!/bin/sh
set -eu

COMPOSE_PROJECT_NAME="agent_studio_rc_capacity_$$"
export COMPOSE_PROJECT_NAME

repo_root=$(CDPATH= cd -- "$(dirname "$0")/.." && pwd)
artifact_dir_value=${RC_CAPACITY_ARTIFACT_DIR:-artifacts/rc-capacity}
case "$artifact_dir_value" in
  /*) artifact_dir=$artifact_dir_value ;;
  *) artifact_dir="$repo_root/$artifact_dir_value" ;;
esac

submitted=0
completed=0
non_completed=0
duplicate_terminal_events=0
remaining_leases=0
remaining_queue_depth=0
elapsed_ms=0
throughput_per_second=0
queue_wait_p95_ms=0
started_workload=0
compose_started=0
summary_persisted=0
cleanup_in_progress=0
run_ids_sql=

write_summary() {
  jq -n -c --argjson submitted "$submitted" --argjson completed "$completed" --argjson nonCompleted "$non_completed" --argjson duplicateTerminalEvents "$duplicate_terminal_events" --argjson remainingLeases "$remaining_leases" --argjson remainingQueueDepth "$remaining_queue_depth" --argjson elapsedMs "$elapsed_ms" --argjson throughputPerSecond "$throughput_per_second" --argjson queueWaitP95Ms "$queue_wait_p95_ms" '{submitted:$submitted,completed:$completed,nonCompleted:$nonCompleted,duplicateTerminalEvents:$duplicateTerminalEvents,remainingLeases:$remainingLeases,remainingQueueDepth:$remainingQueueDepth,elapsedMs:$elapsedMs,throughputPerSecond:$throughputPerSecond,queueWaitP95Ms:$queueWaitP95Ms}' > "$summary_file"
}

persist_summary() {
  ruby -rfileutils -e 'FileUtils.mkdir_p(File.dirname(ARGV.fetch(1))); FileUtils.cp(ARGV.fetch(0), ARGV.fetch(1))' "$summary_file" "$artifact_dir/summary.json"
}

now_epoch_ms() {
  ruby -e 'puts (Time.now.to_f * 1000).to_i'
}

remaining_budget_ms() {
  budget_deadline_ms=$1
  budget_now_ms=$(now_epoch_ms)
  budget_remaining_ms=$((budget_deadline_ms - budget_now_ms))
  [ "$budget_remaining_ms" -gt 0 ] || return 124
  printf '%s\n' "$budget_remaining_ms"
}

remaining_budget_seconds() {
  budget_remaining_ms=$(remaining_budget_ms "$1") || return $?
  budget_remaining_seconds=$((budget_remaining_ms / 1000))
  [ "$budget_remaining_seconds" -ge 1 ] || budget_remaining_seconds=1
  printf '%s\n' "$budget_remaining_seconds"
}

before_epoch_deadline() {
  remaining_budget_ms "$1" >/dev/null
}

curl_with_deadline() {
  curl_deadline_ms=$1
  shift
  curl_max_time=$(remaining_budget_seconds "$curl_deadline_ms") || return $?
  curl --connect-timeout 2 --max-time "$curl_max_time" "$@" || return $?
  before_epoch_deadline "$curl_deadline_ms" || return 124
}

db_scalar_with_timeout() {
  db_timeout_ms=$1
  shift
  docker compose -f "$compose_file" exec -T \
    -e "PGOPTIONS=-c statement_timeout=${db_timeout_ms}ms -c lock_timeout=${db_timeout_ms}ms" \
    db psql -X -v ON_ERROR_STOP=1 -U agent -d agent_studio -Atc "$1"
}

db_scalar() {
  db_remaining_ms=$(remaining_budget_ms "$deadline_epoch_ms") || return $?
  db_result=$(db_scalar_with_timeout "$db_remaining_ms" "$1") || return $?
  before_deadline || return 124
  printf '%s\n' "$db_result"
}

db_scalar_best_effort() {
  docker compose -f "$compose_file" exec -T \
    -e "PGOPTIONS=-c statement_timeout=1000ms -c lock_timeout=1000ms" \
    db psql -X -v ON_ERROR_STOP=1 -U agent -d agent_studio -Atc "$1"
}

refresh_metrics_strict() {
  [ -n "$run_ids_sql" ] || return 2
  value=$(db_scalar "SELECT count(*) FROM runs WHERE id = ANY(ARRAY[$run_ids_sql]::uuid[]) AND status='completed'") || return $?
  completed=$value
  value=$(db_scalar "SELECT count(*) FROM runs WHERE id = ANY(ARRAY[$run_ids_sql]::uuid[]) AND status <> 'completed'") || return $?
  non_completed=$value
  value=$(db_scalar "SELECT count(*) FROM (SELECT r.id FROM runs r LEFT JOIN run_events e ON e.run_id=r.id AND e.type IN ('run.completed','run.failed','run.cancelled') WHERE r.id = ANY(ARRAY[$run_ids_sql]::uuid[]) GROUP BY r.id HAVING count(e.*) <> 1) invalid") || return $?
  duplicate_terminal_events=$value
  value=$(db_scalar "SELECT count(*) FROM runs WHERE id = ANY(ARRAY[$run_ids_sql]::uuid[]) AND lease_owner IS NOT NULL") || return $?
  remaining_leases=$value
  value=$(db_scalar "SELECT count(*) FROM runs WHERE status='queued' AND execution_protocol=1") || return $?
  remaining_queue_depth=$value
  value=$(db_scalar "SELECT COALESCE(percentile_cont(0.95) WITHIN GROUP (ORDER BY EXTRACT(EPOCH FROM (started.timestamp-queued.timestamp))*1000),0) FROM run_events queued JOIN run_events started USING(run_id) WHERE queued.type='run.queued' AND started.type='run.started' AND queued.run_id = ANY(ARRAY[$run_ids_sql]::uuid[])") || return $?
  queue_wait_p95_ms=$value
}

refresh_metrics_best_effort() {
  [ "$compose_started" -eq 1 ] || return 0
  [ -n "$run_ids_sql" ] || return 0
  best_effort_value=$(db_scalar_best_effort "SELECT count(*) FROM runs WHERE id = ANY(ARRAY[$run_ids_sql]::uuid[]) AND status='completed'" 2>/dev/null) && completed=$best_effort_value
  best_effort_value=$(db_scalar_best_effort "SELECT count(*) FROM runs WHERE id = ANY(ARRAY[$run_ids_sql]::uuid[]) AND status <> 'completed'" 2>/dev/null) && non_completed=$best_effort_value
  best_effort_value=$(db_scalar_best_effort "SELECT count(*) FROM (SELECT r.id FROM runs r LEFT JOIN run_events e ON e.run_id=r.id AND e.type IN ('run.completed','run.failed','run.cancelled') WHERE r.id = ANY(ARRAY[$run_ids_sql]::uuid[]) GROUP BY r.id HAVING count(e.*) <> 1) invalid" 2>/dev/null) && duplicate_terminal_events=$best_effort_value
  best_effort_value=$(db_scalar_best_effort "SELECT count(*) FROM runs WHERE id = ANY(ARRAY[$run_ids_sql]::uuid[]) AND lease_owner IS NOT NULL" 2>/dev/null) && remaining_leases=$best_effort_value
  best_effort_value=$(db_scalar_best_effort "SELECT count(*) FROM runs WHERE status='queued' AND execution_protocol=1" 2>/dev/null) && remaining_queue_depth=$best_effort_value
  best_effort_value=$(db_scalar_best_effort "SELECT COALESCE(percentile_cont(0.95) WITHIN GROUP (ORDER BY EXTRACT(EPOCH FROM (started.timestamp-queued.timestamp))*1000),0) FROM run_events queued JOIN run_events started USING(run_id) WHERE queued.type='run.queued' AND started.type='run.started' AND queued.run_id = ANY(ARRAY[$run_ids_sql]::uuid[])" 2>/dev/null) && queue_wait_p95_ms=$best_effort_value
  return 0
}

validate_elapsed() {
  [ "$elapsed_ms" -le "$((RC_CAPACITY_DEADLINE_SECONDS * 1000))" ]
}

capture_failure_logs() {
  raw_log="$run_root/compose.log"
  docker compose -f "$compose_file" logs --no-color api worker >"$raw_log" 2>&1 || true
  if ruby -rfileutils -e 'data=File.binread(ARGV.fetch(0)); key=ENV.fetch("RUN_PAYLOAD_ENCRYPTION_KEY", ""); forbidden=(!key.empty? && data.include?(key)) || data.match?(/authorization/i) || data.match?(/ciphertext/i) || data.match?(/["\x27](?:input|output)["\x27]\s*[:=]/i); FileUtils.mkdir_p(ARGV.fetch(1)); if forbidden; File.write(File.join(ARGV.fetch(1), "redaction.log"), "failure log withheld after sensitive-data scan\n"); exit 1; end; File.binwrite(File.join(ARGV.fetch(1), "compose.log"), data)' "$raw_log" "$artifact_dir"; then
    ruby -e 'STDERR.write(File.readlines(ARGV.fetch(0)).last(300).join)' "$artifact_dir/compose.log" || true
  else
    printf '%s\n' 'capacity failure log withheld after sensitive-data scan' >&2
  fi
}

cleanup() {
  status=$1
  if [ "$cleanup_in_progress" -eq 1 ]; then
    trap - EXIT HUP INT TERM
    exit "$status"
  fi
  cleanup_in_progress=1
  trap - EXIT HUP INT TERM
  set +e
  if [ "$status" -ne 0 ]; then
    refresh_metrics_best_effort
    if [ "$started_workload" -eq 1 ]; then
      now_ms=$(now_epoch_ms)
      elapsed_ms=$((now_ms - start_epoch_ms))
      [ "$elapsed_ms" -ge 0 ] || elapsed_ms=0
      if [ "$elapsed_ms" -gt 0 ]; then
        throughput_per_second=$(ruby -e 'puts format("%.6f", ARGV.fetch(0).to_f / (ARGV.fetch(1).to_f / 1000.0))' "$completed" "$elapsed_ms")
      fi
    fi
    if [ "$compose_started" -eq 1 ]; then
      capture_failure_logs
    fi
  fi
  if [ "$summary_persisted" -eq 0 ] && [ -n "${summary_file:-}" ] && command -v jq >/dev/null 2>&1; then
    write_summary
    persist_summary
  fi
  if [ "$compose_started" -eq 1 ]; then
    docker compose -f "$compose_file" down --remove-orphans >/dev/null 2>&1 || true
  fi
  rm -rf "$run_root"
  exit "$status"
}

on_hup() {
  cleanup 129
}

on_int() {
  cleanup 130
}

on_term() {
  cleanup 143
}

validate_summary() {
  jq -e 'keys == ["completed","duplicateTerminalEvents","elapsedMs","nonCompleted","queueWaitP95Ms","remainingLeases","remainingQueueDepth","submitted","throughputPerSecond"]' "$summary_file" >/dev/null
}

run_root=$(mktemp -d "${TMPDIR:-/tmp}/agent-studio-rc-capacity.XXXXXX")
trap 'status=$?; cleanup "$status"' EXIT
trap on_hup HUP
trap on_int INT
trap on_term TERM
compose_file="$run_root/compose.yaml"
summary_file="$run_root/summary.json"
run_ids_file="$run_root/run-ids"
request_keys_file="$run_root/request-keys"

for command in docker curl jq ruby go; do
  command -v "$command" >/dev/null || {
    printf '%s\n' "$command is required" >&2
    exit 2
  }
done
docker compose version >/dev/null

[ -n "${RUN_PAYLOAD_ENCRYPTION_KEY:-}" ] || {
  printf '%s\n' 'RUN_PAYLOAD_ENCRYPTION_KEY is required' >&2
  exit 2
}

RUN_COUNT=${RUN_COUNT:-500}
RC_CAPACITY_DEADLINE_SECONDS=${RC_CAPACITY_DEADLINE_SECONDS:-570}
[ "$RUN_COUNT" = "500" ] || exit 1
validate_deadline() {
  case "$RC_CAPACITY_DEADLINE_SECONDS" in
    ''|*[!0-9]*) return 1 ;;
  esac
  if [ "$RC_CAPACITY_DEADLINE_SECONDS" -lt 60 ]; then return 1; fi
  if [ "$RC_CAPACITY_DEADLINE_SECONDS" -gt 570 ]; then return 1; fi
}
validate_deadline "$RC_CAPACITY_DEADLINE_SECONDS"

api_port=${RC_CAPACITY_PORT:-$((20000 + ($$ % 10000) * 2))}
db_port=${RC_CAPACITY_DB_PORT:-$((api_port + 1))}
case "$api_port:$db_port" in
  *[!0-9:]*) printf '%s\n' 'capacity ports must be integers' >&2; exit 2 ;;
esac
[ "$api_port" -ge 1 ] && [ "$api_port" -le 65535 ] || exit 2
[ "$db_port" -ge 1 ] && [ "$db_port" -le 65535 ] || exit 2
base_url="http://127.0.0.1:$api_port"
test_image="agent-studio:rc-capacity-e2e"
export api_port db_port test_image RUN_PAYLOAD_ENCRYPTION_KEY

ruby -rfileutils -e 'FileUtils.mkdir_p(ARGV.fetch(0)); %w[summary.json compose.log redaction.log].each { |name| path=File.join(ARGV.fetch(0), name); File.delete(path) if File.file?(path) }' "$artifact_dir"

cat >"$compose_file" <<'YAML'
services:
  db:
    image: postgres:18
    environment:
      POSTGRES_DB: agent_studio
      POSTGRES_USER: agent
      POSTGRES_PASSWORD: agent
    ports:
      - "127.0.0.1:$db_port:5432"
    tmpfs:
      - /var/lib/postgresql
    healthcheck:
      test: ["CMD-SHELL", "pg_isready -U agent -d agent_studio"]
      interval: 1s
      timeout: 3s
      retries: 30
  api:
    image: "$test_image"
    entrypoint: ["/app/agent-studio-api"]
    environment:
      DATABASE_URL: postgres://agent:agent@db:5432/agent_studio?sslmode=disable
      RUN_PAYLOAD_ENCRYPTION_KEY:
      MODEL_PROVIDER: mock
    depends_on:
      db:
        condition: service_healthy
    ports:
      - "127.0.0.1:$api_port:8080"
    healthcheck:
      test: ["CMD", "wget", "-qO-", "http://127.0.0.1:8080/readyz"]
      interval: 1s
      timeout: 3s
      retries: 30
  worker:
    image: "$test_image"
    entrypoint: ["/app/agent-studio-worker"]
    environment:
      DATABASE_URL: postgres://agent:agent@db:5432/agent_studio?sslmode=disable
      RUN_PAYLOAD_ENCRYPTION_KEY:
      MODEL_PROVIDER: mock
      WORKER_MAX_ACTIVE_RUNS: "4"
      WORKER_QUEUE_SAMPLE_INTERVAL: 1s
      OTEL_SERVICE_NAME: agent-studio-worker-capacity
    depends_on:
      api:
        condition: service_healthy
    healthcheck:
      test: ["CMD-SHELL", "kill -0 1"]
      interval: 1s
      timeout: 3s
      retries: 30
YAML

actual_services=$(docker compose -f "$compose_file" config --services | sort | tr '\n' ' ')
[ "$actual_services" = "api db worker " ] || exit 1
if [ "${RC_CAPACITY_CONTRACT_ONLY:-}" = "1" ]; then exit 0; fi

docker build -t "$test_image" "$repo_root"
compose_started=1
docker compose -f "$compose_file" up -d db api worker

wait_for_api() {
  while before_epoch_deadline "$readiness_deadline_ms"; do
    curl_with_deadline "$readiness_deadline_ms" -fsS "$base_url/readyz" >/dev/null 2>&1 && return 0
    sleep 1
  done
  return 124
}
readiness_deadline_ms=$(( $(now_epoch_ms) + 60000 ))
wait_for_api

setup_deadline_ms=$(( $(now_epoch_ms) + 60000 ))
slug="rc-capacity-$$"
created=$(jq -nc --arg slug "$slug" '{name:"RC capacity",slug:$slug,description:"rc capacity baseline"}' |
  curl_with_deadline "$setup_deadline_ms" -fsS -H 'Content-Type: application/json' --data-binary @- "$base_url/api/workflows")
workflow_id=$(printf '%s' "$created" | jq -er '.id')
revision=$(printf '%s' "$created" | jq -er '.draftRevision')
graph=$(jq -nc '{schemaVersion:1,nodes:[
  {id:"start",type:"start",typeVersion:"1",position:{x:0,y:0},config:{fields:[{key:"topic",label:"Topic",type:"text",required:true}]}},
  {id:"prompt-template",type:"template",typeVersion:"1",position:{x:240,y:0},config:{template:"Answer briefly: {{topic}}"}},
  {id:"llm-mock",type:"llm",typeVersion:"1",position:{x:480,y:0},config:{model:"mock",maxTokens:32}},
  {id:"end",type:"end",typeVersion:"1",position:{x:720,y:0},config:{}}
],edges:[
  {id:"start-template",source:"start",sourcePort:"topic",target:"prompt-template",targetPort:"topic"},
  {id:"template-llm",source:"prompt-template",sourcePort:"text",target:"llm-mock",targetPort:"prompt"},
  {id:"llm-end",source:"llm-mock",sourcePort:"text",target:"end",targetPort:"result"}
]}')
saved=$(jq -nc --argjson revision "$revision" --argjson graph "$graph" '{draftRevision:$revision,graph:$graph}' |
  curl_with_deadline "$setup_deadline_ms" -fsS -X PUT -H 'Content-Type: application/json' --data-binary @- "$base_url/api/workflows/$workflow_id")
revision=$(printf '%s' "$saved" | jq -er '.draftRevision')
published=$(jq -nc --argjson revision "$revision" '{draftRevision:$revision}' |
  curl_with_deadline "$setup_deadline_ms" -fsS -H 'Content-Type: application/json' --data-binary @- "$base_url/api/workflows/$workflow_id/publish")
workflow_version_id=$(printf '%s' "$published" | jq -er '.id')

: >"$run_ids_file"
: >"$request_keys_file"
start_epoch_ms=$(now_epoch_ms)
deadline_epoch_ms=$((start_epoch_ms + RC_CAPACITY_DEADLINE_SECONDS * 1000))
started_workload=1

before_deadline() {
  before_epoch_deadline "$deadline_epoch_ms"
}

index=1
while [ "$index" -le "$RUN_COUNT" ]; do
  before_deadline || exit 1
  request_key=$(ruby -rsecurerandom -e 'puts SecureRandom.uuid')
  printf '%s\n' "$request_key" >>"$request_keys_file"
  response=$(jq -nc --arg version "$workflow_version_id" --arg topic "capacity-$index" '{workflowVersionId:$version,input:{topic:$topic}}' |
    curl_with_deadline "$deadline_epoch_ms" -fsS -H 'Content-Type: application/json' -H 'Prefer: respond-async' -H "Idempotency-Key: $request_key" --data-binary @- "$base_url/api/agents/$slug/runs")
  before_deadline
  run_id=$(printf '%s' "$response" | jq -er '.runId')
  ruby -e 'exit(ARGV.fetch(0).match?(/\A[0-9a-f]{8}-[0-9a-f]{4}-[1-5][0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}\z/i) ? 0 : 1)' "$run_id"
  printf '%s\n' "$run_id" >>"$run_ids_file"
  submitted=$((submitted + 1))
  index=$((index + 1))
done

ruby -e 'lines=File.readlines(ARGV.fetch(0), chomp:true); exit(lines.length == 500 && lines.uniq.length == 500 ? 0 : 1)' "$request_keys_file"
ruby -e 'lines=File.readlines(ARGV.fetch(0), chomp:true); exit(lines.length == 500 && lines.uniq.length == 500 ? 0 : 1)' "$run_ids_file"
run_ids_sql=$(ruby -e 'puts File.readlines(ARGV.fetch(0), chomp:true).map { |id| 39.chr + id + 39.chr }.join(",")' "$run_ids_file")

wait_for_completion() {
  while before_deadline; do
    completed=$(db_scalar "SELECT count(*) FROM runs WHERE id = ANY(ARRAY[$run_ids_sql]::uuid[]) AND status='completed'")
    if [ "$completed" -eq "$RUN_COUNT" ]; then
      return 0
    fi
    sleep 1
  done
  return 1
}
wait_for_completion
refresh_metrics_strict

assert_equal() {
  actual=$1
  expected=$2
  label=$3
  [ "$actual" = "$expected" ] || {
    printf '%s\n' "$label assertion failed: got $actual, want $expected" >&2
    return 1
  }
}

total_runs=$(db_scalar "SELECT count(*) FROM runs WHERE id = ANY(ARRAY[$run_ids_sql]::uuid[])")
failed_states=$(db_scalar "SELECT count(*) FROM runs WHERE id = ANY(ARRAY[$run_ids_sql]::uuid[]) AND status IN ('failed','cancelled','recovery_required','queued','running','cancelling')")
sequence_projection_errors=$(db_scalar "SELECT count(*) FROM (SELECT r.id FROM runs r LEFT JOIN run_events e ON e.run_id=r.id WHERE r.id = ANY(ARRAY[$run_ids_sql]::uuid[]) GROUP BY r.id,r.status HAVING COALESCE(min(e.sequence),0) <> 1 OR COALESCE(max(e.sequence),0) <> count(e.*) OR count(e.*) FILTER (WHERE e.type = 'run.' || r.status) <> 1 OR max(e.sequence) FILTER (WHERE e.type IN ('run.completed','run.failed','run.cancelled')) <> max(e.sequence)) invalid")
assert_equal "$submitted" 500 submitted
assert_equal "$total_runs" 500 run-count
assert_equal "$completed" 500 completed
assert_equal "$non_completed" 0 non-completed
assert_equal "$failed_states" 0 non-success-status
assert_equal "$duplicate_terminal_events" 0 terminal-event
assert_equal "$sequence_projection_errors" 0 sequence-projection
assert_equal "$remaining_leases" 0 remaining-leases
assert_equal "$remaining_queue_depth" 0 remaining-queue-depth

api_completed=0
while IFS= read -r run_id; do
  api_status=$(curl_with_deadline "$deadline_epoch_ms" -fsS "$base_url/api/runs/$run_id" | jq -er '.run.status')
  before_deadline
  [ "$api_status" = completed ] || exit 1
  api_completed=$((api_completed + 1))
done <"$run_ids_file"
assert_equal "$api_completed" 500 api-completed

curl_with_deadline "$deadline_epoch_ms" -fsS "$base_url/readyz" >/dev/null
api_container=$(docker compose -f "$compose_file" ps -q api)
worker_container=$(docker compose -f "$compose_file" ps -q worker)
api_health=$(docker inspect --format '{{.State.Health.Status}}' "$api_container")
worker_health=$(docker inspect --format '{{.State.Health.Status}}' "$worker_container")
assert_equal "$api_health" healthy api-health
assert_equal "$worker_health" healthy worker-health
before_deadline

elapsed_ms=$(( $(now_epoch_ms) - start_epoch_ms ))
[ "$elapsed_ms" -ge 0 ] || exit 1
validate_elapsed
throughput_per_second=$(ruby -e 'elapsed=[ARGV.fetch(1).to_i,1].max; puts format("%.6f", ARGV.fetch(0).to_f / (elapsed.to_f / 1000.0))' "$completed" "$elapsed_ms")

write_summary
persist_summary
validate_summary
summary_persisted=1
cat "$summary_file"
