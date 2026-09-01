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
    ["set -eu", "tmpfs:", "WORKER_MAX_ACTIVE_RUNS: \"4\"", "RUN_COUNT=${RUN_COUNT:-500}", "RC_CAPACITY_DEADLINE_SECONDS=${RC_CAPACITY_DEADLINE_SECONDS:-570}", "--connect-timeout", "--max-time", "statement_timeout", "lock_timeout", "duplicateTerminalEvents", "remainingLeases", "remainingQueueDepth", "queueWaitP95Ms"].each { |marker| require_marker.call(marker) }

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

    image_assignments = lines.grep(/^test_image=/)
    allowed_images = ["test_image=\"agent-studio:rc-capacity-e2e\"", "test_image=\"agent-studio:rc-capacity-e2e-$$\""]
    raise "test image must be non-empty and unique" unless image_assignments.length == 1 && allowed_images.include?(image_assignments.first)
    docker_builds = commands.select { |command| command.delete("\"\x27").split.each_cons(2).any? { |pair| pair == %w[docker build] } }
    allowed_builds = ["docker build -t \"$test_image\" \"$repo_root\"", "run_bounded \"$cleanup_start_deadline_ms\" docker build -t \"$test_image\" \"$repo_root\""]
    raise "capacity image must be built exactly once" unless docker_builds.length == 1 && allowed_builds.include?(docker_builds.first)
    allowed_services_commands = [
      "actual_services=$(docker compose -f \"$compose_file\" config --services | sort | tr \x27\\n\x27 \x27 \x27)",
      "actual_services=$(run_bounded \"$cleanup_start_deadline_ms\" docker compose -f \"$compose_file\" config --services | sort | tr \x27\\n\x27 \x27 \x27)",
    ]
    services_command = lines.find { |line| allowed_services_commands.include?(line) }
    raise "Compose config command is missing or malformed" unless services_command
    services_assertion = "[ \"$actual_services\" = \"api db worker \" ] || exit 1"
    raise "Compose must define exactly db/api/worker services" unless lines.include?(services_command) && lines.include?(services_assertion)
    def compose_subcommand(tokens)
      index = tokens.each_cons(2).find_index { |pair| pair == %w[docker compose] }
      return nil unless index
      tail = tokens[(index + 2)..]
      until tail.empty? || !tail.first.start_with?("-")
        option = tail.shift
        tail.shift if %w[-f --file -p --project-name --project-directory --env-file --ansi --profile --parallel --progress].include?(option)
      end
      tail.first
    end
    compose_ups = commands.select { |command| compose_subcommand(command.delete("\"\x27").split) == "up" }
    allowed_ups = ["docker compose -f \"$compose_file\" up -d db api worker", "run_bounded \"$cleanup_start_deadline_ms\" docker compose -f \"$compose_file\" up -d db api worker"]
    raise "Compose must start db/api/worker exactly once" unless compose_ups.length == 1 && allowed_ups.include?(compose_ups.first)
    raise "Compose scaling is forbidden" if code.match?(/--scale|scale:/)
    contract_only = "if [ \"${RC_CAPACITY_CONTRACT_ONLY:-}\" = \"1\" ]; then exit 0; fi"
    contract_index = lines.index(contract_only)
    config_index = lines.index(services_command)
    assertion_index = lines.index(services_assertion)
    up_index = lines.index(compose_ups.first)
    raise "Compose verification order is invalid" unless config_index && assertion_index && contract_index && up_index && config_index < assertion_index && assertion_index < contract_index && contract_index < up_index
    cleanup_start = lines.index("cleanup() {")
    cleanup_end = cleanup_start && lines[(cleanup_start + 1)..].index("}")
    raise "cleanup block missing" unless cleanup_start && cleanup_end
    cleanup = lines[cleanup_start..cleanup_start + cleanup_end]
    raise "cleanup must receive an explicit status" unless cleanup.include?("status=$1")
    raise "cleanup must not derive signal status from $?" if cleanup.include?("status=$?")
    allowed_down = ["docker compose -f \"$compose_file\" down --remove-orphans >/dev/null 2>&1 || true", "run_bounded \"$down_deadline_ms\" docker compose -f \"$compose_file\" down --remove-orphans >/dev/null 2>&1 || true"]
    raise "cleanup must remove only this Compose project" unless cleanup.any? { |line| allowed_down.include?(line) }
    raise "cleanup must remove only run_root" unless cleanup.include?("rm -rf \"$run_root\"")
    expected_traps = [
      "trap \x27status=$?; cleanup \"$status\"\x27 EXIT",
      "trap on_hup HUP",
      "trap on_int INT",
      "trap on_term TERM",
    ]
    trap_index = lines.index(expected_traps.first)
    raise "explicit cleanup traps must immediately follow run_root" unless trap_index == lines.index(expected_run_root) + 1 && lines[trap_index, expected_traps.length] == expected_traps
    {"on_hup() {" => "cleanup 129", "on_int() {" => "cleanup 130", "on_term() {" => "cleanup 143"}.each do |handler, exit_call|
      handler_index = lines.index(handler)
      raise "signal handler missing: #{handler}" unless handler_index && lines[(handler_index + 1), 2].include?(exit_call)
    end

    run_default = "RUN_COUNT=${RUN_COUNT:-500}"
    deadline_default = "RC_CAPACITY_DEADLINE_SECONDS=${RC_CAPACITY_DEADLINE_SECONDS:-570}"
    run_check = "[ \"$RUN_COUNT\" = \"500\" ] || exit 1"
    raise "RUN_COUNT and deadline defaults must be the only assignments" unless lines.grep(/^(?:export[[:space:]]+)?RUN_COUNT=/) == [run_default] && lines.grep(/^(?:export[[:space:]]+)?RC_CAPACITY_DEADLINE_SECONDS=/) == [deadline_default]
    raise "RUN_COUNT must only accept 500 once before workload" unless lines.count(run_check) == 1 && lines.index(run_default) < lines.index(run_check) && lines.index(run_check) < up_index
    deadline_start = lines.index("validate_deadline() {")
    deadline_end = deadline_start && lines[(deadline_start + 1)..].index("}")
    raise "deadline validator missing" unless deadline_start && deadline_end
    deadline = lines[deadline_start..deadline_start + deadline_end]
    raise "deadline must reject non-integers directly" unless deadline.any? { |line| line.match?(/^\x27\x27\|\*\[!0-9\]\*\).*\b(?:exit|return) 1/) }
    raise "deadline lower bound must fail directly" unless deadline.any? { |line| line.include?("[ \"$RC_CAPACITY_DEADLINE_SECONDS\" -lt 60 ]") && line.match?(/\b(?:exit|return) 1/) }
    raise "deadline upper bound must fail directly" unless deadline.any? { |line| line.include?("[ \"$RC_CAPACITY_DEADLINE_SECONDS\" -gt 570 ]") && line.match?(/\b(?:exit|return) 1/) }
    raise "deadline validation cannot be neutralized" if deadline.any? { |line| line.include?("|| true") || line.include?("|| :") }
    deadline_call = "validate_deadline \"$RC_CAPACITY_DEADLINE_SECONDS\""
    raise "deadline default/call order invalid" unless lines.count(deadline_default) == 1 && lines.count(deadline_call) == 1 && lines.index(deadline_default) < lines.index(deadline_call) && lines.index(deadline_call) < up_index

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

