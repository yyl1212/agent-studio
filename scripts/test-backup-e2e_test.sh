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
if [ "${FAKE_GO_REPORT_ENV:-0}" = "1" ]; then
  printf 'BACKUP_E2E=%s\n' "$BACKUP_E2E"
  printf 'TEST_DATABASE_URL=%s\n' "$TEST_DATABASE_URL"
  printf 'CGO_ENABLED=%s\n' "$CGO_ENABLED"
  printf 'ARGS='
  printf '%s ' "$@"
  printf '\n'
fi
case "${EXPECTED_MODE:-}" in
  "") ;;
  create)
    [ "$#" -eq 6 ]
    [ "$1" = "run" ] && [ "$2" = "./cmd/agent-studio" ] && [ "$3" = "backup" ]
    [ "$4" = "create" ] && [ "$5" = "--output" ] && [ "$6" = "$EXPECTED_PATH" ]
    [ "$DATABASE_URL" = "$EXPECTED_DATABASE_URL" ]
    ;;
  inspect)
    [ "$#" -eq 5 ]
    [ "$1" = "run" ] && [ "$2" = "./cmd/agent-studio" ] && [ "$3" = "backup" ]
    [ "$4" = "inspect" ] && [ "$5" = "$EXPECTED_PATH" ]
    ;;
  dry-run)
    [ "$#" -eq 6 ]
    [ "$1" = "run" ] && [ "$2" = "./cmd/agent-studio" ] && [ "$3" = "backup" ]
    [ "$4" = "restore" ] && [ "$5" = "--dry-run" ] && [ "$6" = "$EXPECTED_PATH" ]
    [ "$DATABASE_URL" = "$EXPECTED_DATABASE_URL" ]
    ;;
  restore)
    [ "$#" -eq 6 ]
    [ "$1" = "run" ] && [ "$2" = "./cmd/agent-studio" ] && [ "$3" = "backup" ]
    [ "$4" = "restore" ] && [ "$5" = "--confirm-empty-instance" ] && [ "$6" = "$EXPECTED_PATH" ]
    [ "$DATABASE_URL" = "$EXPECTED_DATABASE_URL" ]
    ;;
  no-database-url)
    [ -z "${TEST_DATABASE_URL+x}" ]
    exec "$REAL_GO" "$@"
    ;;
  must-not-run)
    exit 97
    ;;
  *)
    exit 98
    ;;
esac
[ -z "${FAKE_GO_LOG:-}" ] || printf '%s\n' "$EXPECTED_MODE" >>"$FAKE_GO_LOG"
exit "${FAKE_GO_STATUS:-0}"
EOF
chmod +x "$test_root/bin/go"

cat >"$test_root/bin/docker" <<'EOF'
#!/bin/sh
set -eu
[ "$#" -eq 5 ]
[ "$1" = "compose" ] && [ "$2" = "up" ] && [ "$3" = "-d" ] && [ "$4" = "--wait" ] && [ "$5" = "db" ]
EOF
chmod +x "$test_root/bin/docker"

test_url='postgres://agent:agent@localhost:5432/agent_studio?sslmode=disable'
set +e
output=$(PATH="$test_root/bin:$PATH" TEST_DATABASE_URL="$test_url" FAKE_GO_REPORT_ENV=1 FAKE_GO_STATUS=17 sh "$wrapper" 2>&1)
status=$?
set -e
[ "$status" -eq 17 ]
expected_run='ARGS=test ./internal/backup -run ^(TestBackupRestoreE2E|TestCurrentRuntimeRestoresV1Alpha1GoldenArchive)$ -count=1 -v '
case "$output" in
  *"BACKUP_E2E=1"*"TEST_DATABASE_URL=$test_url"*"CGO_ENABLED=0"*"$expected_run"*) ;;
  *)
    echo "wrapper did not forward the required Go test invocation" >&2
    exit 1
    ;;
esac

injection_marker="$test_root/injected"
output_path="output\"; : > \"$injection_marker\"; #.asbak
second-output-line"
backup_path="backup\"; : > \"$injection_marker\"; #.asbak
second-backup-line"
fake_go_log="$test_root/go.log"
: >"$fake_go_log"

