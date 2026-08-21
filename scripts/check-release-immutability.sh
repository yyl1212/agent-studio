#!/usr/bin/env bash
set -euo pipefail

api_version='X-GitHub-Api-Version: 2026-03-10'

fail() {
	printf 'release immutability check failed: %s\n' "$1" >&2
	exit 1
}

repository=${GITHUB_REPOSITORY:-}
[[ -n "$repository" ]] || fail 'GITHUB_REPOSITORY is required'

case "${1:-}" in
	preflight)
		[[ "$#" -eq 1 ]] || fail 'usage: check-release-immutability.sh preflight'
		set +e
		enabled=$(gh api "repos/$repository/immutable-releases" -H "$api_version" --jq '.enabled')
		api_status=$?
		set -e
		[[ "$api_status" -eq 0 && "$enabled" == true ]] || fail 'immutable releases are not enabled'
		;;
	verify)
		[[ "$#" -eq 2 ]] || fail 'usage: check-release-immutability.sh verify <tag>'
		tag=$2
		timeout_seconds=${RELEASE_IMMUTABILITY_TIMEOUT_SECONDS:-60}
		poll_seconds=${RELEASE_IMMUTABILITY_POLL_SECONDS:-2}
		[[ "$timeout_seconds" =~ ^[0-9]+$ ]] || fail 'RELEASE_IMMUTABILITY_TIMEOUT_SECONDS must be a non-negative integer'
		[[ "$poll_seconds" =~ ^[0-9]+$ ]] || fail 'RELEASE_IMMUTABILITY_POLL_SECONDS must be a non-negative integer'
		command -v timeout >/dev/null || fail 'timeout command is required for bounded verification'
		deadline=$((SECONDS + timeout_seconds))
		attempted=0
		while true; do
			remaining=$((deadline - SECONDS))
			(( attempted == 0 || remaining > 0 )) || fail "release did not become immutable: $tag"
			request_seconds=$remaining
			(( request_seconds > 0 )) || request_seconds=1

			set +e
			immutable=$(timeout --signal=KILL "${request_seconds}s" \
				gh api "repos/$repository/releases/tags/$tag" -H "$api_version" --jq '.immutable')
			api_status=$?
			set -e
			[[ "$api_status" -eq 0 && "$immutable" == true ]] && exit 0
			if [[ "$api_status" -eq 124 || "$api_status" -eq 137 ]]; then
				fail "release did not become immutable within the timeout: $tag"
			fi

			attempted=1
			remaining=$((deadline - SECONDS))
			(( remaining > 0 )) || fail "release did not become immutable: $tag"
			sleep_seconds=$poll_seconds
			(( sleep_seconds <= remaining )) || sleep_seconds=$remaining
			(( sleep_seconds == 0 )) || sleep "$sleep_seconds"
		done
		;;
	*)
		fail 'usage: check-release-immutability.sh <preflight|verify> [tag]'
		;;
esac