validate_runtime_isolation_contract() {
  ruby -e '
    source = File.read(ARGV.fetch(0))
    required = [
      "command_start_epoch_ms=",
      "total_timeout_ms=590000",
      "cleanup_reserve_ms=25000",
      "total_deadline_ms=",
      "cleanup_start_deadline_ms=",
      "run_bounded()",
      "pgroup: true",
      "scan_sensitive_log()",
      "capacity-private-",
      "chmod 600 \"$sensitive_values_file\"",
      "request_key=$(ruby -rsecurerandom -e",
      "topic=\"capacity-private-input-$index\"",
      "printf \x27%s\\n\x27 \"$request_key\" \"$topic\" >>\"$sensitive_values_file\"",
      "deadline_epoch_ms=$(min_epoch_ms \"$((start_epoch_ms + RC_CAPACITY_DEADLINE_SECONDS * 1000))\" \"$cleanup_start_deadline_ms\")",
      "run_bounded \"$curl_deadline_ms\" curl",
      "run_bounded \"$db_deadline_ms\" docker compose",
      "run_bounded \"$cleanup_start_deadline_ms\" docker build",
      "run_bounded \"$cleanup_start_deadline_ms\" docker compose",
      "run_bounded \"$logs_deadline_ms\" docker compose",
      "run_bounded \"$down_deadline_ms\" docker compose",
      "run_bounded \"$image_cleanup_deadline_ms\" docker image rm \"$test_image\"",
    ]
    required.each { |marker| raise "capacity runtime isolation missing: #{marker}" unless source.include?(marker) }
    tag = source.lines.find { |line| line.start_with?("test_image=") }
    raise "capacity image tag must be unique per run" unless tag == "test_image=\"agent-studio:rc-capacity-e2e-$$\"\n"
    start_index = source.index("command_start_epoch_ms=")
    project_index = source.index("COMPOSE_PROJECT_NAME=")
    build_index = source.index("docker build")
    cleanup_start = source.index("cleanup() {")
    cleanup_end = source.index("\non_hup() {", cleanup_start)
    cleanup = source[cleanup_start...cleanup_end]
    summary_index = cleanup.index(%q(if [ "$summary_persisted" -eq 0 ]))
    logs_index = cleanup.index("capture_failure_logs")
    down_index = cleanup.index(%q(run_bounded "$down_deadline_ms" docker compose))
    image_index = cleanup.index(%q(run_bounded "$image_cleanup_deadline_ms" docker image rm "$test_image"))
    raise "capacity command clock must be recorded before project setup" unless start_index && project_index && start_index < project_index
    raise "capacity total deadline must be established before build" unless build_index && start_index < build_index
    raise "capacity cleanup phases must preserve summary/log/down/image order" unless summary_index && logs_index && down_index && image_index && summary_index < logs_index && logs_index < down_index && down_index < image_index
    raise "capacity image cleanup must target exactly one unique tag" unless source.lines.count { |line| line.include?(%q(docker image rm "$test_image")) } == 1
    raise "capacity scanner must be fail-closed for prompt and structured payloads" unless source.include?("authorization") && source.include?("ciphertext") && source.include?("(?:input|output)") && source.include?("/prompt/i") && source.include?(%q(case "$scan_status"))
  ' "$1"
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
cleanup() {
  status=$1
  docker compose -f "$compose_file" down --remove-orphans >/dev/null 2>&1 || true
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
run_root=$(mktemp -d "${TMPDIR:-/tmp}/agent-studio-rc-capacity.XXXXXX")
trap 'status=$?; cleanup "$status"' EXIT
trap on_hup HUP
trap on_int INT
trap on_term TERM
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
curl_budget_probe() {
  curl --connect-timeout 1 --max-time 1 "$@"
}
db_budget_probe() {
  PGOPTIONS="-c statement_timeout=1ms -c lock_timeout=1ms" :
}
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
  for case_name in duplicate_image duplicate_up ansi_up assertion_after_contract contract_after_up worker_port no_cleanup late_trap defaults_reassign exported_defaults_reassign deadline_noop fixed_project reassign_project comment_api_port summary_or_true summary_rewrite brace_overwrite semicolon_down single_quoted_variable run_root_rebind; do
    candidate="$fixture_root/$case_name.sh"
    cat "$base" >"$candidate"
    case "$case_name" in
      duplicate_image) printf '%s\n' 'docker build -t "$test_image" "$repo_root"' >>"$candidate"; expected='built exactly once';;
      duplicate_up) printf '%s\n' 'docker compose -f "$compose_file" up -d db api worker' >>"$candidate"; expected='start db/api/worker exactly once';;
      ansi_up) printf '%s\n' 'docker compose --ansi never -f "$compose_file" up -d db api worker' >>"$candidate"; expected='start db/api/worker exactly once';;
      assertion_after_contract) ruby -e 'p=ARGV[0]; s=File.read(p); a="[ \"$actual_services\" = \"api db worker \" ] || exit 1\n"; File.write(p, s.sub(a, "").sub("if [ \"${RC_CAPACITY_CONTRACT_ONLY:-}\" = \"1\" ]; then exit 0; fi\n", "if [ \"${RC_CAPACITY_CONTRACT_ONLY:-}\" = \"1\" ]; then exit 0; fi\n#{a}"))' "$candidate"; expected='Compose verification order';;
      contract_after_up) ruby -e 'p=ARGV[0]; s=File.read(p); c="if [ \"${RC_CAPACITY_CONTRACT_ONLY:-}\" = \"1\" ]; then exit 0; fi\n"; File.write(p, s.sub(c, "").sub("docker compose -f \"$compose_file\" up -d db api worker\n", "docker compose -f \"$compose_file\" up -d db api worker\n#{c}"))' "$candidate"; expected='Compose verification order';;
      worker_port) ruby -e 'p=ARGV[0]; s=File.read(p); File.write(p, s.sub("    environment:\n", "    ports:\n      - \"127.0.0.1:$worker_port:8080\"\n    environment:\n"))' "$candidate"; expected='Worker must not expose ports';;
      no_cleanup) ruby -e 'p=ARGV[0]; s=File.read(p); File.write(p, s.sub("  docker compose -f \"$compose_file\" down --remove-orphans >/dev/null 2>&1 || true", "  :"))' "$candidate"; expected='cleanup must remove only this Compose project';;
      late_trap) ruby -e 'p=ARGV[0]; s=File.read(p); t="trap \x27status=$?; cleanup \"$status\"\x27 EXIT\ntrap on_hup HUP\ntrap on_int INT\ntrap on_term TERM\n"; File.write(p, s.sub(t, "").sub("docker compose -f \"$compose_file\" up -d db api worker\n", "docker compose -f \"$compose_file\" up -d db api worker\n#{t}"))' "$candidate"; expected='immediately follow';;
      defaults_reassign) ruby -e 'p=ARGV[0]; s=File.read(p); marker="[ \"$RUN_COUNT\" = \"500\" ] || exit 1\n"; injection="RUN_COUNT=1\nRC_CAPACITY_DEADLINE_SECONDS=60\nvalidate_deadline \"$RC_CAPACITY_DEADLINE_SECONDS\"\n"; File.write(p, s.sub(marker, marker + injection))' "$candidate"; expected='defaults must be the only assignments';;
      exported_defaults_reassign) ruby -e 'p=ARGV[0]; s=File.read(p); marker="[ \"$RUN_COUNT\" = \"500\" ] || exit 1\n"; injection="export   RUN_COUNT=1\nexport\tRC_CAPACITY_DEADLINE_SECONDS=60\nvalidate_deadline \"$RC_CAPACITY_DEADLINE_SECONDS\"\n"; File.write(p, s.sub(marker, marker + injection))' "$candidate"; expected='defaults must be the only assignments';;
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