run_make_path_test() {
  target=$1
  variable_name=$2
  path_value=$3
  expected_mode=$4
  shift 4
  set +e
  command_output=$(PATH="$test_root/bin:$PATH" \
    EXPECTED_MODE="$expected_mode" \
    EXPECTED_PATH="$path_value" \
    EXPECTED_DATABASE_URL="$test_url" \
    FAKE_GO_LOG="$fake_go_log" \
    make --no-print-directory "$target" "$variable_name=$path_value" "TEST_DATABASE_URL=$test_url" "$@" 2>&1)
  command_status=$?
  set -e
  if [ "$command_status" -ne 0 ]; then
    printf 'safe Make invocation failed for %s\n' "$target" >&2
    exit 1
  fi
  case "$command_output" in
    *"$test_url"*)
      printf 'Make invocation leaked TEST_DATABASE_URL for %s\n' "$target" >&2
      exit 1
      ;;
  esac
  if [ -e "$injection_marker" ]; then
    printf 'Make invocation executed injected shell for %s\n' "$target" >&2
    exit 1
  fi
}

run_make_path_test backup-create OUTPUT "$output_path" create
run_make_path_test backup-inspect BACKUP "$backup_path" inspect
run_make_path_test backup-restore-dry-run BACKUP "$backup_path" dry-run
run_make_path_test backup-restore BACKUP "$backup_path" restore CONFIRM=empty-instance

for expected_mode in create inspect dry-run restore; do
  grep -Fx "$expected_mode" "$fake_go_log" >/dev/null
done
[ "$(wc -l <"$fake_go_log" | tr -d ' ')" -eq 4 ]

bad_confirm="empty-instance\"; : > \"$injection_marker\"; #
second-confirm-line"
set +e
command_output=$(PATH="$test_root/bin:$PATH" \
  EXPECTED_MODE=must-not-run \
  EXPECTED_PATH="$backup_path" \
  EXPECTED_DATABASE_URL="$test_url" \
  FAKE_GO_LOG="$fake_go_log" \
  make --no-print-directory backup-restore "BACKUP=$backup_path" "CONFIRM=$bad_confirm" "TEST_DATABASE_URL=$test_url" 2>&1)
command_status=$?
set -e
[ "$command_status" -eq 2 ]
case "$command_output" in
  *"$test_url"*)
    printf '%s\n' 'rejected restore confirmation leaked TEST_DATABASE_URL' >&2
    exit 1
    ;;
esac
if [ -e "$injection_marker" ]; then
  printf '%s\n' 'restore confirmation executed injected shell' >&2
  exit 1
fi
[ "$(wc -l <"$fake_go_log" | tr -d ' ')" -eq 4 ]

assert_dry_run_safe() {
  set +e
  dry_run_output=$(make --no-print-directory -n "$@" "TEST_DATABASE_URL=$test_url" 2>&1)
  dry_run_status=$?
  set -e
  [ "$dry_run_status" -eq 0 ]
  case "$dry_run_output" in
    *"$test_url"*|*"$output_path"*|*"$backup_path"*)
      printf '%s\n' 'make -n expanded a secret or untrusted archive path' >&2
      exit 1
      ;;
  esac
}

assert_dry_run_safe backup-create "OUTPUT=$output_path"
assert_dry_run_safe backup-inspect "BACKUP=$backup_path"
assert_dry_run_safe backup-restore-dry-run "BACKUP=$backup_path"
assert_dry_run_safe backup-restore "BACKUP=$backup_path" CONFIRM=empty-instance
assert_dry_run_safe test-api-integration
assert_dry_run_safe verify
assert_dry_run_safe test-backup-e2e EXTERNAL_DB=1

non_db_failures=0
real_go=$(command -v go)
for target in verify-backup-fixture verify-go-quick; do
  if ! env -u TEST_DATABASE_URL \
    PATH="$test_root/bin:$PATH" \
    CGO_ENABLED=0 \
    REAL_GO="$real_go" \
    EXPECTED_MODE=no-database-url \
    make --no-print-directory "$target" >/dev/null 2>&1; then
    printf 'non-database Make target received TEST_DATABASE_URL: %s\n' "$target" >&2
    non_db_failures=1
  fi
done
exit "$non_db_failures"
