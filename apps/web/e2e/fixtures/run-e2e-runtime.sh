#!/bin/sh
set -eu

: "${RUN_PAYLOAD_ENCRYPTION_KEY:?RUN_PAYLOAD_ENCRYPTION_KEY is required}"

runtime_root=$(CDPATH= cd -- "$(dirname "$0")/../../.." && pwd)/api
run_root=$(mktemp -d "${TMPDIR:-/tmp}/agent-studio-playwright-runtime.XXXXXX")
api_pid=
worker_pid=

cleanup() {
  status=$?
  trap - EXIT HUP INT TERM
  [ -z "$api_pid" ] || kill "$api_pid" >/dev/null 2>&1 || true
  [ -z "$worker_pid" ] || kill "$worker_pid" >/dev/null 2>&1 || true
  [ -z "$api_pid" ] || wait "$api_pid" >/dev/null 2>&1 || true
  [ -z "$worker_pid" ] || wait "$worker_pid" >/dev/null 2>&1 || true
  rm -rf "$run_root"
  exit "$status"
}
trap cleanup EXIT HUP INT TERM

cd "$runtime_root"
CGO_ENABLED=0 go build -o "$run_root/agent-studio-api" ./cmd/server
CGO_ENABLED=0 go build -o "$run_root/agent-studio-worker" ./cmd/worker

export CGO_ENABLED=0
export DATABASE_URL=postgres://agent:agent@127.0.0.1:5432/agent_studio?sslmode=disable
export MODEL_PROVIDER=mock
export HTTP_ADDR=127.0.0.1:8080
export AGENT_STUDIO_WEBHOOK_URL=http://127.0.0.1:8090
export AGENT_STUDIO_WEBHOOK_TOKEN=e2e-webhook-secret
export WORKER_MAX_ACTIVE_RUNS=1

"$run_root/agent-studio-worker" &
worker_pid=$!
"$run_root/agent-studio-api" &
api_pid=$!

wait "$api_pid"