extract_production_functions() {
  ruby -e '
    source = File.read(ARGV.shift)

    def extract_function(source, name)
      match = source.match(/^#{Regexp.escape(name)}\(\) \{[[:space:]]*$/)
      raise "production function missing: #{name}" unless match
      start = match.begin(0)
      depth = 0
      quote = nil
      escaped = false
      comment = false
      started = false
      cursor = start
      while cursor < source.length
        character = source[cursor]
        if comment
          comment = false if character == "\n"
        elsif quote
          if quote == 34.chr && character == 92.chr && !escaped
            escaped = true
          elsif character == quote && !escaped
            quote = nil
          else
            escaped = false
          end
        elsif character == 35.chr
          comment = true
        elsif character == 39.chr || character == 34.chr
          quote = character
          escaped = false
        elsif character == "{"
          depth += 1
          started = true
        elsif character == "}"
          depth -= 1
          return source[start..cursor] + "\n" if started && depth.zero?
          raise "unbalanced production function: #{name}" if depth.negative?
        end
        cursor += 1
      end
      raise "unterminated production function: #{name}"
    end

    ARGV.each { |name| STDOUT.write(extract_function(source, name)) }
  ' "$script" "$@"
}

extract_success_metrics_path() {
  ruby -e '
    source = File.read(ARGV.fetch(0))
    match = source.match(/^wait_for_completion\n(refresh_metrics_(?:strict|best_effort))\n/)
    raise "production success metrics path missing" unless match
    puts "wait_for_completion"
    puts match[1]
  ' "$script"
}

run_failure_semantics_test() {
  fixture_root=$(mktemp -d "${TMPDIR:-/tmp}/agent-studio-rc-capacity-semantics.XXXXXX")
  trap 'rm -rf "$fixture_root"' EXIT HUP INT TERM
  strict_definitions="$fixture_root/strict-definitions.sh"
  deadline_definitions="$fixture_root/deadline-definitions.sh"
  signal_definitions="$fixture_root/signal-definitions.sh"
  success_path="$fixture_root/success-path.sh"
  extract_production_functions refresh_metrics_strict >"$strict_definitions"
  extract_production_functions validate_elapsed >"$deadline_definitions"
  extract_production_functions cleanup on_hup on_int on_term >"$signal_definitions"
  extract_success_metrics_path >"$success_path"

  strict_harness="$fixture_root/strict-harness.sh"
  cat >"$strict_harness" <<'HARNESS'
#!/bin/sh
set -eu
. "$1"
run_ids_sql="'00000000-0000-4000-8000-000000000001'"
db_scalar() {
  case "$1" in
    *'lease_owner IS NOT NULL'*) return 42 ;;
    *) printf '%s\n' 0 ;;
  esac
}
set +e
refresh_metrics_strict
status=$?
set -e
[ "$status" -eq 42 ] || {
  printf '%s\n' "strict metrics returned $status instead of propagating 42" >&2
  exit 1
}
HARNESS
  sh "$strict_harness" "$strict_definitions"

  success_harness="$fixture_root/success-harness.sh"
  cat >"$success_harness" <<'HARNESS'
