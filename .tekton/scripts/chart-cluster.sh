#!/bin/sh
# Creates the throwaway cluster in the Docker daemon beside the steps, and loads the images its pods run, which
# therefore never pull. It saves them for the daemon's own platform only: the pulled PostgreSQL image's index names
# every platform, and the cluster's import fails on the content of those the daemon never pulled.
set -eu
kind=.ci/bin/kind
"$kind" create cluster --name devhub --kubeconfig "$KUBECONFIG" --wait 3m
docker save --platform "$(docker version --format '{{.Server.Os}}/{{.Server.Arch}}')" --output .ci/images.tar \
  "devhub:$VERSION" "devhub-console:$VERSION" "$(cat .ci/postgresql-image)"
"$kind" load image-archive --name devhub .ci/images.tar
