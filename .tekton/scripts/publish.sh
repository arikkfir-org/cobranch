#!/bin/sh
# Builds images cobranch, cobranch-console and cobranch-back-office of the checkout, tagged with REVISION's short SHA
# (also the version the binary reports), and pushes them to REGISTRY. The Docker daemon pushes with an access token
# of the pod's Workload Identity, from the GKE metadata server.
set -eu
version=$(printf '%s' "$REVISION" | cut -c1-7)
cobranch="$REGISTRY/cobranch:$version"
console="$REGISTRY/cobranch-console:$version"
backoffice="$REGISTRY/cobranch-back-office:$version"
docker build --build-arg VERSION="$version" --tag "$cobranch" .
docker build --tag "$console" web/console
docker build --tag "$backoffice" web/back-office
wget -qO- --header 'Metadata-Flavor: Google' \
  http://metadata.google.internal/computeMetadata/v1/instance/service-accounts/default/token |
  sed -n 's/.*"access_token" *: *"\([^"]*\)".*/\1/p' |
  docker login --username oauth2accesstoken --password-stdin "https://${REGISTRY%%/*}"
docker push "$cobranch"
docker push "$console"
docker push "$backoffice"
