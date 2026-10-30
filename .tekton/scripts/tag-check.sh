#!/bin/sh
# Refuses TAG unless it is a version (vX.Y.Z) and COMMIT, the commit it names, is on main, then writes that commit's
# chart to .ci/release/chart/devhub for tag-chart.sh. Runs in a shallow checkout of main.
set -eu
if ! printf '%s\n' "$TAG" | grep -Eq '^v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$'; then
  echo "Tag $TAG is not a version (vX.Y.Z): ignoring it" >&2
  exit 1
fi
if [ "$(git rev-parse --is-shallow-repository)" = true ]; then
  git fetch --quiet --unshallow "$REPO_URL" main
else
  git fetch --quiet "$REPO_URL" main
fi
main=$(git rev-parse FETCH_HEAD)
git cat-file -e "$COMMIT^{commit}" 2>/dev/null || git fetch --quiet "$REPO_URL" "$COMMIT"
if ! git merge-base --is-ancestor "$COMMIT" "$main"; then
  echo "Tag $TAG points at $COMMIT, which is not on main: not a version" >&2
  exit 1
fi
if ! git cat-file -e "$COMMIT:chart/devhub/Chart.yaml" 2>/dev/null; then
  echo "Commit $COMMIT has no chart/devhub: too old to be a version" >&2
  exit 1
fi
rm -rf .ci/release
mkdir -p .ci/release
git archive "$COMMIT" chart/devhub | tar -x -C .ci/release
echo "Tag $TAG names $COMMIT, on main"
