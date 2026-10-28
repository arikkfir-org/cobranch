#!/bin/sh
# Fails when go generate ./... changes the checkout: the generated code must match its sources as committed.
set -eu
go generate ./...
if [ -n "$(git status --porcelain)" ]; then
  echo "go generate ./... changed these files; run it and commit them:" >&2
  git status --short >&2
  exit 1
fi
