#!/bin/sh
set -eu

script=scripts/test-rc-capacity-e2e.sh
workflow=.github/workflows/ci.yml
makefile=Makefile
summary_keys='["completed","duplicateTerminalEvents","elapsedMs","nonCompleted","queueWaitP95Ms","remainingLeases","remainingQueueDepth","submitted","throughputPerSecond"]'

validate_script() {
  ruby -rjson -ryaml -e '
    source = File.read(ARGV.fetch(0))
    expected_keys = JSON.parse(ARGV.fetch(1))

    def strip_comment(line)
      quote = nil
      escaped = false
      result = +""
      line.each_char do |character|
        if quote
          result << character
          if quote == "\"" && character == "\\" && !escaped
            escaped = true
            next
          end
          quote = nil if character == quote && !escaped
          escaped = false
        elsif character == "\"" || character == "\x27"
          quote = character
          result << character
        elsif character == "#"
          break
        else
          result << character
        end
      end
      result.strip
    end

    def split_commands(line)
      quote = nil
      escaped = false
      commands = [+""]
      line.each_char do |character|
        if quote
          commands.last << character
          if quote == "\"" && character == "\\" && !escaped
            escaped = true
            next
          end
          quote = nil if character == quote && !escaped
          escaped = false
        elsif character == "\"" || character == "\x27"
          quote = character
          commands.last << character
        elsif character == ";"
          commands << +""
        else
          commands.last << character
        end
      end
      commands.map(&:strip).reject(&:empty?)
    end

    lines = source.lines.map { |line| strip_comment(line).gsub("${summary_file}", "$summary_file") }.reject(&:empty?)
    commands = lines.flat_map { |line| split_commands(line) }
    code = lines.join("\n")
    require_marker = ->(marker) { raise "rc capacity scenario is missing: #{marker}" unless code.include?(marker) }
    ["set -eu", "tmpfs:", "WORKER_MAX_ACTIVE_RUNS: \"4\"", "RUN_COUNT=${RUN_COUNT:-500}", "RC_CAPACITY_DEADLINE_SECONDS=${RC_CAPACITY_DEADLINE_SECONDS:-570}", "trap cleanup EXIT HUP INT TERM", "duplicateTerminalEvents", "remainingLeases", "remainingQueueDepth", "queueWaitP95Ms"].each { |marker| require_marker.call(marker) }

    project_assignments = lines.grep(/^COMPOSE_PROJECT_NAME=/)
    raise "COMPOSE_PROJECT_NAME must be assigned exactly once" unless project_assignments.length == 1
    raise "COMPOSE_PROJECT_NAME must be PID-derived" unless project_assignments.first == "COMPOSE_PROJECT_NAME=\"agent_studio_rc_capacity_$$\""
    project_index = lines.index(project_assignments.first)
    project_exports = lines.each_index.select { |index| lines[index].match?(/^export COMPOSE_PROJECT_NAME(?:[[:space:]]|$)/) }
    raise "COMPOSE_PROJECT_NAME must be exported exactly once after assignment" unless project_exports.length == 1 && project_exports.first > project_index

    run_roots = lines.grep(/^run_root=/)
    expected_run_root = "run_root=$(mktemp -d \"${TMPDIR:-/tmp}/agent-studio-rc-capacity.XXXXXX\")"
    raise "run_root must be assigned exactly once with mktemp -d" unless run_roots == [expected_run_root]
    raise "compose file must be directly under run_root" unless lines.count("compose_file=\"$run_root/compose.yaml\"") == 1
    compose_documents = source.scan(/cat >"\$compose_file" <<\x27YAML\x27\n(.*?)\nYAML/m)
    raise "Compose YAML must be one normalized compose_file heredoc" unless compose_documents.length == 1
    services = YAML.safe_load(compose_documents.first.fetch(0)).fetch("services")
    raise "Compose services must be exactly api/db/worker" unless services.keys.sort == %w[api db worker]
    db, api, worker = services.values_at("db", "api", "worker")
    raise "db must use PostgreSQL 18 tmpfs with its loopback port" unless db["image"] == "postgres:18" && db["tmpfs"] && db["ports"] == ["127.0.0.1:$db_port:5432"]
    raise "API and Worker must share test_image" unless api["image"] == "$test_image" && worker["image"] == "$test_image"
    raise "API must expose one loopback port" unless api["ports"] == ["127.0.0.1:$api_port:8080"]
    raise "Worker must not expose ports" if worker.key?("ports")
    raise "Worker concurrency must be string 4" unless worker.dig("environment", "WORKER_MAX_ACTIVE_RUNS") == "4"

    raise "test image must be non-empty and unique" unless lines.count("test_image=\"agent-studio:rc-capacity-e2e\"") == 1
    docker_builds = commands.select { |command| command.delete("\"\x27").split[0, 2] == %w[docker build] }
    raise "capacity image must be built exactly once" unless docker_builds == ["docker build -t \"$test_image\" \"$repo_root\""]
    services_command = "actual_services=$(docker compose -f \"$compose_file\" config --services | sort | tr \x27\\n\x27 \x27 \x27)"
    raise "Compose must define exactly db/api/worker services" unless lines.include?(services_command) && lines.include?("[ \"$actual_services\" = \"api db worker \" ] || exit 1")
    def compose_subcommand(tokens)
      index = tokens.each_cons(2).find_index { |pair| pair == %w[docker compose] }
      return nil unless index
      tail = tokens[(index + 2)..]
      until tail.empty? || !tail.first.start_with?("-")
        option = tail.shift
        tail.shift if %w[-f --file -p --project-name --project-directory --env-file].include?(option)
      end
      tail.first
    end
    compose_ups = commands.select { |command| compose_subcommand(command.delete("\"\x27").split) == "up" }
    raise "Compose must start db/api/worker exactly once" unless compose_ups == ["docker compose -f \"$compose_file\" up -d db api worker"]
    raise "Compose scaling is forbidden" if code.match?(/--scale|scale:/)
    contract_only = "if [ \"${RC_CAPACITY_CONTRACT_ONLY:-}\" = \"1\" ]; then exit 0; fi"
    contract_index = lines.index(contract_only)
    config_index = lines.index(services_command)
    up_index = lines.index(compose_ups.first)
    raise "contract-only mode must exit after config and before up" unless contract_index && config_index && up_index && config_index < contract_index && contract_index < up_index
    cleanup_start = lines.index("cleanup() {")
    cleanup_end = cleanup_start && lines[(cleanup_start + 1)..].index("}")
    raise "cleanup block missing" unless cleanup_start && cleanup_end
    cleanup = lines[cleanup_start..cleanup_start + cleanup_end]
    raise "cleanup must remove only this Compose project" unless cleanup.include?("docker compose -f \"$compose_file\" down --remove-orphans >/dev/null 2>&1 || true")
    raise "cleanup must remove only run_root" unless cleanup.include?("rm -rf \"$run_root\"")
    trap_index = lines.index("trap cleanup EXIT HUP INT TERM")
    raise "cleanup trap must precede contract-only and up" unless trap_index && trap_index < contract_index && trap_index < up_index

    raise "RUN_COUNT must only accept 500" unless lines.include?("[ \"$RUN_COUNT\" = \"500\" ] || exit 1")
    deadline_start = lines.index("validate_deadline() {")
    deadline_end = deadline_start && lines[(deadline_start + 1)..].index("}")
    raise "deadline validator missing" unless deadline_start && deadline_end
    deadline = lines[deadline_start..deadline_start + deadline_end]
    raise "deadline must reject non-integers directly" unless deadline.any? { |line| line.match?(/^\x27\x27\|\*\[!0-9\]\*\).*\b(?:exit|return) 1/) }
    raise "deadline lower bound must fail directly" unless deadline.any? { |line| line.include?("[ \"$RC_CAPACITY_DEADLINE_SECONDS\" -lt 60 ]") && line.match?(/\b(?:exit|return) 1/) }
    raise "deadline upper bound must fail directly" unless deadline.any? { |line| line.include?("[ \"$RC_CAPACITY_DEADLINE_SECONDS\" -gt 570 ]") && line.match?(/\b(?:exit|return) 1/) }
    raise "deadline validation cannot be neutralized" if deadline.any? { |line| line.include?("|| true") || line.include?("|| :") }
    raise "deadline validator must run" unless lines.count("validate_deadline() {") == 1 && lines.count("validate_deadline \"$RC_CAPACITY_DEADLINE_SECONDS\"") == 1

    raise "summary_file must be assigned once under run_root" unless lines.grep(/^summary_file=/) == ["summary_file=\"$run_root/summary.json\""]
    raise "summary must be written by jq -n" unless lines.any? { |line| line.start_with?("jq -n ") && line.end_with?("> \"$summary_file\"") }
    exact_summary_check = "jq -e \x27keys == #{JSON.generate(expected_keys)}\x27 \"$summary_file\" >/dev/null"
    summary_index = lines.index(exact_summary_check)
    raise "summary key validation must be one normalized jq command" unless summary_index
    later_summary = lines[(summary_index + 1)..]
    allowed_read = "cat \"$summary_file\""
    raise "summary cannot be mutated after key validation" if later_summary.any? { |line| line.include?("$summary_file") && line != allowed_read }

    commands.each do |command|
      tokens = command.delete("\"\x27").split
      compose = tokens.first == "compose" || tokens.each_cons(2).any? { |pair| pair == %w[docker compose] }
      if compose && tokens.include?("down") && (tokens.include?("-v") || tokens.include?("--volumes"))
        raise "rc capacity scenario contains destructive Compose volume cleanup"
      end
      raise "rc capacity scenario contains docker volume rm" if tokens.each_cons(3).any? { |triple| triple == %w[docker volume rm] }
      raise "rc capacity scenario contains an unbounded wait" if tokens == %w[while true do] || tokens == %w[while true]
      if command.match?(/RUN_PAYLOAD_ENCRYPTION_KEY\s*=\s*(?:\x27[^\x27]*\x27|\"[^\"]*\")/) || command.match?(/RUN_PAYLOAD_ENCRYPTION_KEY\s*=\s*(?![\"\x27$])[[:alnum:]+\/=._-]+/)
        raise "rc capacity scenario contains a plaintext payload key"
      end
      next unless tokens[0, 2] == %w[rm -rf]
      raise "rc capacity scenario contains an unbounded rm -rf target" unless tokens == ["rm", "-rf", "$run_root"]
    end
  ' "$1" "$summary_keys"
}

expect_rejected() {
  candidate=$1
  expected=$2
  if output=$(validate_script "$candidate" 2>&1); then
    printf '%s\n' "fixture unexpectedly passed: $candidate" >&2
    exit 1
  fi
  case "$output" in *"$expected"*) ;; *) printf '%s\n' "fixture failed for the wrong reason ($candidate): $output" >&2; exit 1;; esac
}