#!/bin/sh
set -eu
trace_file=$2
wait_for_completion() { :; }
refresh_metrics_strict() { printf '%s\n' strict >>"$trace_file"; }
refresh_metrics_best_effort() { printf '%s\n' best-effort >>"$trace_file"; }
. "$1"
[ "$(cat "$trace_file")" = strict ] || {
  printf '%s\n' 'production success path did not call strict metrics exclusively' >&2
  exit 1
}
HARNESS
  sh "$success_harness" "$success_path" "$fixture_root/success-trace"

  deadline_harness="$fixture_root/deadline-harness.sh"
  cat >"$deadline_harness" <<'HARNESS'
#!/bin/sh
set -eu
. "$1"
RC_CAPACITY_DEADLINE_SECONDS=570
elapsed_ms=570000
validate_elapsed || {
  printf '%s\n' 'deadline rejected 570000ms' >&2
  exit 1
}
elapsed_ms=570001
if validate_elapsed; then
  printf '%s\n' 'deadline accepted 570001ms' >&2
  exit 1
fi
HARNESS
  sh "$deadline_harness" "$deadline_definitions"

  signal_harness="$fixture_root/signal-harness.sh"
  cat >"$signal_harness" <<'HARNESS'
#!/bin/sh
set -eu
. "$1"
mode=$2
cleanup_count_file=$3
failure_marker=$4
ready_marker=$5
run_root=/fixture/run-root
artifact_dir=/fixture/artifacts
summary_file=/fixture/summary.json
compose_file=/fixture/compose.yaml
cleanup_in_progress=0
summary_persisted=1
compose_started=1
started_workload=0
image_cleanup_required=0
run_ids_sql=
logs_deadline_ms=1000
down_deadline_ms=1000
image_cleanup_deadline_ms=1000
now_epoch_ms() { printf '%s\n' 1; }
min_epoch_ms() { printf '%s\n' "$1"; }
run_bounded() { shift; "$@"; }
refresh_metrics_best_effort() { :; }
capture_failure_logs() { printf '%s\n' failure >"$failure_marker"; }
docker() { :; }
rm() { printf '%s\n' cleanup >>"$cleanup_count_file"; }
case "$mode" in
  term)
    trap 'status=$?; cleanup "$status"' EXIT
    trap on_hup HUP
    trap on_int INT
    trap on_term TERM
    printf '%s\n' ready >"$ready_marker"
    sleep 2
    ;;
  hup) on_hup ;;
  int) on_int ;;
  *) exit 2 ;;
