#!/bin/sh
# Installs chart/devhub on the throwaway cluster as release devhub, or upgrades it there rolling every workload, and
# waits for the migrate Job to finish and every pod to be ready. An upgrade must keep the generated database password,
# and its migrate Job find nothing left to apply.
set -eu
action=$1
ns=devhub
# On failure, what the pods were doing.
diagnose() {
  status=$?
  [ "$status" -ne 0 ] || return 0
  kubectl -n "$ns" get pods -o wide
  kubectl -n "$ns" get events --sort-by=.lastTimestamp | tail -30
  kubectl -n "$ns" logs --all-containers --prefix --tail=30 -l app.kubernetes.io/instance=devhub
  return "$status"
}
trap diagnose EXIT
set -- --namespace "$ns" --wait --wait-for-jobs --timeout 5m \
  --set images.devhub.repository=devhub --set-string "images.devhub.tag=$VERSION" --set images.devhub.pullPolicy=Never \
  --set images.console.repository=devhub-console --set-string "images.console.tag=$VERSION" \
  --set images.console.pullPolicy=Never --set postgresql.image.pullPolicy=Never \
  --set blob.url=file:///var/lib/devhub/blobs --set-json 'blob.volume={"emptyDir":{}}' \
  --set urls.console=https://devhub.test --set urls.rest=https://devhub.test/api
password() { kubectl -n "$ns" get secret devhub-postgresql -o jsonpath='{.data.password}'; }
case "$action" in
install)
  helm install devhub chart/devhub --create-namespace "$@"
  ;;
upgrade)
  before=$(password)
  now=$(date +%s)
  helm upgrade devhub chart/devhub "$@" \
    --set-string "restServer.podAnnotations.ci/rolled=$now" --set-string "graphqlServer.podAnnotations.ci/rolled=$now" \
    --set-string "console.podAnnotations.ci/rolled=$now" --set-string "migrate.podAnnotations.ci/rolled=$now"
  [ "$(password)" = "$before" ] || { echo "The upgrade changed the database's password" >&2; exit 1; }
  jobs=$(kubectl -n "$ns" get jobs -l app.kubernetes.io/component=migrate -o name)
  # Helm deletes the Job the previous release named, and its new one is the only one left.
  [ -n "$jobs" ] && [ "$(echo "$jobs" | wc -l)" -eq 1 ] || { echo "The upgrade left these migrate Jobs: ${jobs:-none}" >&2; exit 1; }
  if kubectl -n "$ns" logs "$jobs" | grep 'Applied a migration'; then
    echo "The upgrade's migrate Job applied a migration again" >&2
    exit 1
  fi
  ;;
*)
  echo "usage: chart-install.sh install|upgrade" >&2
  exit 2
  ;;
esac
kubectl -n "$ns" get pods -o wide
