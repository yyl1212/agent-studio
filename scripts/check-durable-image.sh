#!/bin/sh
set -eu

image=${DURABLE_IMAGE_TAG:-agent-studio:durable-check}

docker build --tag "$image" .

user=$(docker image inspect --format '{{.Config.User}}' "$image")
[ "$user" = "10001:10001" ] || {
  printf '%s\n' "image user is $user; want 10001:10001" >&2
  exit 1
}

entrypoint=$(docker image inspect --format '{{join .Config.Entrypoint " "}}' "$image")
[ "$entrypoint" = "/app/agent-studio-api" ] || {
  printf '%s\n' "image entrypoint is $entrypoint; want /app/agent-studio-api" >&2
  exit 1
}

docker run --rm --entrypoint /bin/sh "$image" -c '
  set -eu
  [ "$(id -u)" = 10001 ]
  [ "$HOME" = /home/agentstudio ]
  [ -d "$HOME" ]
  [ -w "$HOME" ]
  [ -x /app/agent-studio-api ]
  [ -x /app/agent-studio-worker ]
  [ -x /app/agent-studio ]
  for binary in /app/agent-studio-api /app/agent-studio-worker /app/agent-studio; do
    if ldd "$binary" >/dev/null 2>&1; then
      printf "%s\n" "$binary is dynamically linked" >&2
      exit 1
    fi
  done
  /app/agent-studio version >/dev/null
'

printf '%s\n' 'durable image check passed'
