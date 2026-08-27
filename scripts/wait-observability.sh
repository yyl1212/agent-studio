#!/bin/sh
set -eu

wait_for_service() {
  service_name=$1
  health_url=$2
  attempts=0
  while [ "$attempts" -lt 60 ]; do
    if curl -fsS --max-time 2 -o /dev/null "$health_url"; then
      echo "$service_name ready"
      return 0
    fi
    attempts=$((attempts + 1))
    sleep 1
  done
  echo "$service_name: health_timeout" >&2
  docker compose --profile observability logs --tail=100 "$service_name" >&2
  return 1
}

wait_for_service otel-collector http://127.0.0.1:13133/
wait_for_service prometheus http://127.0.0.1:9090/-/ready
wait_for_service jaeger http://127.0.0.1:16686/api/services
