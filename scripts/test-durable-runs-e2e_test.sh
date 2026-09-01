#!/bin/sh
set -eu

script=scripts/test-durable-runs-e2e.sh
[ -f "$script" ] || {
  printf '%s\n' "$script is missing" >&2
  exit 1
}

require_marker() {
  marker=$1
  grep -F "$marker" "$script" >/dev/null || {
    printf '%s\n' "durable E2E scenario is missing: $marker" >&2
    exit 1
  }
}

for marker in \
  scenario_api_kill_worker_completes \
  scenario_worker_kill_after_completed \
  scenario_pure_takeover \
  scenario_side_effect_recovery \
  scenario_stale_token_rejected \
  scenario_cancel_queued \
  scenario_cancel_running \
  scenario_cancel_recovery \
  scenario_ndjson_disconnect \
  scenario_backup_same_key \
  scenario_backup_wrong_key
do
  require_marker "$marker"
done

require_marker 'wait_for_status()'
require_marker 'while [ "$attempts" -lt "$WAIT_ATTEMPTS" ]'
require_marker 'trap cleanup EXIT HUP INT TERM'
require_marker 'COMPOSE_PROJECT_NAME'
require_marker 'CGO_ENABLED=0'

ruby -ryaml -e '
  workflow = YAML.safe_load(File.read(ARGV.fetch(0)), aliases: true)
  job = workflow.fetch("jobs").fetch("durable-runs")
  raise "durable job timeout must be bounded" unless job.fetch("timeout-minutes").to_i.between?(1, 10)
  raise "durable job must disable CGO" unless job.fetch("env").fetch("CGO_ENABLED") == "0"
  steps = job.fetch("steps")
  raise "durable E2E command missing" unless steps.any? { |step| step["run"] == "make test-durable-runs-e2e" }
  upload = steps.find { |step| step["name"] == "Preserve failure logs" }
  raise "failure log upload missing" unless upload && upload.fetch("if") == "failure()" && upload.fetch("uses").match?(/@[0-9a-f]{40}$/)
' .github/workflows/ci.yml

if grep -E 'docker compose down[[:space:]]+-v|docker volume rm|while[[:space:]]+true' "$script" >/dev/null; then
  printf '%s\n' 'durable E2E contains destructive cleanup or an unbounded wait' >&2
  exit 1
fi

printf '%s\n' 'durable run E2E contract passed'
