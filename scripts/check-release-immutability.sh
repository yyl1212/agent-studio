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
		enabled=$(gh api "repos/$repository/immutable-releases" -H "$api_version" --jq '.enabled')
		[[ "$enabled" == true ]] || fail 'immutable releases are not enabled'
		;;
	verify)
		[[ "$#" -eq 2 ]] || fail 'usage: check-release-immutability.sh verify <tag>'
		tag=$2
		timeout_seconds=${RELEASE_IMMUTABILITY_TIMEOUT_SECONDS:-60}
		poll_seconds=${RELEASE_IMMUTABILITY_POLL_SECONDS:-2}
		[[ "$timeout_seconds" =~ ^[0-9]+$ ]] || fail 'RELEASE_IMMUTABILITY_TIMEOUT_SECONDS must be a non-negative integer'
		[[ "$poll_seconds" =~ ^[0-9]+$ ]] || fail 'RELEASE_IMMUTABILITY_POLL_SECONDS must be a non-negative integer'
		deadline=$((SECONDS + timeout_seconds))
		while true; do
			immutable=$(gh api "repos/$repository/releases/tags/$tag" -H "$api_version" --jq '.immutable')
			[[ "$immutable" == true ]] && exit 0
			(( SECONDS < deadline )) || fail "release did not become immutable: $tag"
			sleep "$poll_seconds"
		done
		;;
	*)
		fail 'usage: check-release-immutability.sh <preflight|verify> [tag]'
		;;
esac
