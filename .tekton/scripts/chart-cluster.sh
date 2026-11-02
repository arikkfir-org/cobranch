#!/bin/sh
# Creates the throwaway cluster in the Docker daemon beside the steps while chart-images.sh builds the images, which
# nothing in its creation waits for, then loads the images its pods run, which
# therefore never pull. It saves them for the daemon's own platform only: the pulled PostgreSQL image's index names
# every platform, and the cluster's import fails on the content of those the daemon never pulled.
set -eu
kind=.ci/bin/kind
"$kind" create cluster --name cobranch --kubeconfig "$KUBECONFIG" --wait 3m >.ci/kind.log 2>&1 &
cluster=$!
sh .tekton/scripts/chart-images.sh
status=0
wait "$cluster" || status=$?
cat .ci/kind.log
[ "$status" -eq 0 ] || { echo "Creating the cluster failed with status $status" >&2; exit "$status"; }
docker save --platform "$(docker version --format '{{.Server.Os}}/{{.Server.Arch}}')" --output .ci/images.tar \
  "cobranch:$VERSION" "cobranch-console:$VERSION" "cobranch-back-office:$VERSION" "$(cat .ci/postgresql-image)"
"$kind" load image-archive --name cobranch .ci/images.tar
