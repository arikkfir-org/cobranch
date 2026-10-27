#!/bin/sh
# Creates the throwaway cluster in the Docker daemon beside the steps, and loads the images its pods run, which
# therefore never pull.
set -eu
kind=.ci/bin/kind
"$kind" create cluster --name devhub --kubeconfig "$KUBECONFIG" --wait 3m
"$kind" load docker-image --name devhub "devhub:$VERSION" "devhub-console:$VERSION" "$(cat .ci/postgresql-image)"
