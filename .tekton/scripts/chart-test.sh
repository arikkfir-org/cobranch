#!/bin/sh
# Runs the chart's helm test on the throwaway cluster. On failure it also shows what the release's pods logged: the
# test pod tells only what it saw, and a Git server's refusal says why only in the Git server's log.
set -eu
ns=cobranch
if helm test cobranch --namespace="$ns" --logs --timeout=3m; then
  exit 0
fi
kubectl -n "$ns" get pods -o wide
kubectl -n "$ns" logs --all-containers --prefix --tail=30 -l app.kubernetes.io/instance=cobranch
exit 1
