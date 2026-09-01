#!/bin/sh
set -eu

if [ -z "${RUN_PAYLOAD_ENCRYPTION_KEY:-}" ]; then
  printf '%s\n' 'RUN_PAYLOAD_ENCRYPTION_KEY is required' >&2
  exit 2
fi
for command in docker curl jq ruby go; do
  command -v "$command" >/dev/null || {
    printf '%s\n' "$command is required" >&2
    exit 2
  }
done

repo_root=$(CDPATH= cd -- "$(dirname "$0")/.." && pwd)
run_root=$(mktemp -d "${TMPDIR:-/tmp}/agent-studio-durable-e2e.XXXXXX")
compose_file="$run_root/compose.yaml"
artifact_dir=${DURABLE_E2E_ARTIFACT_DIR:-$repo_root/artifacts/durable-runs}
api_port=${DURABLE_E2E_PORT:-18081}
db_port=${DURABLE_E2E_DB_PORT:-15433}
base_url="http://127.0.0.1:$api_port"
WAIT_ATTEMPTS=${WAIT_ATTEMPTS:-90}
COMPOSE_PROJECT_NAME="agent_studio_durable_e2e_$$"
export COMPOSE_PROJECT_NAME RUN_PAYLOAD_ENCRYPTION_KEY

cat >"$compose_file" <<EOF
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
    image: agent-studio:durable-e2e
    build:
      context: "$repo_root"
      args:
        GO_BUILD_TAGS: durablee2e
    entrypoint: ["/app/agent-studio-api"]
    environment: &runtime
      DATABASE_URL: postgres://agent:agent@db:5432/agent_studio?sslmode=disable
      RUN_PAYLOAD_ENCRYPTION_KEY:
      MODEL_PROVIDER: mock
      WORKER_LEASE_DURATION: 15s
      WORKER_HEARTBEAT_INTERVAL: 5s
      WORKER_CLAIM_INTERVAL: 100ms
      WORKER_SHUTDOWN_TIMEOUT: 1s
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
    image: agent-studio:durable-e2e
    build:
      context: "$repo_root"
      args:
        GO_BUILD_TAGS: durablee2e
    entrypoint: ["/app/agent-studio-worker"]
    environment:
      <<: *runtime
      WORKER_MAX_ACTIVE_RUNS: "1"
      OTEL_SERVICE_NAME: agent-studio-worker-e2e
    depends_on:
      db:
        condition: service_healthy
EOF

compose() {
  docker compose -f "$compose_file" "$@"
}

cleanup() {
  status=$?
  trap - EXIT HUP INT TERM
  if [ "$status" -ne 0 ]; then
    mkdir -p "$artifact_dir"
    compose logs --no-color api worker >"$artifact_dir/compose.log" 2>&1 || true
    tail -n 300 "$artifact_dir/compose.log" >&2 || true
  fi
  compose down --remove-orphans >/dev/null 2>&1 || true
  rm -rf "$run_root"
  exit "$status"
}
trap cleanup EXIT HUP INT TERM

wait_for_api() {
  attempts=0
  while [ "$attempts" -lt "$WAIT_ATTEMPTS" ]; do
    curl -fsS "$base_url/readyz" >/dev/null 2>&1 && return 0
    attempts=$((attempts + 1))
    sleep 1
  done
  printf '%s\n' 'API readiness timed out' >&2
  return 1
}

wait_for_status() {
  run_id=$1
  expected=$2
  attempts=0
  while [ "$attempts" -lt "$WAIT_ATTEMPTS" ]; do
    current=$(curl -fsS "$base_url/api/runs/$run_id" 2>/dev/null | jq -r '.run.status' 2>/dev/null || true)
    [ "$current" = "$expected" ] && return 0
    attempts=$((attempts + 1))
    sleep 1
  done
  printf '%s\n' "run $run_id did not reach $expected" >&2
  return 1
}

