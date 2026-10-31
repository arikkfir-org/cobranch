#!/bin/sh
# Refuses TAG unless it is a version (vX.Y.Z) and COMMIT, the commit it names, is on main and has a chart whose images
# default to REGISTRY's, then writes that chart to .ci/release/chart/cobranch for tag-chart.sh, and the images it runs
# to .ci/release/images for tag-images.sh. Runs in a shallow checkout of main.
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
if ! git cat-file -e "$COMMIT:chart/cobranch/Chart.yaml" 2>/dev/null; then
  echo "Commit $COMMIT has no chart/cobranch: too old to be a version" >&2
  exit 1
fi
rm -rf .ci/release
mkdir -p .ci/release
git archive "$COMMIT" chart/cobranch | tar -x -C .ci/release
values=.ci/release/chart/cobranch/values.yaml
# image checks that the values' key $1 defaults to REGISTRY's image $2, and lists it.
image() {
  if [ "$(yq ".images.$1.repository" "$values")" != "$REGISTRY/$2" ]; then
    echo "Commit $COMMIT's chart doesn't default to the images in $REGISTRY: too old to be a version" >&2
    exit 1
  fi
  echo "$2" >>.ci/release/images
}
image cobranch cobranch
image console cobranch-console
# The Back-office's image, since the chart runs it.
case "$(yq '.images | has("backOffice")' "$values")" in
  true) image backOffice cobranch-back-office ;;
  false) ;;
  *) echo "Could not read the images of $COMMIT's chart" >&2; exit 1 ;;
esac
echo "Tag $TAG names $COMMIT, on main"
