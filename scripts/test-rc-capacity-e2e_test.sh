#!/bin/sh
set -eu

script=scripts/test-rc-capacity-e2e.sh
workflow=.github/workflows/ci.yml
makefile=Makefile

[ -f "$workflow" ] || {
  printf '%s\n' "$workflow is missing" >&2
  exit 1
}

# Parse the workflow before looking for the Task 5 files.  This keeps a RED
# result attributable to a missing capacity implementation or CI job, never a
# malformed Ruby/YAML check in this contract.
ruby -ryaml -e '
  YAML.safe_load(File.read(ARGV.fetch(0)), aliases: true)
' "$workflow"

[ -f "$makefile" ] || {
  printf '%s\n' "$makefile is missing" >&2
  exit 1
}

ruby -e '
  makefile = File.read(ARGV.fetch(0))
  target = /^test-rc-capacity-e2e:\n\tsh scripts\/test-rc-capacity-e2e\.sh$/
  raise "rc capacity Makefile target is missing or malformed" unless makefile.match?(target)
' "$makefile"

[ -f "$script" ] || {
  printf '%s\n' "$script is missing (Task 5 must add the 500-run capacity scenario)" >&2
  exit 1
}

require_marker() {
  marker=$1
  grep -F "$marker" "$script" >/dev/null || {
    printf '%s\n' "rc capacity scenario is missing: $marker" >&2
    exit 1
  }
}

for marker in \
  'COMPOSE_PROJECT_NAME' \
  'tmpfs:' \
  'WORKER_MAX_ACTIVE_RUNS: "4"' \
  'RUN_COUNT=${RUN_COUNT:-500}' \
  'RC_CAPACITY_DEADLINE_SECONDS=${RC_CAPACITY_DEADLINE_SECONDS:-570}' \
  'trap cleanup EXIT HUP INT TERM' \
  'duplicateTerminalEvents' \
  'remainingLeases' \
  'remainingQueueDepth' \
  'queueWaitP95Ms'
do
  require_marker "$marker"
done

ruby -e '
  script = File.read(ARGV.fetch(0))
  required = {
    "one API process" => /api_pid=\$!/,
    "one Worker process" => /worker_pid=\$!/,
    "RUN_COUNT must only accept 500" => /\[ "\$RUN_COUNT" = "500" \]/,
    "deadline lower bound" => /RC_CAPACITY_DEADLINE_SECONDS.*60/,
    "deadline upper bound" => /RC_CAPACITY_DEADLINE_SECONDS.*570/,
    "unique Compose project" => /COMPOSE_PROJECT_NAME=.*\$\$/,
    "temporary Compose file" => /compose_file=.*run_root/,
    "PostgreSQL loopback port" => /127\.0\.0\.1:\$db_port:5432/,
    "API loopback port" => /127\.0\.0\.1:\$api_port/,
  }
  required.each do |description, pattern|
    raise "rc capacity scenario is missing #{description}" unless script.match?(pattern)
  end

  summary = script[/jq -n(?<body>.*?)(?:\n\n|\n[[:alpha:]_][[:alnum:]_]*\(\)|\z)/m, :body]
  raise "rc capacity summary must be generated with jq -n" unless summary
  expected_fields = %w[
    submitted completed nonCompleted duplicateTerminalEvents remainingLeases
    remainingQueueDepth elapsedMs throughputPerSecond queueWaitP95Ms
  ]
  actual_fields = summary.scan(/(?:^|[,{[:space:]])([[:alpha:]][[:alnum:]_]*)\s*:/).flatten
  raise "rc capacity summary fields must be exact: #{actual_fields.inspect}" unless actual_fields == expected_fields
' "$script"

if grep -E 'docker compose down[[:space:]]+-v|docker volume rm|while[[:space:]]+true|RUN_PAYLOAD_ENCRYPTION_KEY=[^"$[:space:]]|rm[[:space:]]+-rf([[:space:]]|$)' "$script" >/dev/null; then
  printf '%s\n' 'rc capacity scenario contains unsafe cleanup, an unbounded wait, or a plaintext key' >&2
  exit 1
fi

ruby -ryaml -e '
  workflow = YAML.safe_load(File.read(ARGV.fetch(0)), aliases: true)
  job = workflow.fetch("jobs").fetch("rc-capacity")
  raise "rc capacity job timeout must be 12 minutes" unless job.fetch("timeout-minutes") == 12
  env = job.fetch("env")
  raise "rc capacity job must disable CGO" unless env.fetch("CGO_ENABLED") == "0"
  raise "rc capacity artifact directory missing" unless env.fetch("RC_CAPACITY_ARTIFACT_DIR") == "artifacts/rc-capacity"
  raise "rc capacity test key missing" if env.fetch("RUN_PAYLOAD_ENCRYPTION_KEY").to_s.empty?

  steps = job.fetch("steps")
  contract = steps.find { |step| step["name"] == "Verify capacity contract" }
  raise "capacity contract step missing" unless contract && contract["run"] == "sh scripts/test-rc-capacity-e2e_test.sh"
  baseline = steps.find { |step| step["name"] == "Exercise 500-run capacity baseline" }
  raise "capacity baseline step missing" unless baseline && baseline["timeout-minutes"] == 10 && baseline["run"] == "timeout 10m make test-rc-capacity-e2e"

  uploads = steps.select { |step| step["uses"].to_s.match?(/actions\/upload-artifact@[0-9a-f]{40}$/) }
  summary = uploads.find do |step|
    step["if"] == "always()" && step.dig("with", "path") == "artifacts/rc-capacity/summary.json"
  end
  raise "capacity summary upload missing" unless summary && summary.dig("with", "retention-days") == 7
  logs = uploads.find { |step| step["if"] == "failure()" }
  raise "capacity failure log upload missing" unless logs && logs.dig("with", "retention-days") == 7
' "$workflow"

printf '%s\n' 'rc capacity E2E contract passed'
