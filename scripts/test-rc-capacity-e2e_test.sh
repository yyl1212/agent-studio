#!/bin/sh
set -eu

script=scripts/test-rc-capacity-e2e.sh
workflow=.github/workflows/ci.yml
makefile=Makefile
expected_summary_keys='["completed","duplicateTerminalEvents","elapsedMs","nonCompleted","queueWaitP95Ms","remainingLeases","remainingQueueDepth","submitted","throughputPerSecond"]'

validate_script() {
  ruby -rjson -e '
    source = File.read(ARGV.fetch(0))
    expected_summary_keys = JSON.parse(ARGV.fetch(1))

    def require_marker(source, marker)
      raise "rc capacity scenario is missing: #{marker}" unless source.include?(marker)
    end

    def require_single_process(source, role, executable)
      variables = source.scan(/^\s*([[:alpha:]_][[:alnum:]_]*)=.*#{Regexp.escape(executable)}/).flatten
      bindings = variables.sum do |variable|
        source.scan(/"\$#{Regexp.escape(variable)}"[^\n]*&\s*\n\s*#{role}_pid=\$!/).length
      end
      raise "rc capacity scenario must start exactly one #{role} process and bind its PID" unless bindings == 1
    end

    [
      "COMPOSE_PROJECT_NAME",
      "tmpfs:",
      "WORKER_MAX_ACTIVE_RUNS: \"4\"",
      "RUN_COUNT=${RUN_COUNT:-500}",
      "RC_CAPACITY_DEADLINE_SECONDS=${RC_CAPACITY_DEADLINE_SECONDS:-570}",
      "trap cleanup EXIT HUP INT TERM",
      "duplicateTerminalEvents",
      "remainingLeases",
      "remainingQueueDepth",
      "queueWaitP95Ms",
    ].each { |marker| require_marker(source, marker) }

    require_single_process(source, "api", "agent-studio-api")
    require_single_process(source, "worker", "agent-studio-worker")
    raise "RUN_COUNT must only accept 500" unless source.match?(/\[ "\$RUN_COUNT" = "500" \]/)
    raise "deadline must reject non-integers" unless source.match?(/case "\$RC_CAPACITY_DEADLINE_SECONDS" in.*\*\[!0-9\]\*/m)
    raise "deadline lower bound missing" unless source.match?(/\[ "\$RC_CAPACITY_DEADLINE_SECONDS" -lt 60 \]/)
    raise "deadline upper bound missing" unless source.match?(/\[ "\$RC_CAPACITY_DEADLINE_SECONDS" -gt 570 \]/)

    project_defined = source.match?(/(?:^|\n)\s*(?:export\s+)?COMPOSE_PROJECT_NAME=[^\n]*\$\$/)
    project_bound = source.match?(/export\s+COMPOSE_PROJECT_NAME/) || source.match?(/docker\s+compose[^\n]*--project-name\s+"\$COMPOSE_PROJECT_NAME"/)
    raise "Compose project must be unique and bound to every Compose command" unless project_defined && project_bound
    raise "run_root must come from mktemp -d" unless source.match?(/run_root=\$\(mktemp -d\b/)
    raise "compose file must be inside run_root" unless source.match?(/compose_file="\$run_root\/compose\.ya?ml"/)
    raise "PostgreSQL port must bind to loopback" unless source.include?("127.0.0.1:$db_port:5432")
    raise "API port must bind to loopback" unless source.include?("127.0.0.1:$api_port")

    raise "summary must be written by jq -n to summary_file" unless source.match?(/jq\s+-n\b.*>\s*"\$summary_file"/m)
    summary_checks = source.scan(/jq\s+-e\s+([\x27\"])(.*?)\1\s+"\$summary_file"/m).map(&:last)
    keys_check = summary_checks.any? do |expression|
      keys = expression[/keys\s*==\s*(\[[^\]]*\])/m, 1]
      keys && JSON.parse(keys) == expected_summary_keys
    rescue JSON::ParserError
      false
    end
    raise "summary must use jq -e to enforce the exact key set" unless keys_check

    source.each_line do |line|
      if line.match?(/(?:\bdocker\s+compose\b|\bcompose\b)[^\n]*\bdown\b[^\n]*(?:^|[[:space:]])-v(?:[[:space:]]|$)/)
        raise "rc capacity scenario contains docker compose down -v"
      end
      raise "rc capacity scenario contains docker volume rm" if line.match?(/\bdocker\s+volume\s+rm\b/)
      raise "rc capacity scenario contains an unbounded wait" if line.match?(/\bwhile[[:space:]]+true\b/)
      if line.match?(/RUN_PAYLOAD_ENCRYPTION_KEY\s*=\s*\x27[^$\x27][^\x27]*\x27/) ||
         line.match?(/RUN_PAYLOAD_ENCRYPTION_KEY\s*=\s*\"[^$\"][^\"]*\"/) ||
         line.match?(/RUN_PAYLOAD_ENCRYPTION_KEY\s*=\s*(?![\"\x27$])[[:alnum:]+\/=._-]+/)
        raise "rc capacity scenario contains a plaintext payload key"
      end
      next unless line.match?(/\brm[[:space:]]+-rf\b/)

      target = line.sub(/^.*\brm[[:space:]]+-rf[[:space:]]+/, "").strip.sub(/[[:space:]]+#.*$/, "")
      unless ["\"$run_root\"", "\"${run_root:?}\""].include?(target)
        raise "rc capacity scenario contains an unbounded rm -rf target: #{target}"
      end
    end
  ' "$1" "$expected_summary_keys"
}

expect_script_rejected() {
  rejected_fixture=$1
  expected_error=$2
  if output=$(validate_script "$rejected_fixture" 2>&1); then
    printf '%s\n' "fixture unexpectedly passed: $rejected_fixture" >&2
    exit 1
  fi
  case "$output" in
    *"$expected_error"*) ;;
    *)
      printf '%s\n' "fixture failed for the wrong reason ($rejected_fixture): $output" >&2
      exit 1
      ;;
  esac
}

run_contract_self_test() {
  fixture_root=$(mktemp -d "${TMPDIR:-/tmp}/agent-studio-rc-capacity-contract.XXXXXX")
  trap 'rm -rf "$fixture_root"' EXIT HUP INT TERM
  fixture="$fixture_root/scenario.sh"
  cat >"$fixture" <<'EOF'
#!/bin/sh
run_root=$(mktemp -d /tmp/agent-studio-capacity.XXXXXX)
compose_file="$run_root/compose.yaml"
COMPOSE_PROJECT_NAME="agent_studio_capacity_$$"
export COMPOSE_PROJECT_NAME
RUN_COUNT=${RUN_COUNT:-500}
RC_CAPACITY_DEADLINE_SECONDS=${RC_CAPACITY_DEADLINE_SECONDS:-570}
[ "$RUN_COUNT" = "500" ] || exit 2
case "$RC_CAPACITY_DEADLINE_SECONDS" in
  ''|*[!0-9]*) exit 2 ;;
esac
if [ "$RC_CAPACITY_DEADLINE_SECONDS" -lt 60 ] || [ "$RC_CAPACITY_DEADLINE_SECONDS" -gt 570 ]; then exit 2; fi
api_bin="$run_root/agent-studio-api"
worker_bin="$run_root/agent-studio-worker"
"$api_bin" &
api_pid=$!
"$worker_bin" &
worker_pid=$!
cat <<'COMPOSE'
tmpfs:
WORKER_MAX_ACTIVE_RUNS: "4"
127.0.0.1:$db_port:5432
127.0.0.1:$api_port
COMPOSE
cleanup() { :; }
trap cleanup EXIT HUP INT TERM
summary_file="$run_root/summary.json"
jq -n '{}' > "$summary_file"
jq -e 'keys == ["completed","duplicateTerminalEvents","elapsedMs","nonCompleted","queueWaitP95Ms","remainingLeases","remainingQueueDepth","submitted","throughputPerSecond"]' "$summary_file"
duplicateTerminalEvents=0
remainingLeases=0
remainingQueueDepth=0
queueWaitP95Ms=0
rm -rf "$run_root"
EOF

  validate_script "$fixture"
  for unsafe in compose_down_v literal_single_key literal_double_key extra_summary_key; do
    candidate="$fixture_root/$unsafe.sh"
    cat "$fixture" >"$candidate"
    case "$unsafe" in
      compose_down_v) printf '%s\n' 'docker compose -f "$compose_file" down -v' >>"$candidate" ;;
      literal_single_key) printf '%s\n' "RUN_PAYLOAD_ENCRYPTION_KEY='literal-test-key'" >>"$candidate" ;;
      literal_double_key) printf '%s\n' 'RUN_PAYLOAD_ENCRYPTION_KEY="literal-test-key"' >>"$candidate" ;;
      extra_summary_key)
        ruby -e 'path = ARGV.fetch(0); source = File.read(path); File.write(path, source.sub("\"throughputPerSecond\"]", "\"throughputPerSecond\",\"extra\"]"))' "$candidate"
        ;;
    esac
    case "$unsafe" in
      compose_down_v) expect_script_rejected "$candidate" 'docker compose down -v' ;;
      literal_single_key|literal_double_key) expect_script_rejected "$candidate" 'plaintext payload key' ;;
      extra_summary_key) expect_script_rejected "$candidate" 'exact key set' ;;
    esac
  done
  trap - EXIT HUP INT TERM
  rm -rf "$fixture_root"
}

