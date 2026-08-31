#!/bin/sh
set -eu

# A missing URL must fail before Go is invoked; a wrapper change that omits this
# guard could otherwise run against a developer's ambient database settings.
test_root=$(mktemp -d "${TMPDIR:-/tmp}/agent-studio-backup-e2e-wrapper.XXXXXX")
trap 'rm -rf "$test_root"' EXIT HUP INT TERM

wrapper="$test_root/test-backup-e2e.sh"
cp scripts/test-backup-e2e.sh "$wrapper"
chmod +x "$wrapper"

set +e
output=$(env -u TEST_DATABASE_URL PATH="$test_root/bin:$PATH" sh "$wrapper" 2>&1)
status=$?
set -e
[ "$status" -eq 2 ]
case "$output" in
  *"TEST_DATABASE_URL is required"*) ;;
  *)
    echo "wrapper did not report missing TEST_DATABASE_URL" >&2
    exit 1
    ;;
esac

if grep -E 'down[[:space:]]+-v|DROP[[:space:]]+DATABASE|docker[[:space:]]+volume[[:space:]]+rm' scripts/test-backup-e2e.sh >/dev/null; then
  echo "backup E2E wrapper contains a destructive command" >&2
  exit 1
fi

mkdir "$test_root/bin"
cat >"$test_root/bin/go" <<'EOF'
#!/bin/sh
set -eu
printf 'BACKUP_E2E=%s\n' "$BACKUP_E2E"
printf 'TEST_DATABASE_URL=%s\n' "$TEST_DATABASE_URL"
printf 'CGO_ENABLED=%s\n' "$CGO_ENABLED"
printf 'ARGS='
printf '%s ' "$@"
printf '\n'
exit "${FAKE_GO_STATUS:-0}"
EOF
chmod +x "$test_root/bin/go"

test_url='postgres://agent:agent@localhost:5432/agent_studio?sslmode=disable'
set +e
output=$(PATH="$test_root/bin:$PATH" TEST_DATABASE_URL="$test_url" FAKE_GO_STATUS=17 sh "$wrapper" 2>&1)
status=$?
set -e
[ "$status" -eq 17 ]
case "$output" in
  *"BACKUP_E2E=1"*"TEST_DATABASE_URL=$test_url"*"CGO_ENABLED=0"*"ARGS=test ./internal/backup -run ^TestBackupRestoreE2E$ -count=1 -v "*) ;;
  *)
    echo "wrapper did not forward the required Go test invocation: $output" >&2
    exit 1
    ;;
esac