wait_for_db_status() {
  run_id=$1
  expected=$2
  attempts=0
  while [ "$attempts" -lt "$WAIT_ATTEMPTS" ]; do
    current=$(compose exec -T db psql -U agent -d agent_studio -Atc "SELECT status FROM runs WHERE id='$run_id'" 2>/dev/null || true)
    [ "$current" = "$expected" ] && return 0
    attempts=$((attempts + 1))
    sleep 1
  done
  printf '%s\n' "database run $run_id did not reach $expected" >&2
  return 1
}

wait_for_node() {
  run_id=$1
  node_id=$2
  expected=$3
  attempts=0
  while [ "$attempts" -lt "$WAIT_ATTEMPTS" ]; do
    if curl -fsS "$base_url/api/runs/$run_id" 2>/dev/null | jq -e --arg node "$node_id" --arg status "$expected" \
      '.nodeRuns | any(.nodeId == $node and .status == $status)' >/dev/null 2>&1; then
      return 0
    fi
    attempts=$((attempts + 1))
    sleep 1
  done
  printf '%s\n' "node $node_id in run $run_id did not reach $expected" >&2
  return 1
}

create_workflow() {
  node_type=$1
  delay_ms=$2
  suffix=$3
  slug="durable-$suffix-$$"
  created=$(jq -nc --arg name "Durable $suffix" --arg slug "$slug" '{name:$name,slug:$slug,description:"durable e2e"}' |
    curl -fsS -H 'Content-Type: application/json' --data-binary @- "$base_url/api/workflows")
  workflow_id=$(printf '%s' "$created" | jq -r '.id')
  revision=$(printf '%s' "$created" | jq -r '.draftRevision')
  graph=$(jq -nc --arg type "$node_type" --argjson delay "$delay_ms" '{schemaVersion:1,nodes:[
    {id:"start",type:"start",typeVersion:"1",position:{x:0,y:0},config:{fields:[{key:"value",label:"Value",type:"text",required:true}]}},
    {id:"work",type:$type,typeVersion:"1",position:{x:300,y:0},config:{delayMs:$delay}},
    {id:"end",type:"end",typeVersion:"1",position:{x:600,y:0},config:{}}
  ],edges:[
    {id:"start-work",source:"start",sourcePort:"value",target:"work",targetPort:"value"},
    {id:"work-end",source:"work",sourcePort:"result",target:"end",targetPort:"result"}
  ]}')
  saved=$(jq -nc --argjson revision "$revision" --argjson graph "$graph" '{draftRevision:$revision,graph:$graph}' |
    curl -fsS -X PUT -H 'Content-Type: application/json' --data-binary @- "$base_url/api/workflows/$workflow_id")
  revision=$(printf '%s' "$saved" | jq -r '.draftRevision')
  version=$(jq -nc --argjson revision "$revision" '{draftRevision:$revision}' |
    curl -fsS -H 'Content-Type: application/json' --data-binary @- "$base_url/api/workflows/$workflow_id/publish")
  workflow_version_id=$(printf '%s' "$version" | jq -r '.id')
}

create_two_step_workflow() {
  suffix=$1
  slug="durable-$suffix-$$"
  created=$(jq -nc --arg name "Durable $suffix" --arg slug "$slug" '{name:$name,slug:$slug,description:"durable e2e"}' |
    curl -fsS -H 'Content-Type: application/json' --data-binary @- "$base_url/api/workflows")
  workflow_id=$(printf '%s' "$created" | jq -r '.id')
  revision=$(printf '%s' "$created" | jq -r '.draftRevision')
  graph='{"schemaVersion":1,"nodes":[{"id":"start","type":"start","typeVersion":"1","position":{"x":0,"y":0},"config":{"fields":[{"key":"value","label":"Value","type":"text","required":true}]}},{"id":"first","type":"e2e.slow-pure","typeVersion":"1","position":{"x":200,"y":0},"config":{"delayMs":250}},{"id":"second","type":"e2e.slow-pure","typeVersion":"1","position":{"x":400,"y":0},"config":{"delayMs":4000}},{"id":"end","type":"end","typeVersion":"1","position":{"x":600,"y":0},"config":{}}],"edges":[{"id":"start-first","source":"start","sourcePort":"value","target":"first","targetPort":"value"},{"id":"first-second","source":"first","sourcePort":"result","target":"second","targetPort":"value"},{"id":"second-end","source":"second","sourcePort":"result","target":"end","targetPort":"result"}]}'
  saved=$(jq -nc --argjson revision "$revision" --argjson graph "$graph" '{draftRevision:$revision,graph:$graph}' |
    curl -fsS -X PUT -H 'Content-Type: application/json' --data-binary @- "$base_url/api/workflows/$workflow_id")
  revision=$(printf '%s' "$saved" | jq -r '.draftRevision')
  version=$(jq -nc --argjson revision "$revision" '{draftRevision:$revision}' |
    curl -fsS -H 'Content-Type: application/json' --data-binary @- "$base_url/api/workflows/$workflow_id/publish")
  workflow_version_id=$(printf '%s' "$version" | jq -r '.id')
}

