#!/bin/sh
# Seeds the Docker daemon's Go cache mounts with main's Go caches (go-cache-restore.sh, through go-cache.Dockerfile),
# so that building image cobranch compiles only what main's caches lack. A seed that fails costs the build time, never
# its result. The token is the pod's Workload Identity's, from the GKE metadata server; tasks chart and publish run it.
set -eu
GCS_TOKEN="$(wget -qO- --header 'Metadata-Flavor: Google' \
  http://metadata.google.internal/computeMetadata/v1/instance/service-accounts/default/token |
  sed -n 's/.*"access_token" *: *"\([^"]*\)".*/\1/p')"
export GCS_TOKEN
docker build --progress=plain --secret id=gcs-token,env=GCS_TOKEN --file .tekton/go-cache.Dockerfile .tekton ||
  echo "Couldn't seed Go's caches: image cobranch compiles everything"
