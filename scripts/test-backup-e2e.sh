#!/bin/sh
set -eu

if [ -z "${TEST_DATABASE_URL:-}" ]; then
  printf '%s\n' 'TEST_DATABASE_URL is required' >&2
  exit 2
fi
if [ -z "${RUN_PAYLOAD_ENCRYPTION_KEY:-}" ]; then
  printf '%s\n' 'RUN_PAYLOAD_ENCRYPTION_KEY is required' >&2
  exit 2
fi

BACKUP_E2E=1 TEST_DATABASE_URL="$TEST_DATABASE_URL" RUN_PAYLOAD_ENCRYPTION_KEY="$RUN_PAYLOAD_ENCRYPTION_KEY" CGO_ENABLED=0 \
  go test ./internal/backup -run '^(TestBackupRestoreE2E|TestCurrentRuntimeRestoresV1Alpha1GoldenArchive|TestCurrentRuntimeRestoresV1Alpha2GoldenArchive)$' -count=1 -v
