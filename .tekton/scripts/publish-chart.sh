#!/bin/sh
# Packages the commit's chart at version 0.0.0-main.<n>.g<short SHA>, where n counts the commits of main up to REVISION,
# so that a newer commit of main always has the higher version, with REVISION's short SHA as its app version, so that
# its images default to those task publish pushed, and pushes it to REGISTRY as charts/cobranch. It runs only once they
# are pushed: an installation that follows main installs the newest of these charts (>=0.0.0-main.0, <0.0.0), so never a
# commit whose images aren't there, nor one whose build a newer push overtook. Helm logs in with an access token of the
# pod's Workload Identity, from the GKE metadata server.
set -eu
# The clone is shallow: count main's commits up to REVISION in its whole history.
git fetch --quiet --unshallow "$REPO_URL" "$REVISION"
sha=$(printf '%s' "$REVISION" | cut -c1-7)
version="0.0.0-main.$(git rev-list --count "$REVISION").g$sha"
helm package chart/cobranch --version "$version" --app-version "$sha" --destination .ci/main
wget -qO- --header 'Metadata-Flavor: Google' \
  http://metadata.google.internal/computeMetadata/v1/instance/service-accounts/default/token |
  sed -n 's/.*"access_token" *: *"\([^"]*\)".*/\1/p' |
  helm registry login --username oauth2accesstoken --password-stdin "${REGISTRY%%/*}"
helm push ".ci/main/cobranch-$version.tgz" "oci://$REGISTRY/charts"
