#!/bin/sh
set -eu

script_dir=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
test_root=$(mktemp -d "${TMPDIR:-/tmp}/agent-studio-release-preflight-test.XXXXXX")
trap 'rm -rf "$test_root"' EXIT HUP INT TERM
fake_bin="$test_root/bin"
mkdir -p "$fake_bin"

cat > "$fake_bin/git" <<'EOF'
#!/bin/sh
set -eu

case "$1:$2" in
	remote:get-url)
		[ "$#" -eq 3 ] && [ "$3" = origin ] || exit 2
		printf '%s\n' 'git@github.com:yyl1212/agent-studio.git'
		;;
	show-ref:--verify)
		[ "$#" -eq 4 ] && [ "$3" = --quiet ] && [ "$4" = refs/tags/v0.3.0-rc.2 ] || exit 2
		[ "$FAKE_SCENARIO" = local-existing ] && exit 0
		exit 1
		;;
	ls-remote:--exit-code)
		[ "$#" -eq 5 ] && [ "$3" = --tags ] && [ "$4" = origin ] && [ "$5" = refs/tags/v0.3.0-rc.2 ] || exit 64
		case "$FAKE_SCENARIO" in
			remote-existing)
				printf '%s\t%s\n' 0123456789012345678901234567890123456789 refs/tags/v0.3.0-rc.2
				exit 0
				;;
			remote-error) exit 128 ;;
			*) exit 2 ;;
		esac
		;;
	rev-parse:HEAD)
		[ "$#" -eq 2 ] || exit 64
		printf '%s\n' 1111111111111111111111111111111111111111
		;;
	*)
		printf 'unexpected git arguments: %s\n' "$*" >&2
		exit 2
		;;
esac
EOF
chmod +x "$fake_bin/git"

cat > "$fake_bin/gh" <<'EOF'
#!/bin/sh
set -eu
[ "$#" -eq 6 ] && [ "$1" = api ] || exit 2
[ "$2" = repos/yyl1212/agent-studio/immutable-releases ] || {
	printf 'preflight queried the wrong repository: %s\n' "$2" >&2
	exit 2
}
if [ "$FAKE_SCENARIO" = immutable-disabled ]; then
	printf '%s\n' 'gh: Not Found (HTTP 404)' >&2
	exit 1
fi
printf 'true\n'
EOF
chmod +x "$fake_bin/gh"

run_preflight() {
	scenario=$1
	GITHUB_REPOSITORY=someone/else \
	FAKE_SCENARIO=$scenario \
	PATH="$fake_bin:$PATH" \
		bash "$script_dir/release-preflight.sh" v0.3.0-rc.2
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

run_preflight available
expect_failure 'local tag already exists' run_preflight local-existing
expect_failure 'remote tag already exists' run_preflight remote-existing
expect_failure 'unable to determine whether remote tag exists' run_preflight remote-error
expect_failure 'immutable releases are not enabled' run_preflight immutable-disabled

printf '%s\n' 'release preflight tests passed'
