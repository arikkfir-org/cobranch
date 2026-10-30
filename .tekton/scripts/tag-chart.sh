#!/bin/sh
# Packages the tagged commit's chart (.ci/release/chart/cobranch, from tag-check.sh) at TAG's version, its app version
# the same so its images default to the tag tag-images.sh added, and pushes it to REGISTRY as charts/cobranch. Helm logs
# in with an access token of the pod's Workload Identity, from the GKE metadata server.
set -eu
version=${TAG#v}
helm package .ci/release/chart/cobranch --version "$version" --app-version "$version" --destination .ci/release
wget -qO- --header 'Metadata-Flavor: Google' \
  http://metadata.google.internal/computeMetadata/v1/instance/service-accounts/default/token |
  sed -n 's/.*"access_token" *: *"\([^"]*\)".*/\1/p' |
  helm registry login --username oauth2accesstoken --password-stdin "${REGISTRY%%/*}"
helm push ".ci/release/cobranch-$version.tgz" "oci://$REGISTRY/charts"
