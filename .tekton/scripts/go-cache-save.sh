#!/bin/sh
# Saves each of Go's caches in /go-cache (mod, generate, build, race, lint and golangci) as archive
# main/go/<name>.tar.gz of bucket arikkfir-cobranch-ci-cache, which every run restores (go-cache-restore.sh). It first
# trims them to what Cobranch builds with: the entries this run used (go-cache-restore.sh dated the rest two hours
# back), and the modules go.sum names, unpacked, without their zips, which Go doesn't read once a module is unpacked, or
# their test data. The caches archive side by side. A cache it can't trim or save costs later runs time, never this run
# its result. Runs in curlimages/curl, as the step's Workload Identity, in the checkout.
set -eu
token="$(curl -fsS -H 'Metadata-Flavor: Google' \
  http://metadata.google.internal/computeMetadata/v1/instance/service-accounts/default/token |
  sed -n 's/.*"access_token" *: *"\([^"]*\)".*/\1/p')"
# go.sum's modules as the module cache spells them, a capital as ! and its lower case.
sed -n 's|^\([^ ]*\) \([^ /]*\) .*|\1@\2|p' go.sum | sed 's/[A-Z]/!&/g' | tr '[:upper:]' '[:lower:]' >/tmp/modules
cd /go-cache
# trim NAME drops what this run didn't use from the cache NAME.
trim() {
  if [ "$1" != mod ]; then
    find "$1" -path "$1/??/*" -type f ! -newer .restored -delete
  elif [ -s /tmp/modules ]; then
    chmod -R u+w mod && rm -rf mod/cache/download/sumdb &&
      find mod/cache/download -name '*.zip' -delete &&
      find mod -path mod/cache -prune -o -type d -name testdata -prune -exec rm -rf {} + &&
      (cd mod && find . -path ./cache -prune -o -type d -name '*@*' -prune -print) |
      sed 's|^\./||' | grep -vxF -f /tmp/modules | (cd mod && xargs -r rm -rf)
  fi
}
# save NAME trims the cache NAME and uploads it as main/go/NAME.tar.gz.
save() {
  archive="main/go/$1.tar.gz"
  trim "$1" || echo "Couldn't trim $1"
  # shellcheck disable=SC3040 # busybox's sh, curlimages/curl's, has pipefail
  if ! (set -o pipefail; tar -cf - -C "$1" . | gzip -1 >"/tmp/$1.tar.gz"); then
    echo "Couldn't archive $1"
    return 0
  fi
  # A failed upload prints its status, 000 when there was no answer, and keeps main's last archive.
  status="$(curl -sS -o /dev/null -w '%{http_code}' -X POST -T "/tmp/$1.tar.gz" \
    -H "Authorization: Bearer ${token}" -H 'Content-Type: application/gzip' \
    "https://storage.googleapis.com/upload/storage/v1/b/arikkfir-cobranch-ci-cache/o?uploadType=media&name=main%2Fgo%2F$1.tar.gz" ||
    true)"
  if [ "${status}" = 200 ]; then
    echo "Saved ${archive}: $(du -h "/tmp/$1.tar.gz" | cut -f1)"
  else
    echo "Couldn't save ${archive} (HTTP ${status})"
  fi
}
for name in mod generate build race lint golangci; do
  [ -d "${name}" ] && save "${name}" &
done
wait
