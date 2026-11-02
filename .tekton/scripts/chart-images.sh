#!/bin/sh
# Builds Cobranch's images, tagged with the commit, on main's Go caches, and pulls the bundled PostgreSQL's, for the
# cluster to load.
set -eu
sh .tekton/scripts/go-cache-seed.sh
docker build --build-arg VERSION="$VERSION" --tag "cobranch:$VERSION" .
docker build --tag "cobranch-console:$VERSION" web/console
docker build --tag "cobranch-back-office:$VERSION" web/back-office
docker pull --quiet "$(cat .ci/postgresql-image)"
