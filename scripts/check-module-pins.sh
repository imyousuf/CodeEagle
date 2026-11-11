#!/usr/bin/env bash
# check-module-pins.sh verifies that the application, and every nested module,
# builds against copies of this repository's own modules that are the ones in
# this checkout.
#
# A nested module (pkg/jev, pkg/llm, pkg/kit) is required by version, never by
# a replace directive, because `go install .../cmd/codeeagle@latest` refuses
# replace directives and ignores go.work. A version is a tag or, while a module
# is untagged, the pseudo-version of a pushed commit. The workspace hides a
# stale pin: everything here builds against the working copy, so a pin that
# names an older commit passes every test and still ships the old code to
# anyone who installs. This check closes that gap. For each selected version of
# one of our modules it requires that the commit is in this history (a rebase
# or squash merge orphans it) and that the module's directory is unchanged
# since that commit.
#
# Usage: scripts/check-module-pins.sh [module-dir ...]
# With no arguments it checks the repository root and every nested module.
set -euo pipefail

prefix="github.com/imyousuf/CodeEagle/"
root=$(git rev-parse --show-toplevel)
cd "$root"

dirs=("$@")
if [ ${#dirs[@]} -eq 0 ]; then
	dirs=(.)
	while IFS= read -r mod; do
		dirs+=("$(dirname "$mod")")
	done < <(git ls-files 'pkg/*/go.mod')
fi

status=0
for dir in "${dirs[@]}"; do
	# The build list as a consumer of this module resolves it: outside the
	# workspace, so the selected versions are the pinned ones.
	selected=$(cd "$dir" && GOWORK=off go list -m -f '{{if not .Main}}{{.Path}} {{.Version}}{{end}}' all |
		grep "^${prefix}pkg/" || true)
	while read -r path version; do
		[ -n "$path" ] || continue
		sub=${path#"$prefix"}
		if [[ $version =~ -[0-9]{14}-([0-9a-f]{12})$ ]]; then
			ref=${BASH_REMATCH[1]}
		else
			ref="refs/tags/$sub/$version"
		fi
		sha=$(git rev-parse --verify -q "$ref^{commit}" || true)
		if [ -z "$sha" ]; then
			echo "$dir: $path $version names a commit this checkout does not have" >&2
			status=1
			continue
		fi
		if ! git merge-base --is-ancestor "$sha" HEAD; then
			echo "$dir: $path $version (${sha:0:12}) is not in this history; was the branch rebased or squashed?" >&2
			status=1
			continue
		fi
		if ! git diff --quiet "$sha" HEAD -- "$sub"; then
			echo "$dir: $path is pinned at ${sha:0:12}, but $sub has changed since; pin it again" >&2
			git diff --stat "$sha" HEAD -- "$sub" >&2
			status=1
			continue
		fi
		echo "$dir: $path $version matches $sub"
	done <<<"$selected"
done
exit $status
