#!/usr/bin/env bash
# check-root-deps.sh lists the application's third-party requirements that
# differ from those at a base revision, ignoring this repository's own modules.
#
# Creating a nested module and running `go mod tidy` in it resolves each new
# requirement at @latest, and minimal version selection then raises the
# application to whatever the module asked for. Nothing fails: the
# application just builds against newer libraries. Run this before merging a
# change that adds or re-pins a nested module; it prints nothing and exits 0
# when only our own modules moved.
#
# Usage: scripts/check-root-deps.sh [base-revision]   (default: origin/main)
set -euo pipefail

base=${1:-origin/main}
root=$(git rev-parse --show-toplevel)
cd "$root"

tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT

requires() { # go.mod path -> "module version" lines, own modules excluded
	(cd "$(dirname "$1")" && go mod edit -json "$(basename "$1")") |
		awk '/"Require"/ {in_req=1} in_req && /"Path"/ {gsub(/[",]/, "", $2); path=$2}
			in_req && /"Version"/ {gsub(/[",]/, "", $2); print path, $2}
			in_req && /^\t\]/ {in_req=0}' |
		grep -v '^github.com/imyousuf/CodeEagle/' | sort
}

git show "$base:go.mod" >"$tmp/go.mod"
if ! diff <(requires "$tmp/go.mod") <(requires go.mod); then
	echo "third-party requirements changed against $base (lines above: < base, > here)" >&2
	exit 1
fi
