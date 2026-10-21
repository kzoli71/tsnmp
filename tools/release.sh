#!/usr/bin/env bash
# Cuts a tsnmp release, from the version bump to the signed, published GitHub
# release.
#
#   CERTUM_EMAIL=you@example.com tools/release.sh [-y] 1.2.3
#
# Steps (each one asks for confirmation unless -y is given):
#   1. checks: clean main, in sync with github/main and origin/main (Gitea)
#   2. bumps versioninfo.json, runs go generate and the local checks
#   3. commits the bump on release/vX.Y.Z, opens a PR, waits for green CI,
#      squash-merges it and pushes main to Gitea
#   4. tags the merged commit, pushes the tag to GitHub and Gitea, and waits
#      for the release workflow to create the draft release
#   5. runs sign-release, which asks for the SimplySign one-time code itself
#      (this script never reads it), signs and verifies the exe and publishes
#
# The script can be run again after a failure: steps that are already done
# (version bumped on main, tag pushed, draft created) are skipped.
#
# Requirements: git, go, gh (logged in), and sign-release (on PATH, or its
# path in $SIGN_RELEASE) with its own requirements.
set -euo pipefail

repo=kzoli71/tsnmp
gh_remote=github
gitea_remote=origin

usage() {
	echo "usage: CERTUM_EMAIL=... ${0##*/} [-y] X.Y.Z" >&2
	exit 2
}
die() { echo "release: $*" >&2; exit 1; }
step() { printf '\n==> %s\n' "$*"; }
confirm() {
	[[ -n $yes ]] && return
	local answer
	read -rp "$* [y/N] " answer </dev/tty
	[[ $answer == [yY] ]] || die "stopped"
}

yes=
while getopts yh opt; do
	case $opt in
	y) yes=1 ;;
	*) usage ;;
	esac
