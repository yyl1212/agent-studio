#!/bin/sh
set -eu

check_dir=$(mktemp -d)
trap 'rm -rf "$check_dir"' EXIT HUP INT TERM

docker compose --profile observability config --format json >"$check_dir/compose.json"

ruby -rjson -e '
  document = JSON.parse(File.read(ARGV.fetch(0)))
  services = document.fetch("services")
  expected = {
    "otel-collector" => "otel/opentelemetry-collector-contrib:0.159.0",
    "prometheus" => "prom/prometheus:v3.14.0",
    "jaeger" => "cr.jaegertracing.io/jaegertracing/jaeger:2.20.0"
  }
  expected.each do |name, image|
    service = services.fetch(name)
    raise "#{name}: image mismatch" unless service.fetch("image") == image
    raise "#{name}: observability profile missing" unless service.fetch("profiles", []).include?("observability")
    service.fetch("ports", []).each do |port|
      raise "#{name}: published port is not loopback-only" unless port.fetch("host_ip", "") == "127.0.0.1"
    end
    dependencies = service.fetch("depends_on", {})
    raise "#{name}: must not depend on db" if dependencies.key?("db")
    volumes = service.fetch("volumes", [])
    raise "#{name}: must not mount postgres data" if volumes.any? { |volume| volume["source"] == "agent_pg_data" }
  end
' "$check_dir/compose.json"

ruby -e '
  text = File.read(ARGV.fetch(0))
  raise "collector logs pipeline is forbidden" if text.match?(/^\s+logs:\s*$/)
' deploy/observability/otel-collector.yaml

docker compose --profile observability run --rm --no-deps otel-collector validate --config=/etc/otelcol-contrib/config.yaml
echo "observability compose check passed"
