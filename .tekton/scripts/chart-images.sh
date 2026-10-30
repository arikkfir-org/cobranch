#!/bin/sh
# Builds both images, tagged with the commit, and pulls the bundled PostgreSQL's, for the cluster to load.
set -eu
docker build --build-arg VERSION="$VERSION" --tag "cobranch:$VERSION" .
docker build --tag "cobranch-console:$VERSION" web/console
docker pull --quiet "$(cat .ci/postgresql-image)"