esac
HARNESS

  term_count="$fixture_root/term-count"
  term_failure="$fixture_root/term-failure"
  term_ready="$fixture_root/term-ready"
  : >"$term_count"
  sh "$signal_harness" "$signal_definitions" term "$term_count" "$term_failure" "$term_ready" &
  signal_pid=$!
  attempts=0
  while [ ! -f "$term_ready" ] && [ "$attempts" -lt 50 ]; do
    sleep 0.02
    attempts=$((attempts + 1))
  done
  [ -f "$term_ready" ] || {
    kill "$signal_pid" 2>/dev/null || true
    wait "$signal_pid" 2>/dev/null || true
    printf '%s\n' 'TERM fixture did not become ready' >&2
    exit 1
  }
  kill -TERM "$signal_pid"
  set +e
  wait "$signal_pid"
  signal_status=$?
  set -e
  [ "$signal_status" -eq 143 ] || {
    printf '%s\n' "TERM fixture exited $signal_status instead of 143" >&2
    exit 1
  }
  [ "$(wc -l <"$term_count" | tr -d ' ')" -eq 1 ] || {
    printf '%s\n' 'TERM fixture cleanup count was not exactly one' >&2
    exit 1
  }
  [ "$(cat "$term_failure")" = failure ] || {
    printf '%s\n' 'TERM fixture did not persist failure evidence' >&2
    exit 1
  }

  for signal_case in hup:129 int:130; do
    signal_name=${signal_case%%:*}
    expected_status=${signal_case##*:}
    signal_count="$fixture_root/$signal_name-count"
    signal_failure="$fixture_root/$signal_name-failure"
    : >"$signal_count"
    set +e
    sh "$signal_harness" "$signal_definitions" "$signal_name" "$signal_count" "$signal_failure" "$fixture_root/$signal_name-ready"
    signal_status=$?
    set -e
    [ "$signal_status" -eq "$expected_status" ] || {
      printf '%s\n' "$signal_name fixture exited $signal_status instead of $expected_status" >&2
      exit 1
    }
    [ "$(wc -l <"$signal_count" | tr -d ' ')" -eq 1 ] || {
      printf '%s\n' "$signal_name fixture cleanup count was not exactly one" >&2
      exit 1
    }
    [ "$(cat "$signal_failure")" = failure ] || {
      printf '%s\n' "$signal_name fixture did not persist failure evidence" >&2
      exit 1
    }
  done

  trap - EXIT HUP INT TERM
  rm -rf "$fixture_root"
}

