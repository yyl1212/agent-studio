#!/bin/sh
set -eu

config=apps/web/playwright.config.ts
runner=apps/web/e2e/fixtures/run-e2e-runtime.sh

require_text() {
  file=$1
  pattern=$2
  [ -f "$file" ] || { printf '%s\n' "missing required file: $file" >&2; exit 1; }
  grep -F "$pattern" "$file" >/dev/null || {
    printf '%s\n' "missing required text in $file: $pattern" >&2
    exit 1
  }
}

require_text "$config" 'run-e2e-runtime.sh'
require_text "$runner" './cmd/server'
require_text "$runner" './cmd/worker'
require_text "$runner" '"$run_root/agent-studio-api" &'
require_text "$runner" '"$run_root/agent-studio-worker" &'
require_text "$runner" 'RUN_PAYLOAD_ENCRYPTION_KEY'
require_text "$runner" 'trap cleanup EXIT HUP INT TERM'

if grep -E 'RUN_PAYLOAD_ENCRYPTION_KEY=[A-Za-z0-9+/]{40,}={0,2}' "$runner" >/dev/null; then
  printf '%s\n' 'Playwright runtime runner must not contain a hard-coded encryption key' >&2
  exit 1
fi

printf '%s\n' 'Playwright durable worker contract passed'