submit_async() {
  request_key=$(ruby -rsecurerandom -e 'puts SecureRandom.uuid')
  response=$(jq -nc --arg version "$workflow_version_id" '{workflowVersionId:$version,input:{value:"durable"}}' |
    curl -fsS -H 'Content-Type: application/json' -H 'Prefer: respond-async' -H "Idempotency-Key: $request_key" --data-binary @- "$base_url/api/agents/$slug/runs")
  printf '%s' "$response" | jq -r '.runId'
}

start_worker() { compose up -d worker >/dev/null; }
stop_worker() { compose stop worker >/dev/null 2>&1 || true; }
kill_worker() { compose kill worker >/dev/null; }

scenario_cancel_queued() {
  stop_worker
  create_workflow e2e.slow-pure 1000 cancel-queued
  run_id=$(submit_async)
  curl -fsS -X POST "$base_url/api/runs/$run_id/cancel" >/dev/null
  start_worker
  wait_for_status "$run_id" cancelled
}

scenario_api_kill_worker_completes() {
  create_workflow e2e.slow-pure 1500 api-kill
  run_id=$(submit_async)
  wait_for_node "$run_id" work running
  compose kill api >/dev/null
  wait_for_db_status "$run_id" completed
  compose up -d api >/dev/null
  wait_for_api
  wait_for_status "$run_id" completed
}

scenario_worker_kill_after_completed() {
  create_two_step_workflow completed-prefix
  run_id=$(submit_async)
  wait_for_node "$run_id" second running
  kill_worker
  start_worker
  wait_for_status "$run_id" completed
  count=$(curl -fsS "$base_url/api/runs/$run_id" | jq '[.nodeRuns[] | select(.nodeId == "first")] | length')
  [ "$count" -eq 1 ] || { printf '%s\n' 'completed node ran more than once' >&2; return 1; }
}

scenario_pure_takeover() {
  create_workflow e2e.slow-pure 4000 pure-takeover
  run_id=$(submit_async)
  wait_for_node "$run_id" work running
  kill_worker
  start_worker
  wait_for_status "$run_id" completed
  max_attempt=$(curl -fsS "$base_url/api/runs/$run_id" | jq '[.nodeRuns[] | select(.nodeId == "work") | .attempt] | max')
  [ "$max_attempt" -eq 2 ] || { printf '%s\n' "pure node max attempt is $max_attempt" >&2; return 1; }
}

scenario_side_effect_recovery() {
  create_workflow e2e.slow-side-effect 4000 side-effect
  run_id=$(submit_async)
  wait_for_node "$run_id" work running
  kill_worker
  start_worker
  wait_for_status "$run_id" recovery_required
  recovery=$(curl -fsS "$base_url/api/runs/$run_id/recovery")
  sequence=$(printf '%s' "$recovery" | jq '.sequence')
  attempt=$(printf '%s' "$recovery" | jq '.nodes[] | select(.nodeId == "work") | .nodeAttempt')
  jq -nc --argjson sequence "$sequence" --argjson attempt "$attempt" '{expectedSequence:$sequence,nodeAttempt:$attempt}' |
    curl -fsS -H 'Content-Type: application/json' --data-binary @- "$base_url/api/runs/$run_id/recovery/nodes/work/retry" >/dev/null
  wait_for_status "$run_id" completed
}