run_runtime_isolation_test() {
  fixture_root=$(mktemp -d "${TMPDIR:-/tmp}/agent-studio-rc-capacity-runtime.XXXXXX")
  trap 'rm -rf "$fixture_root"' EXIT HUP INT TERM

  scanner_definitions="$fixture_root/scanner-definitions.sh"
  extract_production_functions scan_sensitive_log >"$scanner_definitions"
  scanner_harness="$fixture_root/scanner-harness.sh"
  cat >"$scanner_harness" <<'HARNESS'
#!/bin/sh
set -eu
. "$1"
sensitive_values_file=$3
export RUN_PAYLOAD_ENCRYPTION_KEY='fixture-private-key'
set +e
scan_sensitive_log "$2" 2>/dev/null
scan_status=$?
set -e
[ "$scan_status" -eq "$4" ] || {
  printf '%s\n' "scanner returned $scan_status instead of $4 for $2" >&2
  exit 1
}
HARNESS
  known_values="$fixture_root/known-values"
  printf '%s\n' 'capacity-private-input-' 'capacity-private-key-' 'capacity-private-prompt' >"$known_values"
  chmod 600 "$known_values"
  printf '%s\n' 'ordinary worker shutdown line' >"$fixture_root/safe.log"
  sh "$scanner_harness" "$scanner_definitions" "$fixture_root/safe.log" "$known_values" 1
  scanner_case=1
  for sensitive_line in 'fixture-private-key' 'Authorization: bearer hidden' 'ciphertext=value' 'input: private' 'output=private' 'prompt private' 'capacity-private-input-42'; do
    printf '%s\n' "$sensitive_line" >"$fixture_root/sensitive-$scanner_case.log"
    sh "$scanner_harness" "$scanner_definitions" "$fixture_root/sensitive-$scanner_case.log" "$known_values" 0
    scanner_case=$((scanner_case + 1))
  done
  sh "$scanner_harness" "$scanner_definitions" "$fixture_root/missing.log" "$known_values" 2

  fake_bin="$fixture_root/bin"
  mkdir -p "$fake_bin"
  fake_docker="$fake_bin/docker"
  cat >"$fake_docker" <<'HARNESS'
#!/bin/sh
set -eu
printf '%s|%s\n' "${test_image:-unset}" "$*" >>"$FAKE_DOCKER_LOG"
case "${FAKE_DOCKER_MODE:-contract}:$*" in
  contract:*'config --services'*) printf '%s\n' worker db api ;;
  slow:*'docker build'*|slow:build*) exec sleep 2 ;;
  slow:*' logs '*|slow:*' logs') exec sleep 2 ;;
  *) : ;;