run_contract_self_test() {
  fixture_root=$(mktemp -d "${TMPDIR:-/tmp}/agent-studio-rc-capacity-contract.XXXXXX")
  trap 'rm -rf "$fixture_root"' EXIT HUP INT TERM
  base="$fixture_root/base.sh"
  cat >"$base" <<'FIXTURE'
#!/bin/sh
set -eu
COMPOSE_PROJECT_NAME="agent_studio_rc_capacity_$$"
export COMPOSE_PROJECT_NAME
run_root=$(mktemp -d "${TMPDIR:-/tmp}/agent-studio-rc-capacity.XXXXXX")
compose_file="$run_root/compose.yaml"
RUN_COUNT=${RUN_COUNT:-500}
RC_CAPACITY_DEADLINE_SECONDS=${RC_CAPACITY_DEADLINE_SECONDS:-570}
[ "$RUN_COUNT" = "500" ] || exit 1
validate_deadline() {
  case "$RC_CAPACITY_DEADLINE_SECONDS" in
    ''|*[!0-9]*) exit 1 ;;
  esac
  if [ "$RC_CAPACITY_DEADLINE_SECONDS" -lt 60 ]; then exit 1; fi
  if [ "$RC_CAPACITY_DEADLINE_SECONDS" -gt 570 ]; then exit 1; fi
}
validate_deadline "$RC_CAPACITY_DEADLINE_SECONDS"
test_image="agent-studio:rc-capacity-e2e"
docker build -t "$test_image" "$repo_root"
cat >"$compose_file" <<'YAML'
services:
  db:
    image: postgres:18
    tmpfs:
      - /var/lib/postgresql
    ports:
      - "127.0.0.1:$db_port:5432"
  api:
    image: "$test_image"
    ports:
      - "127.0.0.1:$api_port:8080"
  worker:
    image: "$test_image"
    environment:
      WORKER_MAX_ACTIVE_RUNS: "4"
