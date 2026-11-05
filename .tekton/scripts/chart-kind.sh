#!/bin/sh
# Installs kind's release binary for the node's architecture as .ci/bin/kind, and fails unless it matches the checksum
# pinned here, the release's own.
set -eu
version=v0.33.0
case "$(uname -m)" in
x86_64) arch=amd64 sum=aee6151561422756b764a4ae28e7f44cda5af5a9eead3cc9985112b1de8d8e0d ;;
aarch64) arch=arm64 sum=20022bee6cfcd5086cb7234d218e3454e6090022f2a8f55d1fa7fcf42c3867a2 ;;
*)
  echo "No kind binary is pinned for $(uname -m)" >&2
  exit 1
  ;;
esac
mkdir -p .ci/bin
# GitHub's release downloads now and then answer 504 or drop the connection, which would fail the run before any test:
# try a few times, a little longer apart each time, before giving up.
url="https://github.com/kubernetes-sigs/kind/releases/download/$version/kind-linux-$arch"
for attempt in 1 2 3 4 5; do
  if wget -qO .ci/bin/kind "$url"; then
    break
  fi
  if [ "$attempt" -eq 5 ]; then
    echo "Could not download kind $version after $attempt attempts" >&2
    exit 1
  fi
  echo "Downloading kind $version failed (attempt $attempt); trying again in $((attempt * 5))s" >&2
  sleep $((attempt * 5))
done
echo "$sum  .ci/bin/kind" | sha256sum -c -
chmod +x .ci/bin/kind
