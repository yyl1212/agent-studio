#!/bin/sh
set -eu

if [ -z "${TEST_DATABASE_URL:-}" ]; then
  printf '%s\n' 'TEST_DATABASE_URL is required' >&2
  exit 2
fi

BACKUP_E2E=1 TEST_DATABASE_URL="$TEST_DATABASE_URL" CGO_ENABLED=0 \
  go test ./internal/backup -run '^(TestBackupRestoreE2E|TestCurrentRuntimeRestoresV1Alpha1GoldenArchive)$' -count=1 -v