done
shift $((OPTIND - 1))
[[ $# -eq 1 ]] || usage
version=${1#v}
[[ $version =~ ^([0-9]+)\.([0-9]+)\.([0-9]+)$ ]] || die "the version must be X.Y.Z: $1"
major=${BASH_REMATCH[1]} minor=${BASH_REMATCH[2]} patch=${BASH_REMATCH[3]}
tag=v$version
branch=release/$tag

[[ -n ${CERTUM_EMAIL:-} ]] || die "set CERTUM_EMAIL to the SimplySign account e-mail"
sign_release=${SIGN_RELEASE:-$(command -v sign-release || command -v sign-release.sh || true)}
[[ -n $sign_release && -x $sign_release ]] ||
	die "sign-release not found: put it on PATH or set SIGN_RELEASE to its path"
for cmd in git go gh; do
	command -v "$cmd" >/dev/null || die "$cmd is not installed"
done
gh auth status >/dev/null 2>&1 || die "gh is not logged in"

cd "$(git rev-parse --show-toplevel)"
export GOTOOLCHAIN=local

# 1. Checks.
step "checking the repository"
[[ $(git symbolic-ref --short HEAD) == main ]] || die "not on main"
[[ -z $(git status --porcelain) ]] || die "the working tree is not clean"
git fetch -q "$gh_remote" main --tags
git fetch -q "$gitea_remote" main
head=$(git rev-parse HEAD)
[[ $head == $(git rev-parse "$gh_remote/main") ]] || die "main differs from $gh_remote/main"
[[ $head == $(git rev-parse "$gitea_remote/main") ]] || die "main differs from $gitea_remote/main"

if state=$(gh release view "$tag" -R "$repo" --json isDraft --jq .isDraft 2>/dev/null); then
	[[ $state == true ]] || die "release $tag is already published"
fi

current=$(sed -n 's/^\t\t"FileVersion": "\([^"]*\)".*/\1/p' versioninfo.json)
[[ -n $current ]] || die "no FileVersion in versioninfo.json"
echo "versioninfo.json: $current, releasing: $version"

# 2-3. Version bump, merged through a PR.
if [[ $current != "$version" ]]; then
	git rev-parse -q --verify "refs/tags/$tag" >/dev/null &&
		die "tag $tag exists but versioninfo.json on main is $current"
	confirm "Bump versioninfo.json to $version and run the checks?"

	step "bumping versioninfo.json"
	sed -i \
		-e "s/\"Major\": [0-9]*, \"Minor\": [0-9]*, \"Patch\": [0-9]*,/\"Major\": $major, \"Minor\": $minor, \"Patch\": $patch,/" \
		-e "s/^\(\t\t\"\(File\|Product\)Version\": \)\"[^\"]*\"/\1\"$version\"/" \
		versioninfo.json
	GOFLAGS=-mod=mod go generate
	git --no-pager diff --stat

	step "running the checks"
	go run ./tools/licenses
	go vet ./...
	go test -race ./...
	GOOS=windows GOARCH=amd64 go build -trimpath -o /dev/null .

	confirm "Commit, push $branch and open a PR?"
	step "opening the PR"
	git checkout -q -b "$branch"
	git add versioninfo.json resource_windows_*.syso
	git commit -q -m "chore: release $tag"
	git push -q -u "$gh_remote" "$branch"
	gh pr create -R "$repo" --base main --head "$branch" \
		--title "chore: release $tag" \
		--body "Bumps the Windows file and product version in versioninfo.json to $version and regenerates the resource files, before tagging $tag."

	step "waiting for CI"
	for _ in $(seq 30); do # the checks take a few seconds to show up
		[[ $(gh pr checks "$branch" -R "$repo" 2>&1) == *"no checks reported"* ]] || break
		sleep 5
	done
	gh pr checks "$branch" -R "$repo" --watch --fail-fast || die "CI failed, see the PR"

	confirm "CI is green. Merge the PR?"
	step "merging"
	gh pr merge "$branch" -R "$repo" --squash --delete-branch
	git checkout -q main
	git branch -q -D "$branch"
	git fetch -q "$gh_remote" main
	git merge -q --ff-only "$gh_remote/main"
	git push -q "$gitea_remote" main
	git fetch -q "$gh_remote" --prune
else
	echo "versioninfo.json is already $version, skipping the bump"
fi

# 4. Tag and draft release.
commit=$(git rev-parse HEAD)
if git rev-parse -q --verify "refs/tags/$tag" >/dev/null; then
	[[ $(git rev-parse "$tag^{commit}") == "$commit" ]] ||
		die "tag $tag exists but does not point to main ($commit)"
	echo "tag $tag already exists"
else
	git log -1 --format='%h %s' "$commit"
	confirm "Tag this commit $tag and push the tag to GitHub and Gitea?"
	step "tagging"
	git tag -s -m "tsnmp $tag" "$tag" "$commit"
fi
# Only this tag: archive/* must never reach GitHub.
git push -q "$gh_remote" "refs/tags/$tag"
git push -q "$gitea_remote" "refs/tags/$tag"

if ! gh release view "$tag" -R "$repo" >/dev/null 2>&1; then
	step "waiting for the release workflow"
	run=
	for _ in $(seq 30); do
		run=$(gh run list -R "$repo" -w release.yml -c "$commit" -L 1 --json databaseId --jq '.[0].databaseId')
		[[ -n $run ]] && break
		sleep 5
	done
	[[ -n $run ]] || die "the release workflow did not start"
	gh run watch "$run" -R "$repo" --exit-status || die "the release workflow failed"
	gh release view "$tag" -R "$repo" >/dev/null 2>&1 || die "no draft release $tag after the workflow"
fi

# 5. Signing and publishing.
step "signing the Windows exe (sign-release asks for the SimplySign code)"
"$sign_release" -R "$repo" -a 'tsnmp_{version}_windows_amd64.zip' ${yes:+-y} "$tag"

echo
echo "released: https://github.com/$repo/releases/tag/$tag"