esac
HARNESS
  chmod 700 "$fake_docker"

  fixed_tag_marker="$fixture_root/preexisting-fixed-tag"
  printf '%s\n' 'agent-studio:rc-capacity-e2e' >"$fixed_tag_marker"
  contract_log="$fixture_root/contract-docker.log"
  : >"$contract_log"
  FAKE_DOCKER_MODE=contract FAKE_DOCKER_LOG="$contract_log" PATH="$fake_bin:$PATH" RC_CAPACITY_ARTIFACT_DIR="$fixture_root/artifact-one" RC_CAPACITY_CONTRACT_ONLY=1 RUN_PAYLOAD_ENCRYPTION_KEY='fixture-key-one' sh "$script" &
  contract_pid_one=$!
  FAKE_DOCKER_MODE=contract FAKE_DOCKER_LOG="$contract_log" PATH="$fake_bin:$PATH" RC_CAPACITY_ARTIFACT_DIR="$fixture_root/artifact-two" RC_CAPACITY_CONTRACT_ONLY=1 RUN_PAYLOAD_ENCRYPTION_KEY='fixture-key-two' sh "$script" &
  contract_pid_two=$!
  wait "$contract_pid_one"
  wait "$contract_pid_two"
  ruby -e '
    fixed = ARGV.fetch(0)
    entries = File.readlines(ARGV.fetch(1), chomp: true)
    tags = entries.map { |line| line.split("|", 2).first }.grep(/\Aagent-studio:rc-capacity-e2e-[0-9]+\z/).uniq
    raise "concurrent contract-only runs did not use two unique image tags" unless tags.length == 2
    raise "contract-only referenced the pre-existing fixed tag" if entries.any? { |line| line.split("|", 2).first == fixed }
  ' "$(cat "$fixed_tag_marker")" "$contract_log"
  [ "$(cat "$fixed_tag_marker")" = 'agent-studio:rc-capacity-e2e' ] || exit 1

  budget_definitions="$fixture_root/budget-definitions.sh"
  extract_production_functions now_epoch_ms min_epoch_ms run_bounded scan_sensitive_log withhold_failure_log capture_failure_logs cleanup >"$budget_definitions"
  budget_harness="$fixture_root/budget-harness.sh"
  cat >"$budget_harness" <<'HARNESS'