YAML
cleanup() {
  docker compose -f "$compose_file" down --remove-orphans >/dev/null 2>&1 || true
  rm -rf "$run_root"
}
trap cleanup EXIT HUP INT TERM
actual_services=$(docker compose -f "$compose_file" config --services | sort | tr '\n' ' ')
[ "$actual_services" = "api db worker " ] || exit 1
if [ "${RC_CAPACITY_CONTRACT_ONLY:-}" = "1" ]; then exit 0; fi
docker compose -f "$compose_file" up -d db api worker
summary_file="$run_root/summary.json"
jq -n '{}' > "$summary_file"
jq -e 'keys == ["completed","duplicateTerminalEvents","elapsedMs","nonCompleted","queueWaitP95Ms","remainingLeases","remainingQueueDepth","submitted","throughputPerSecond"]' "$summary_file" >/dev/null
cat "$summary_file"
duplicateTerminalEvents=0
remainingLeases=0
remainingQueueDepth=0
queueWaitP95Ms=0
FIXTURE

  validate_script "$base"
  for case_name in duplicate_image duplicate_up contract_after_up worker_port no_cleanup late_trap deadline_noop fixed_project reassign_project comment_api_port summary_or_true summary_rewrite brace_overwrite semicolon_down single_quoted_variable run_root_rebind; do
    candidate="$fixture_root/$case_name.sh"
    cat "$base" >"$candidate"
    case "$case_name" in
      duplicate_image) printf '%s\n' 'docker build -t "$test_image" "$repo_root"' >>"$candidate"; expected='built exactly once';;
      duplicate_up) printf '%s\n' 'docker compose -f "$compose_file" up -d db api worker' >>"$candidate"; expected='start db/api/worker exactly once';;
      contract_after_up) ruby -e 'p=ARGV[0]; s=File.read(p); c="if [ \"${RC_CAPACITY_CONTRACT_ONLY:-}\" = \"1\" ]; then exit 0; fi\n"; File.write(p, s.sub(c, "").sub("docker compose -f \"$compose_file\" up -d db api worker\n", "docker compose -f \"$compose_file\" up -d db api worker\n#{c}"))' "$candidate"; expected='contract-only mode must exit';;
      worker_port) ruby -e 'p=ARGV[0]; s=File.read(p); File.write(p, s.sub("    environment:\n", "    ports:\n      - \"127.0.0.1:$worker_port:8080\"\n    environment:\n"))' "$candidate"; expected='Worker must not expose ports';;
      no_cleanup) ruby -e 'p=ARGV[0]; s=File.read(p); File.write(p, s.sub("  docker compose -f \"$compose_file\" down --remove-orphans >/dev/null 2>&1 || true", "  :"))' "$candidate"; expected='cleanup must remove only this Compose project';;
      late_trap) ruby -e 'p=ARGV[0]; s=File.read(p); t="trap cleanup EXIT HUP INT TERM\n"; File.write(p, s.sub(t, "").sub("docker compose -f \"$compose_file\" up -d db api worker\n", "docker compose -f \"$compose_file\" up -d db api worker\n#{t}"))' "$candidate"; expected='cleanup trap must precede';;
      deadline_noop) ruby -e 'p=ARGV[0]; s=File.read(p); File.write(p, s.sub("exit 1 ;;", ": ;;"))' "$candidate"; expected='deadline must reject non-integers directly';;
      fixed_project) ruby -e 'p=ARGV[0]; s=File.read(p); File.write(p, s.sub("COMPOSE_PROJECT_NAME=\"agent_studio_rc_capacity_$$\"", "COMPOSE_PROJECT_NAME=\"fixed\" # $$"))' "$candidate"; expected='PID-derived';;
      reassign_project) printf '%s\n' 'COMPOSE_PROJECT_NAME="agent_studio_rc_capacity_$$"' >>"$candidate"; expected='assigned exactly once';;
      comment_api_port) ruby -e 'p=ARGV[0]; s=File.read(p); File.write(p, s.sub("      - \"127.0.0.1:$api_port:8080\"", "      # 127.0.0.1:$api_port:8080"))' "$candidate"; expected='API must expose one loopback port';;
      summary_or_true) ruby -e 'p=ARGV[0]; s=File.read(p); needle="\"$summary_file\" >/dev/null"; File.write(p, s.sub(needle, "\"$summary_file\" >/dev/null || true"))' "$candidate"; expected='one normalized jq command';;
      summary_rewrite) printf '%s\n' 'jq -n "{}" > "$summary_file"' >>"$candidate"; expected='cannot be mutated';;
      brace_overwrite) printf '%s\n' 'jq -n "{}" > "${summary_file}"' >>"$candidate"; expected='cannot be mutated';;
      semicolon_down) printf '%s\n' 'docker compose -f "$compose_file" down --volumes; true' >>"$candidate"; expected='destructive Compose volume cleanup';;
      single_quoted_variable) printf '%s\n' "RUN_PAYLOAD_ENCRYPTION_KEY='\$STATIC_LITERAL'" >>"$candidate"; expected='plaintext payload key';;
      run_root_rebind) printf '%s\n' 'run_root=/' >>"$candidate"; expected='assigned exactly once';;
    esac
    expect_rejected "$candidate" "$expected"
  done
  trap - EXIT HUP INT TERM
  rm -rf "$fixture_root"
}

