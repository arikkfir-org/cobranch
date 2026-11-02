#!/bin/sh
# Installs the binaries of the throwaway API server the cluster adapter's tests start, kube-apiserver and etcd for
# the node's architecture, in .ci/kube, and fails unless each matches the checksum pinned here, its release's own.
set -eu
kubernetes=v1.37.1
etcd=v3.6.5
case "$(uname -m)" in
x86_64)
  arch=amd64
  apiserver_sum=de9b1b43c10143f448408b3aaf15f44fd5248b97a56301417a1fe2f1fc47a691
  etcd_sum=66bad39ed920f6fc15fd74adcb8bfd38ba9a6412f8c7852d09eb11670e88cac3
  ;;
aarch64)
  arch=arm64
  apiserver_sum=c3c240bc4a0fbe3ad311ac3d74ca1d19ac411628130ea825049550fd856020d9
  etcd_sum=7010161787077b07de29b15b76825ceacbbcedcb77fe2e6832f509be102cab6b
  ;;
*)
  echo "No API server binaries are pinned for $(uname -m)" >&2
  exit 1
  ;;
esac
mkdir -p .ci/kube
curl -fsSLo .ci/kube/kube-apiserver "https://dl.k8s.io/release/$kubernetes/bin/linux/$arch/kube-apiserver"
echo "$apiserver_sum  .ci/kube/kube-apiserver" | sha256sum -c -
curl -fsSLo .ci/kube/etcd.tar.gz "https://github.com/etcd-io/etcd/releases/download/$etcd/etcd-$etcd-linux-$arch.tar.gz"
echo "$etcd_sum  .ci/kube/etcd.tar.gz" | sha256sum -c -
tar -xzf .ci/kube/etcd.tar.gz -C .ci/kube --strip-components=1 "etcd-$etcd-linux-$arch/etcd"
rm .ci/kube/etcd.tar.gz
chmod +x .ci/kube/kube-apiserver .ci/kube/etcd
