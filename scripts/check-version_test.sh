#!/bin/sh
set -eu

test_root=$(mktemp -d "${TMPDIR:-/tmp}/agent-studio-version-test.XXXXXX")
trap 'rm -rf "$test_root"' EXIT HUP INT TERM

make_fixture() {
	name=$1
	root="$test_root/$name"
	mkdir -p "$root/scripts" "$root/sdk/go/agentnode" "$root/docs/releases"
	cp "$(dirname "$0")/check-version.sh" "$root/scripts/check-version.sh"
	printf '%s\n' 'package agentnode' 'const Version = "0.2.0"' > "$root/sdk/go/agentnode/node.go"
	printf '%s\n' '# v0.2.0-rc.1' > "$root/docs/releases/v0.2.0-rc.1.md"
	printf '%s\n' '# v0.2.0' > "$root/docs/releases/v0.2.0.md"
	git init -q "$root"
	git -C "$root" config user.name "Agent Studio Test"
	git -C "$root" config user.email "test@example.invalid"
	git -C "$root" config commit.gpgsign false
	git -C "$root" add .
	git -C "$root" commit -qm "fixture"
	git -C "$root" update-ref refs/remotes/origin/main HEAD
	printf '%s\n' "$root"
}

expect_failure() {
	expected=$1
	shift
	set +e
	output=$("$@" 2>&1)
	status=$?
	set -e
	if [ "$status" -eq 0 ]; then
		printf 'expected failure containing %s\n' "$expected" >&2
		exit 1
	fi
	case "$output" in
		*"$expected"*) ;;
		*)
			printf 'output %s does not contain %s\n' "$output" "$expected" >&2
			exit 1
			;;
	esac
}

valid=$(make_fixture valid)
valid_output=$(sh "$valid/scripts/check-version.sh" v0.2.0-rc.1)
[ "$valid_output" = "release preflight ok: v0.2.0-rc.1" ]

stable_output=$(sh "$valid/scripts/check-version.sh" v0.2.0)
[ "$stable_output" = "release preflight ok: v0.2.0" ]

expect_failure "invalid release tag" sh "$valid/scripts/check-version.sh" 0.2.0
expect_failure "invalid release tag" sh "$valid/scripts/check-version.sh" v0.2.0-rc.0
expect_failure "invalid release tag" sh "$valid/scripts/check-version.sh" v0.2.0-beta.1
expect_failure "invalid release tag" sh "$valid/scripts/check-version.sh" v0.2.0-extra

behind=$(make_fixture behind-main)
printf '%s\n' "local" > "$behind/local.txt"
git -C "$behind" add local.txt
git -C "$behind" commit -qm "local commit"
expect_failure "HEAD is not origin/main" sh "$behind/scripts/check-version.sh" v0.2.0-rc.1

missing_main=$(make_fixture missing-main)
git -C "$missing_main" update-ref -d refs/remotes/origin/main
expect_failure "missing origin/main" sh "$missing_main/scripts/check-version.sh" v0.2.0-rc.1

mismatch=$(make_fixture mismatch)
printf '%s\n' 'package agentnode' 'const Version = "0.3.0"' > "$mismatch/sdk/go/agentnode/node.go"
git -C "$mismatch" add .
git -C "$mismatch" commit -qm "change version"
expect_failure "SDK version mismatch" sh "$mismatch/scripts/check-version.sh" v0.2.0-rc.1

missing_notes=$(make_fixture missing-notes)
rm "$missing_notes/docs/releases/v0.2.0-rc.1.md"
git -C "$missing_notes" add -u
git -C "$missing_notes" commit -qm "remove notes"
expect_failure "missing release notes" sh "$missing_notes/scripts/check-version.sh" v0.2.0-rc.1

dirty=$(make_fixture dirty)
printf '\n' >> "$dirty/sdk/go/agentnode/node.go"
expect_failure "worktree is not clean" sh "$dirty/scripts/check-version.sh" v0.2.0-rc.1

untracked=$(make_fixture untracked)
printf '%s\n' "untracked" > "$untracked/local.txt"
expect_failure "worktree is not clean" sh "$untracked/scripts/check-version.sh" v0.2.0-rc.1

ignored=$(make_fixture ignored)
printf '%s\n' '/.pnpm-store/' > "$ignored/.gitignore"
git -C "$ignored" add .gitignore
git -C "$ignored" commit -qm "ignore pnpm store"
git -C "$ignored" update-ref refs/remotes/origin/main HEAD
mkdir -p "$ignored/.pnpm-store"
printf '%s\n' "cache" > "$ignored/.pnpm-store/cache"
sh "$ignored/scripts/check-version.sh" v0.2.0-rc.1 >/dev/null

printf '%s\n' "check-version tests passed"
