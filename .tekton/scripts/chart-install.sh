#!/bin/sh
# Installs chart/cobranch on the throwaway cluster as release cobranch, seeded with the demo data so the test signs in,
# or upgrades it there rolling every workload, and waits for the migrate Job to finish and every pod to be ready. An
# upgrade must keep the generated database password, and its migrate Job find nothing left to apply.
set -eu
action=$1
ns=cobranch
# On failure, what the pods were doing.
diagnose() {
  status=$?
  [ "$status" -ne 0 ] || return 0
  kubectl -n "$ns" get pods -o wide
  kubectl -n "$ns" get events --sort-by=.lastTimestamp | tail -30
  kubectl -n "$ns" logs --all-containers --prefix --tail=30 -l app.kubernetes.io/instance=cobranch
  return "$status"
}
trap diagnose EXIT
set -- --namespace "$ns" --wait --wait-for-jobs --timeout 5m \
  --set images.cobranch.repository=cobranch --set-string "images.cobranch.tag=$VERSION" \
  --set images.cobranch.pullPolicy=Never \
  --set images.console.repository=cobranch-console --set-string "images.console.tag=$VERSION" \
  --set images.console.pullPolicy=Never --set postgresql.image.pullPolicy=Never \
  --set blob.url=file:///var/lib/cobranch/blobs --set-json 'blob.volume={"emptyDir":{}}' \
  --set urls.console=https://cobranch.test --set urls.rest=https://cobranch.test/api --set urls.auth=https://cobranch.test/auth \
  --set seed.enabled=true
password() { kubectl -n "$ns" get secret cobranch-postgresql -o jsonpath='{.data.password}'; }
case "$action" in
install)
  helm install cobranch chart/cobranch --create-namespace "$@"
  ;;
upgrade)
  before=$(password)
  now=$(date +%s)
  helm upgrade cobranch chart/cobranch "$@" \
    --set-string "restServer.podAnnotations.ci/rolled=$now" --set-string "graphqlServer.podAnnotations.ci/rolled=$now" \
    --set-string "console.podAnnotations.ci/rolled=$now" --set-string "migrate.podAnnotations.ci/rolled=$now" \
    --set-string "authServer.podAnnotations.ci/rolled=$now"
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
