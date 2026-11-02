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
wget -qO .ci/bin/kind "https://github.com/kubernetes-sigs/kind/releases/download/$version/kind-linux-$arch"
echo "$sum  .ci/bin/kind" | sha256sum -c -
chmod +x .ci/bin/kind
