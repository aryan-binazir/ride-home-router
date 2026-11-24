#!/bin/sh
# Tag origin/main with the next semver and publish a GitHub release.
# Usage: scripts/release.sh [patch|minor|major] [--dry-run]
# Bump: explicit argument, else the merged PR's major/minor label, else patch.
set -eu

level=
dry_run=
for arg in "$@"; do
	case $arg in
	patch | minor | major) level=$arg ;;
	--dry-run) dry_run=1 ;;
	*)
		echo "usage: $0 [patch|minor|major] [--dry-run]" >&2
		exit 2
		;;
	esac
done

repo=$(gh repo view --json nameWithOwner --jq .nameWithOwner)
remote="https://github.com/$repo.git"

git_https() {
	git -c credential.helper= -c credential.helper='!gh auth git-credential' "$@"
}

git_https fetch --quiet --tags "$remote" '+refs/heads/main:refs/remotes/origin/main'
head=$(git rev-parse origin/main)

if current=$(git describe --tags --exact-match --match 'v[0-9]*' "$head" 2>/dev/null); then
	echo "origin/main ($head) is already released as $current"
	exit 0
fi

last=$(git describe --tags --abbrev=0 --match 'v[0-9]*' "$head")

if [ -z "$level" ]; then
	labels=$(gh api "repos/$repo/commits/$head/pulls" --jq '.[0].labels[].name' 2>/dev/null || true)
	level='patch'
	if printf '%s\n' "$labels" | grep -qx major; then
		level=major
	elif printf '%s\n' "$labels" | grep -qx minor; then
		level=minor
	fi
fi

version=${last#v}
major=${version%%.*}
rest=${version#*.}
minor=${rest%%.*}
patch=${rest#*.}
case $level in
major) next="v$((major + 1)).0.0" ;;
minor) next="v$major.$((minor + 1)).0" ;;
patch) next="v$major.$minor.$((patch + 1))" ;;
esac

echo "$last -> $next ($level) at $head"
if [ -n "$dry_run" ]; then
	echo "dry run: no tag or release created"
	exit 0
fi

git tag -a "$next" -m "$next" "$head"
git_https push --quiet "$remote" "refs/tags/$next"
gh release create "$next" --repo "$repo" --verify-tag --latest \
	--title "$next" --generate-notes --notes-start-tag "$last"
