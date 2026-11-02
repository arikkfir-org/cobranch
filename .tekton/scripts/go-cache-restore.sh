#!/bin/sh
# Restores main's Go caches that its arguments name, each archive main/go/<name>.tar.gz of bucket
# arikkfir-cobranch-ci-cache, which main's warm task saves (go-cache-save.sh), into GO_CACHE/<name> (/go-cache by
# default), which the steps name as GOMODCACHE, GOCACHE or GOLANGCI_LINT_CACHE: mod, the modules; generate, build, race
# and lint, Go's build caches of the generators, the static binaries, the tests with the race detector and the linters;
# golangci, golangci-lint's own. A task restores only those its steps use, side by side, each unpacking on a core of its
# own. Without its archive, a cache starts empty. Go marks a cache entry it uses by its modification time, at most once
# an hour: dated two hours back here, each entry a run uses shows it, and go-cache-save.sh keeps only those. Runs in
# curlimages/curl, as the step's Workload Identity, or, seeding the Docker daemon's cache mounts (go-cache-seed.sh),
# with the token that the build's secret gcs-token holds.
set -eu
dir="${GO_CACHE:-/go-cache}"
if [ -r /run/secrets/gcs-token ]; then
  token="$(cat /run/secrets/gcs-token)"
else
  token="$(curl -fsS -H 'Metadata-Flavor: Google' \
    http://metadata.google.internal/computeMetadata/v1/instance/service-accounts/default/token |
    sed -n 's/.*"access_token" *: *"\([^"]*\)".*/\1/p')"
fi
restored="$(($(date +%s) - 7200))"
# restore NAME streams main/go/NAME.tar.gz into the cache NAME and dates its entries, or empties it.
restore() {
  cache="${dir}/$1" archive="main/go/$1.tar.gz"
  mkdir -p "${cache}"
  # shellcheck disable=SC3040 # busybox's sh, curlimages/curl's, has pipefail
  if (set -o pipefail; curl -sf -w '%{stderr}%{http_code}' -H "Authorization: Bearer ${token}" \
    "https://storage.googleapis.com/storage/v1/b/arikkfir-cobranch-ci-cache/o/main%2Fgo%2F$1.tar.gz?alt=media" \
    2>"/tmp/$1.status" | tar -xzf - -C "${cache}" 2>"/tmp/$1.tar"); then
    echo "Restored ${archive}"
  else
    find "${cache}" -mindepth 1 -maxdepth 1 -exec rm -rf {} +
    case "$(cat "/tmp/$1.status")" in
      404) echo "No ${archive} yet" ;;
      200) echo "${archive} didn't unpack: $(cat "/tmp/$1.tar")" ;;
      *) echo "${archive} unreadable (HTTP $(cat "/tmp/$1.status"))" ;;
    esac
  fi
  [ "$1" = mod ] || find "${cache}" -path "${cache}/??/*" -type f -exec touch -d "@${restored}" {} + ||
    echo "Couldn't date ${cache}'s entries: go-cache-save.sh may drop entries this run uses"
}
# shellcheck disable=SC2048,SC2086 # The names may come as one argument, the StepAction's param, split at its spaces.
for name in $*; do
  restore "${name}" &
done
wait
touch "${dir}/.restored"
