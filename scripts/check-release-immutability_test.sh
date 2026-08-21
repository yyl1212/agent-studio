#!/bin/sh
set -eu

script_dir=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
test_root=$(mktemp -d "${TMPDIR:-/tmp}/agent-studio-release-immutability-test.XXXXXX")
trap 'rm -rf "$test_root"' EXIT HUP INT TERM

fake_bin="$test_root/bin"
mkdir -p "$fake_bin"

cat > "$fake_bin/timeout" <<'EOF'
#!/bin/sh
set -eu
[ "$#" -ge 4 ] && [ "$1" = --signal=KILL ] || {
	printf 'unexpected timeout arguments: %s\n' "$*" >&2
	exit 2
}
case "$2" in
	*[!0-9]s|'s') exit 2 ;;
esac
shift 2
if [ "$FAKE_SCENARIO" = verify-timeout ]; then
	exit 124
fi
FAKE_TIMEOUT_ACTIVE=1 exec "$@"
EOF
chmod +x "$fake_bin/timeout"

cat > "$fake_bin/gh" <<'EOF'
#!/bin/sh
set -eu

expected_api_version='X-GitHub-Api-Version: 2026-03-10'
if [ "$#" -ne 6 ] || [ "$1" != api ] || [ "$3" != -H ] || [ "$4" != "$expected_api_version" ] || [ "$5" != --jq ]; then
	printf 'unexpected gh arguments: %s\n' "$*" >&2
	exit 2
fi

endpoint=$2
query=$6
case "$FAKE_SCENARIO:$endpoint:$query" in
	preflight-enabled:repos/yyl1212/agent-studio/immutable-releases:.enabled)
		printf 'true\n'
		;;
	preflight-disabled:repos/yyl1212/agent-studio/immutable-releases:.enabled)
		printf 'false\n'
		;;
	verify-immutable:repos/yyl1212/agent-studio/releases/tags/v0.3.0-rc.2:.immutable)
		[ "${FAKE_TIMEOUT_ACTIVE:-}" = 1 ] || exit 2
		printf 'true\n'
		;;
	verify-mutable:repos/yyl1212/agent-studio/releases/tags/v0.3.0-rc.2:.immutable)
		[ "${FAKE_TIMEOUT_ACTIVE:-}" = 1 ] || exit 2
		printf 'false\n'
		;;
	verify-timeout:*)
		exit 2
		;;
	verify-eventual:repos/yyl1212/agent-studio/releases/tags/v0.3.0-rc.2:.immutable)
		[ "${FAKE_TIMEOUT_ACTIVE:-}" = 1 ] || exit 2
		counter="$FAKE_STATE_DIR/eventual-count"
		if [ -f "$counter" ]; then
			printf 'true\n'
		else
			: > "$counter"
			printf 'false\n'
		fi
		;;
	verify-transient:repos/yyl1212/agent-studio/releases/tags/v0.3.0-rc.2:.immutable)
		[ "${FAKE_TIMEOUT_ACTIVE:-}" = 1 ] || exit 2
		counter="$FAKE_STATE_DIR/transient-count"
		if [ -f "$counter" ]; then
			printf 'true\n'
		else
			: > "$counter"
			exit 1
		fi
		;;
	*)
		printf 'unexpected fake scenario or endpoint: %s %s %s\n' "$FAKE_SCENARIO" "$endpoint" "$query" >&2
		exit 2
		;;
esac
EOF
chmod +x "$fake_bin/gh"

run_check() {
	scenario=$1
	shift
	FAKE_SCENARIO=$scenario \
	FAKE_STATE_DIR=$test_root \
	GITHUB_REPOSITORY=yyl1212/agent-studio \
	RELEASE_IMMUTABILITY_TIMEOUT_SECONDS=${TEST_TIMEOUT_SECONDS:-0} \
	RELEASE_IMMUTABILITY_POLL_SECONDS=0 \
	PATH="$fake_bin:$PATH" \
		bash "$script_dir/check-release-immutability.sh" "$@"
}

expect_failure() {
	expected_message=$1
	shift
	set +e
	output=$("$@" 2>&1)
	status=$?
	set -e
	if [ "$status" -eq 0 ]; then
		printf 'expected failure containing %s\n' "$expected_message" >&2
		exit 1
	fi
	case "$output" in
		*"$expected_message"*) ;;
		*)
			printf 'output %s does not contain %s\n' "$output" "$expected_message" >&2
			exit 1
			;;
	esac
}

run_check preflight-enabled preflight
expect_failure 'immutable releases are not enabled' run_check preflight-disabled preflight
run_check verify-immutable verify v0.3.0-rc.2
expect_failure 'release did not become immutable' run_check verify-mutable verify v0.3.0-rc.2
expect_failure 'release did not become immutable within the timeout' run_check verify-timeout verify v0.3.0-rc.2
TEST_TIMEOUT_SECONDS=2 run_check verify-eventual verify v0.3.0-rc.2
TEST_TIMEOUT_SECONDS=2 run_check verify-transient verify v0.3.0-rc.2

printf '%s\n' 'release immutability tests passed'
