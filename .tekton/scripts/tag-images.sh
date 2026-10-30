#!/bin/sh
# Adds TAG's version (v1.2.3 is 1.2.3, the chart's version) to images devhub and devhub-console of COMMIT, which
# pipeline main published under its short SHA; nothing is rebuilt. gcloud authenticates as the pod's Workload Identity.
set -eu
sha=$(printf '%s' "$COMMIT" | cut -c1-7)
version=${TAG#v}
for image in devhub devhub-console; do
  tries=0
  # The commit's images may still be building when its tag arrives. Only "not found" means that: any other error
  # (permission, auth, network) ends the run at once.
  until out=$(gcloud artifacts docker images describe "$REGISTRY/$image:$sha" --quiet 2>&1); do
    printf '%s' "$out" | grep -Eiq 'not[ _]found' || { echo "$out" >&2; exit 1; }
    tries=$((tries + 1))
    if [ "$tries" -gt 60 ]; then
      echo "$REGISTRY/$image:$sha is not published after 15 minutes: main's build failed, or built a newer commit instead" >&2
      exit 1
    fi
    echo "Waiting for $REGISTRY/$image:$sha"
    sleep 15
  done
  echo "Tagging $REGISTRY/$image:$sha as $version"
  gcloud artifacts docker tags add "$REGISTRY/$image:$sha" "$REGISTRY/$image:$version" --quiet
done
