#!/bin/sh
# Refresh the `deploy` branch so it holds ONLY the contents of web/ at its root.
# The working tree, the index and the current branch are not touched, and
# web/.gitignore is honoured (no vendor/, no .env).
#
#   web/publish-branch.sh [branch]      then: git push origin <branch>
set -e
branch="${1:-deploy}"
root="$(git rev-parse --show-toplevel)"
cd "$root"

tmp_index="$(mktemp)"
rm -f "$tmp_index"
trap 'rm -f "$tmp_index"' EXIT

GIT_INDEX_FILE="$tmp_index" git add -A web
tree="$(GIT_INDEX_FILE="$tmp_index" git write-tree --prefix=web/)"

parent=""
if git rev-parse -q --verify "refs/heads/$branch" >/dev/null; then
    if [ "$(git rev-parse "$branch^{tree}")" = "$tree" ]; then
        echo "$branch is already up to date."
        exit 0
    fi
    parent="-p $(git rev-parse "$branch")"
fi

src="$(git rev-parse --short HEAD)"
commit="$(git commit-tree "$tree" $parent -m "deploy: web/ from $src")"
git update-ref "refs/heads/$branch" "$commit"
echo "$branch -> $(git rev-parse --short "$commit")"
