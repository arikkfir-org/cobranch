#!/bin/sh
# Restores main's Go caches, archive main/go.tar.gz of bucket arikkfir-cobranch-ci-cache, which main's warm task saves
# (go-cache-save.sh), into GO_CACHE (/go-cache by default): build, mod and lint, which GOCACHE, GOMODCACHE and
# GOLANGCI_LINT_CACHE name. Without it, the run downloads and compiles everything. Go marks a cache entry it uses by
# its modification time, at most once an hour: dated two hours back here, each entry a run uses shows it, and
# go-cache-save.sh keeps only those. Runs in curlimages/curl, as the step's Workload Identity, or, seeding the Docker
# daemon's cache mounts (go-cache-seed.sh), with the token that the build's secret gcs-token holds.
set -eu
dir="${GO_CACHE:-/go-cache}"
if [ -r /run/secrets/gcs-token ]; then
  token="$(cat /run/secrets/gcs-token)"
else
  token="$(curl -fsS -H 'Metadata-Flavor: Google' \
    http://metadata.google.internal/computeMetadata/v1/instance/service-accounts/default/token |
    sed -n 's/.*"access_token" *: *"\([^"]*\)".*/\1/p')"
fi
mkdir -p "${dir}/build" "${dir}/mod" "${dir}/lint"
# shellcheck disable=SC3040 # busybox's sh, curlimages/curl's, has pipefail
if (set -o pipefail; curl -sf -w '%{stderr}%{http_code}' -H "Authorization: Bearer ${token}" \
  "https://storage.googleapis.com/storage/v1/b/arikkfir-cobranch-ci-cache/o/main%2Fgo.tar.gz?alt=media" \
  2>/tmp/status | tar -xzf - -C "${dir}" 2>/tmp/tar); then
  echo "Restored main/go.tar.gz"
else
  find "${dir}/build" "${dir}/mod" "${dir}/lint" -mindepth 1 -maxdepth 1 -exec rm -rf {} +
  case "$(cat /tmp/status)" in
    404) echo "No main/go.tar.gz yet" ;;
    200) echo "main/go.tar.gz didn't unpack: $(cat /tmp/tar)" ;;
    *) echo "main/go.tar.gz unreadable (HTTP $(cat /tmp/status))" ;;
  esac
fi
restored="$(( $(date +%s) - 7200 ))"
find "${dir}/build" "${dir}/lint" -path "${dir}/*/??/*" -type f -exec touch -d "@${restored}" {} + ||
  echo "Couldn't date ${dir}'s entries: go-cache-save.sh may drop entries this run uses"
touch "${dir}/.restored"
