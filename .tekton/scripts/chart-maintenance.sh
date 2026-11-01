#!/bin/sh
# Runs the maintenance CronJob once on the throwaway cluster, after the tests pushed into Acme's website twice, so it
# compacts the website's two packs (the install sets maintenance.compaction.packs to 2), and fails unless it did.
set -eu
ns=cobranch
job=maintenance-ci
kubectl -n "$ns" create job "$job" --from=cronjob/cobranch-maintenance
for _ in $(seq 60); do
  state=$(kubectl -n "$ns" get job "$job" -o jsonpath='{.status.succeeded}/{.status.conditions[?(@.type=="Failed")].status}')
  case "$state" in
  1/*) break ;;
  */True)
    kubectl -n "$ns" logs "job/$job" --all-containers --prefix
    echo "The maintenance Job failed" >&2
    exit 1
    ;;
  /) sleep 5 ;;
  *)
    echo "The maintenance Job is in a state this script doesn't know: $state" >&2
    exit 1
    ;;
  esac
done
[ "${state%%/*}" = 1 ] || { echo "The maintenance Job didn't finish in 5 minutes" >&2; exit 1; }
logs=$(kubectl -n "$ns" logs "job/$job")
echo "$logs"
echo "$logs" | grep -q '"repositories":1,' || { echo "The maintenance Job compacted no repository" >&2; exit 1; }