[ -f "$workflow" ] || { printf '%s\n' "$workflow is missing" >&2; exit 1; }
ruby -ryaml -e 'YAML.safe_load(File.read(ARGV.fetch(0)), aliases: true)' "$workflow"
[ -f "$makefile" ] || { printf '%s\n' "$makefile is missing" >&2; exit 1; }
ruby -e '
  makefile = File.read(ARGV.fetch(0))
  raise "rc capacity Makefile target is missing or malformed" unless makefile.match?(/^test-rc-capacity-e2e:\n\tsh scripts\/test-rc-capacity-e2e\.sh$/)
  phony = makefile.lines.find { |line| line.start_with?(".PHONY:") }
  raise "rc capacity Makefile target must be phony" unless phony && phony.split.include?("test-rc-capacity-e2e")
' "$makefile"
run_contract_self_test

[ -f "$script" ] || { printf '%s\n' "$script is missing (Task 5 must add the 500-run capacity scenario)" >&2; exit 1; }
validate_script "$script"

ruby -ryaml -e '
  workflow = YAML.safe_load(File.read(ARGV.fetch(0)), aliases: true)
  job = workflow.fetch("jobs").fetch("rc-capacity")
  raise "rc capacity job timeout must be 12 minutes" unless job.fetch("timeout-minutes") == 12
  env = job.fetch("env")
  raise "rc capacity job must disable CGO" unless env.fetch("CGO_ENABLED") == "0"
  raise "rc capacity artifact directory missing" unless env.fetch("RC_CAPACITY_ARTIFACT_DIR") == "artifacts/rc-capacity"
  raise "rc capacity test key must be fixed" unless env.fetch("RUN_PAYLOAD_ENCRYPTION_KEY") == "MDEyMzQ1Njc4OWFiY2RlZjAxMjM0NTY3ODlhYmNkZWY="
  steps = job.fetch("steps")
  contract = steps.find { |step| step["run"] == "sh scripts/test-rc-capacity-e2e_test.sh" }
  raise "capacity contract step missing" unless contract && !contract.key?("if") && (!contract.key?("continue-on-error") || contract["continue-on-error"] == false)
  baseline = steps.find { |step| step["run"] == "timeout 10m make test-rc-capacity-e2e" }
  raise "capacity baseline step missing or malformed" unless baseline && baseline["timeout-minutes"] == 10 && !baseline.key?("if") && (!baseline.key?("continue-on-error") || baseline["continue-on-error"] == false)
  uploads = steps.select { |step| step["uses"].to_s.match?(/actions\/upload-artifact@[0-9a-f]{40}$/) }
  summary = uploads.find { |step| step["if"] == "always()" && step.dig("with", "path") == "artifacts/rc-capacity/summary.json" }
  raise "capacity summary upload missing" unless summary && summary.dig("with", "retention-days") == 7
  logs = uploads.find { |step| step["if"] == "failure()" && step["name"] == "Preserve capacity failure logs" && step.dig("with", "path") == "artifacts/rc-capacity/*.log" }
  raise "capacity failure log upload missing or malformed" unless logs && logs.dig("with", "retention-days") == 7
' "$workflow"

printf '%s\n' 'rc capacity E2E contract passed'