scenario_stale_token_rejected() {
  stop_worker
  TEST_DATABASE_URL="postgres://agent:agent@127.0.0.1:$db_port/agent_studio?sslmode=disable" CGO_ENABLED=0 \
    go test ./apps/api/internal/store/postgres -run '^TestDurableRunLeaseFencesAllWrites$' -count=1
  start_worker
}

scenario_cancel_running() {
  create_workflow e2e.slow-pure 10000 cancel-running
  run_id=$(submit_async)
  wait_for_node "$run_id" work running
  curl -fsS -X POST "$base_url/api/runs/$run_id/cancel" >/dev/null
  wait_for_status "$run_id" cancelled
}

scenario_cancel_recovery() {
  create_workflow e2e.slow-side-effect 4000 cancel-recovery
  run_id=$(submit_async)
  wait_for_node "$run_id" work running
  kill_worker
  start_worker
  wait_for_status "$run_id" recovery_required
  sequence=$(curl -fsS "$base_url/api/runs/$run_id/recovery" | jq '.sequence')
  jq -nc --argjson sequence "$sequence" '{expectedSequence:$sequence}' |
    curl -fsS -H 'Content-Type: application/json' --data-binary @- "$base_url/api/runs/$run_id/recovery/terminate" >/dev/null
  wait_for_status "$run_id" cancelled
}

scenario_ndjson_disconnect() {
  create_workflow e2e.slow-pure 2500 ndjson-disconnect
  output="$run_root/ndjson.out"
  set +e
  jq -nc --arg version "$workflow_version_id" '{workflowVersionId:$version,input:{value:"disconnect"}}' |
    curl -sS --max-time 1 -H 'Content-Type: application/json' --data-binary @- "$base_url/api/agents/$slug/runs" >"$output"
  set -e
  run_id=$(ruby -rjson -e 'ARGF.each_line { |line| begin; id=JSON.parse(line)["runId"]; if id; puts id; exit; end; rescue JSON::ParserError; end }' "$output")
  [ -n "$run_id" ] || { printf '%s\n' 'NDJSON disconnect did not expose a run ID' >&2; return 1; }
  wait_for_status "$run_id" completed
}

scenario_backup_same_key() {
  stop_worker
  compose stop api >/dev/null
  BACKUP_E2E=1 TEST_DATABASE_URL="postgres://agent:agent@127.0.0.1:$db_port/agent_studio?sslmode=disable" CGO_ENABLED=0 \
    go test ./internal/backup -run '^TestBackupRestoreE2E$' -count=1
}

scenario_backup_wrong_key() {
  stop_worker
  create_workflow e2e.slow-pure 1000 wrong-key
  run_id=$(submit_async)
  wrong_key='ZmVkY2JhOTg3NjU0MzIxMGZlZGNiYTk4NzY1NDMyMTA='
  RUN_PAYLOAD_ENCRYPTION_KEY="$wrong_key" compose up -d --force-recreate worker >/dev/null
  wait_for_status "$run_id" recovery_required
  reason=$(curl -fsS "$base_url/api/runs/$run_id/recovery" | jq -r '.reason')
  [ "$reason" = payload_unavailable ] || { printf '%s\n' "wrong key recovery reason is $reason" >&2; return 1; }
  stop_worker
  CGO_ENABLED=0 go test ./apps/api/internal/worker -run '^TestRehydratorRejectsHistoryAndPayloadDamage/wrong_encryption_key$' -count=1
}

cd "$repo_root"
compose up -d --build db api
wait_for_api
scenario_cancel_queued
scenario_api_kill_worker_completes
scenario_worker_kill_after_completed
scenario_pure_takeover
scenario_side_effect_recovery
scenario_stale_token_rejected
scenario_cancel_running
scenario_cancel_recovery
scenario_ndjson_disconnect
scenario_backup_wrong_key
scenario_backup_same_key

printf '%s\n' 'durable runs E2E passed'