#!/bin/sh
set -eu
. "$1"
fixture_root=$2
fake_bin=$3
export PATH="$fake_bin:$PATH"
export FAKE_DOCKER_MODE=slow
export FAKE_DOCKER_LOG="$fixture_root/budget-docker.log"
export RUN_PAYLOAD_ENCRYPTION_KEY='budget-private-key'
run_root="$fixture_root/transient"
artifact_dir="$fixture_root/budget-artifacts"
summary_file="$run_root/summary.json"
compose_file="$run_root/compose.yaml"
sensitive_values_file="$run_root/sensitive-values"
mkdir -p "$run_root" "$artifact_dir"
printf '%s\n' 'capacity-private-' >"$sensitive_values_file"
cleanup_in_progress=0
summary_persisted=0
compose_started=1
image_cleanup_required=1
started_workload=0
run_ids_sql=
test_image='agent-studio:rc-capacity-e2e-fixture-unique'
export test_image
now_ms=$(now_epoch_ms)
cleanup_start_deadline_ms=$((now_ms + 250))
logs_deadline_ms=$((now_ms + 550))
down_deadline_ms=$((now_ms + 850))
image_cleanup_deadline_ms=$((now_ms + 1100))
refresh_metrics_best_effort() { :; }
write_summary() { printf '%s\n' summary >"$summary_file"; }
persist_summary() { printf '%s\n' persisted >"$fixture_root/summary-persisted"; }
set +e
run_bounded "$cleanup_start_deadline_ms" docker build -t "$test_image" .
build_status=$?
set -e
[ "$build_status" -eq 124 ] || exit 1
cleanup "$build_status"
HARNESS
  budget_started_ms=$(ruby -e 'puts (Time.now.to_f * 1000).to_i')
  set +e
  sh "$budget_harness" "$budget_definitions" "$fixture_root" "$fake_bin" 2>"$fixture_root/budget-stderr"
  budget_status=$?
  set -e
  budget_elapsed_ms=$(( $(ruby -e 'puts (Time.now.to_f * 1000).to_i') - budget_started_ms ))
  [ "$budget_status" -eq 124 ] || {
    printf '%s\n' "bounded cleanup fixture exited $budget_status instead of 124" >&2
    exit 1
  }
  [ "$budget_elapsed_ms" -lt 2000 ] || {
    printf '%s\n' "bounded cleanup fixture took ${budget_elapsed_ms}ms" >&2
    exit 1
  }
  [ "$(cat "$fixture_root/summary-persisted")" = persisted ] || exit 1
  [ -f "$fixture_root/budget-artifacts/redaction.log" ] || {
    printf '%s\n' 'hung log collection did not create withheld marker' >&2
    exit 1
  }
  ruby -e '
    entries = File.readlines(ARGV.fetch(0), chomp: true)
    raise "hung logs prevented compose down" unless entries.any? { |line| line.end_with?("|compose -f #{ARGV.fetch(1)} down --remove-orphans") }
    expected_rm = "agent-studio:rc-capacity-e2e-fixture-unique|image rm agent-studio:rc-capacity-e2e-fixture-unique"
    raise "cleanup did not remove the exact unique image tag" unless entries.include?(expected_rm)
    raise "cleanup referenced the old fixed tag" if entries.any? { |line| line.start_with?("agent-studio:rc-capacity-e2e|") }
  ' "$fixture_root/budget-docker.log" "$fixture_root/transient/compose.yaml"

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
validate_runtime_isolation_contract "$script"
run_failure_semantics_test
run_runtime_isolation_test

ruby -ryaml -e '
  workflow = YAML.safe_load(File.read(ARGV.fetch(0)), aliases: true)
  job = workflow.fetch("jobs").fetch("rc-capacity")
  raise "rc capacity job cannot be conditional or continue on error" unless !job.key?("if") && (!job.key?("continue-on-error") || job["continue-on-error"] == false)
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