[ -f "$workflow" ] || {
  printf '%s\n' "$workflow is missing" >&2
  exit 1
}
ruby -ryaml -e 'YAML.safe_load(File.read(ARGV.fetch(0)), aliases: true)' "$workflow"

[ -f "$makefile" ] || {
  printf '%s\n' "$makefile is missing" >&2
  exit 1
}
ruby -e '
  makefile = File.read(ARGV.fetch(0))
  target = /^test-rc-capacity-e2e:\n\tsh scripts\/test-rc-capacity-e2e\.sh$/
  raise "rc capacity Makefile target is missing or malformed" unless makefile.match?(target)
  phony = makefile.lines.find { |line| line.start_with?(".PHONY:") }
  raise "rc capacity Makefile target must be phony" unless phony && phony.split.include?("test-rc-capacity-e2e")
' "$makefile"

run_contract_self_test

[ -f "$script" ] || {
  printf '%s\n' "$script is missing (Task 5 must add the 500-run capacity scenario)" >&2
  exit 1
}
validate_script "$script"

ruby -ryaml -e '
  workflow = YAML.safe_load(File.read(ARGV.fetch(0)), aliases: true)
  job = workflow.fetch("jobs").fetch("rc-capacity")
  raise "rc capacity job timeout must be 12 minutes" unless job.fetch("timeout-minutes") == 12
  env = job.fetch("env")
  raise "rc capacity job must disable CGO" unless env.fetch("CGO_ENABLED") == "0"
  raise "rc capacity artifact directory missing" unless env.fetch("RC_CAPACITY_ARTIFACT_DIR") == "artifacts/rc-capacity"
  expected_key = "MDEyMzQ1Njc4OWFiY2RlZjAxMjM0NTY3ODlhYmNkZWY="
  raise "rc capacity test key must be the fixed non-production key" unless env.fetch("RUN_PAYLOAD_ENCRYPTION_KEY") == expected_key

  steps = job.fetch("steps")
  contract = steps.find { |step| step["run"] == "sh scripts/test-rc-capacity-e2e_test.sh" }
  raise "capacity contract step missing" unless contract
  baseline = steps.find { |step| step["run"] == "timeout 10m make test-rc-capacity-e2e" }
  raise "capacity baseline step missing or malformed" unless baseline && baseline["timeout-minutes"] == 10

  uploads = steps.select { |step| step["uses"].to_s.match?(/actions\/upload-artifact@[0-9a-f]{40}$/) }
  summary = uploads.find do |step|
    step["if"] == "always()" && step.dig("with", "path") == "artifacts/rc-capacity/summary.json"
  end
  raise "capacity summary upload missing" unless summary && summary.dig("with", "retention-days") == 7
  logs = uploads.find do |step|
    step["if"] == "failure()" && step["name"] == "Preserve capacity failure logs" && step.dig("with", "path") == "artifacts/rc-capacity/*.log"
  end
  raise "capacity failure log upload missing or malformed" unless logs && logs.dig("with", "retention-days") == 7
' "$workflow"

printf '%s\n' 'rc capacity E2E contract passed'
