#!/bin/sh
# Saves Go's caches in /go-cache as archive main/go.tar.gz of bucket arikkfir-cobranch-ci-cache, which every run
# restores (go-cache-restore.sh). It first trims them to what Cobranch builds with: the build and lint entries this run
# used (go-cache-restore.sh dated the rest two hours back), and the modules go.sum names, unpacked, without their zips,
# which Go doesn't read once a module is unpacked, or their test data. A cache it can't trim or save costs later runs
# time, never this run its result. Runs in curlimages/curl, as the step's Workload Identity, in the checkout.
set -eu
token="$(curl -fsS -H 'Metadata-Flavor: Google' \
  http://metadata.google.internal/computeMetadata/v1/instance/service-accounts/default/token |
  sed -n 's/.*"access_token" *: *"\([^"]*\)".*/\1/p')"
# go.sum's modules as the module cache spells them, a capital as ! and its lower case.
sed -n 's|^\([^ ]*\) \([^ /]*\) .*|\1@\2|p' go.sum | sed 's/[A-Z]/!&/g' | tr '[:upper:]' '[:lower:]' >/tmp/modules
cd /go-cache
if [ -s /tmp/modules ] && ! (
  find build lint -path '*/??/*' -type f ! -newer .restored -delete &&
    chmod -R u+w mod && rm -rf mod/cache/download/sumdb &&
    find mod/cache/download -name '*.zip' -delete &&
    find mod -path mod/cache -prune -o -type d -name testdata -prune -exec rm -rf {} + &&
    (cd mod && find . -path ./cache -prune -o -type d -name '*@*' -prune -print) |
    sed 's|^\./||' | grep -vxF -f /tmp/modules | (cd mod && xargs -r rm -rf)
); then
  echo "Couldn't trim Go's caches"
fi
# shellcheck disable=SC3040 # busybox's sh, curlimages/curl's, has pipefail
if ! (set -o pipefail; tar -cf - build mod lint | gzip -1 >/tmp/go.tar.gz); then
  echo "Couldn't archive Go's caches"
  exit 0
fi
# A failed upload prints its status, 000 when there was no answer, and keeps main's last archive.
status="$(curl -sS -o /dev/null -w '%{http_code}' -X POST -T /tmp/go.tar.gz \
  -H "Authorization: Bearer ${token}" -H 'Content-Type: application/gzip' \
  "https://storage.googleapis.com/upload/storage/v1/b/arikkfir-cobranch-ci-cache/o?uploadType=media&name=main%2Fgo.tar.gz" ||
  true)"
if [ "${status}" = 200 ]; then
  echo "Saved main/go.tar.gz: $(du -h /tmp/go.tar.gz | cut -f1)"
else
  echo "Couldn't save main/go.tar.gz (HTTP ${status})"
fi
