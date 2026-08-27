#!/bin/sh
set -eu

verify_dir=$(mktemp -d)
api_pid=""

cleanup() {
  if [ -n "$api_pid" ]; then
    kill "$api_pid" 2>/dev/null || true
    wait "$api_pid" 2>/dev/null || true
  fi
  make observability-down >/dev/null 2>&1 || true
  rm -rf "$verify_dir"
}
trap cleanup EXIT HUP INT TERM

wait_for_url() {
  service_name=$1
  health_url=$2
  attempts=0
  while [ "$attempts" -lt 60 ]; do
    if curl -fsS --max-time 2 -o /dev/null "$health_url"; then
      return 0
    fi
    attempts=$((attempts + 1))
    sleep 1
  done
  echo "$service_name: health_timeout" >&2
  return 1
}

create_and_run() {
  label=$1
  source=$2
  expected_terminal=$3
  slug="observability-$label-$(date +%s)-$$"

  ruby -rjson -e 'puts JSON.generate(name: ARGV.fetch(0), slug: ARGV.fetch(1), description: "observability smoke")' \
    "Observability $label" "$slug" >"$verify_dir/create-request.json"
  curl -fsS -H 'Content-Type: application/json' --data-binary @"$verify_dir/create-request.json" \
    http://127.0.0.1:18080/api/workflows >"$verify_dir/create-response.json"
  workflow_id=$(ruby -rjson -e 'puts JSON.parse(File.read(ARGV.fetch(0))).fetch("id")' "$verify_dir/create-response.json")
  draft_revision=$(ruby -rjson -e 'puts JSON.parse(File.read(ARGV.fetch(0))).fetch("draftRevision")' "$verify_dir/create-response.json")

  ruby -rjson -e '
    source = ARGV.fetch(0)
    revision = Integer(ARGV.fetch(1))
    graph = {
      schemaVersion: 1,
      nodes: [
        {id: "start", type: "start", typeVersion: "1", position: {x: 100, y: 100}, config: {fields: [{key: "payload", label: "Payload", type: "text", required: true}]}},
        {id: "code", type: "code", typeVersion: "1", position: {x: 350, y: 100}, config: {source: source}},
        {id: "end", type: "end", typeVersion: "1", position: {x: 600, y: 100}, config: {}}
      ],
      edges: [
        {id: "edge-start-code", source: "start", sourcePort: "payload", target: "code", targetPort: "input"},
        {id: "edge-code-end", source: "code", sourcePort: "result", target: "end", targetPort: "result"}
      ]
    }
    puts JSON.generate(draftRevision: revision, graph: graph)
  ' "$source" "$draft_revision" >"$verify_dir/save-request.json"
  curl -fsS -X PUT -H 'Content-Type: application/json' --data-binary @"$verify_dir/save-request.json" \
    "http://127.0.0.1:18080/api/workflows/$workflow_id" >"$verify_dir/save-response.json"
  saved_revision=$(ruby -rjson -e 'puts JSON.parse(File.read(ARGV.fetch(0))).fetch("draftRevision")' "$verify_dir/save-response.json")

  ruby -rjson -e 'puts JSON.generate(draftRevision: Integer(ARGV.fetch(0)), input: {payload: "hello"})' \
    "$saved_revision" >"$verify_dir/run-request.json"
  curl -fsS -H 'Content-Type: application/json' --data-binary @"$verify_dir/run-request.json" \
    "http://127.0.0.1:18080/api/workflows/$workflow_id/test-runs" >"$verify_dir/run-$label.ndjson"
  ruby -rjson -e '
    events = File.readlines(ARGV.fetch(0), chomp: true).reject(&:empty?).map { |line| JSON.parse(line) }
    expected = ARGV.fetch(1)
    raise "terminal event missing" unless events.any? { |event| event["type"] == expected }
    first_id = events.map { |event| event["runId"] }.compact.first
    raise "run id missing" if first_id.nil? || first_id.empty?
    puts first_id
  ' "$verify_dir/run-$label.ndjson" "$expected_terminal"
}

wait_for_prometheus_metric() {
  metric_name=$1
  output_file=$2
  attempts=0
  while [ "$attempts" -lt 60 ]; do
    if curl -fsS --get --data-urlencode "query=$metric_name" http://127.0.0.1:9090/api/v1/query >"$output_file" && \
      ruby -rjson -e '
        data = JSON.parse(File.read(ARGV.fetch(0))).fetch("data").fetch("result")
        exit(data.any? { |row| row.fetch("value").fetch(1).to_f > 0 } ? 0 : 1)
      ' "$output_file"; then
      return 0
    fi
    attempts=$((attempts + 1))
    sleep 1
  done
  echo "prometheus: metric_timeout" >&2
  return 1
}

wait_for_jaeger_traces() {
  attempts=0
  while [ "$attempts" -lt 60 ]; do
    if curl -fsS http://127.0.0.1:16686/api/services >"$verify_dir/jaeger-services.json" && \
      ruby -rjson -e 'exit(JSON.parse(File.read(ARGV.fetch(0))).fetch("data", []).include?(ARGV.fetch(1)) ? 0 : 1)' \
        "$verify_dir/jaeger-services.json" agent-studio-observability-smoke && \
      curl -fsS --get --data-urlencode 'service=agent-studio-observability-smoke' --data-urlencode 'limit=100' \
        http://127.0.0.1:16686/api/traces >"$verify_dir/jaeger-traces.json" && \
      ruby -rjson -e '
        text = File.read(ARGV.fetch(0))
        document = JSON.parse(text)
        names = document.fetch("data", []).flat_map { |trace| trace.fetch("spans", []).map { |span| span["operationName"] } }.compact
        required = ["workflow.run", "workflow.node"]
        exit(required.all? { |name| names.include?(name) } && names.any? { |name| name.start_with?("HTTP POST") } ? 0 : 1)
      ' "$verify_dir/jaeger-traces.json"; then
      return 0
    fi
    attempts=$((attempts + 1))
    sleep 1
  done
  echo "jaeger: trace_timeout" >&2
  return 1
}

make db-up
make observability-up

HTTP_ADDR=127.0.0.1:18080 \
OTEL_EXPORTER_OTLP_ENDPOINT=http://127.0.0.1:4318 \
OTEL_SERVICE_NAME=agent-studio-observability-smoke \
OTEL_METRIC_EXPORT_INTERVAL=1000 \
OTEL_EXPORTER_OTLP_TIMEOUT=1000 \
CGO_ENABLED=0 go run ./apps/api/cmd/server >"$verify_dir/api.log" 2>&1 &
api_pid=$!
wait_for_url api http://127.0.0.1:18080/readyz

create_and_run success 'def main(input): return "observability-ok"' run.completed >/dev/null
create_and_run failure 'def main(input): fail("observability-sensitive-sentinel")' run.failed >/dev/null

wait_for_prometheus_metric agent_studio_workflow_runs_total "$verify_dir/prometheus-runs.json"
wait_for_prometheus_metric agent_studio_http_server_requests_total "$verify_dir/prometheus-http.json"
wait_for_jaeger_traces

if grep -R 'observability-sensitive-sentinel' "$verify_dir/api.log" "$verify_dir/prometheus-runs.json" "$verify_dir/prometheus-http.json" "$verify_dir/jaeger-services.json" "$verify_dir/jaeger-traces.json" >/dev/null; then
  echo "observability: sensitive_sentinel_leaked" >&2
  exit 1
fi

docker compose --profile observability stop otel-collector
create_and_run collector-down 'def main(input): return "collector-down-ok"' run.completed >/dev/null
docker compose ps --status running db | grep -q 'db'

echo "observability verification passed"
