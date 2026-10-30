#!/bin/sh
# Lints chart/cobranch and validates what it renders: with the bundled database, a host per workload and traces, and
# with an external database, one host with a path per API and no traces. Writes the bundled PostgreSQL's image to
# .ci/postgresql-image, for the cluster to load.
set -eu
chart=chart/cobranch
out=.ci/manifests
mkdir -p "$out"
helm lint --strict "$chart" --set blob.url=file:///var/lib/cobranch/blobs
helm template cobranch "$chart" --namespace cobranch --set blob.url=gs://cobranch-objects \
  --set urls.console=https://cobranch.example.com --set urls.rest=https://api.cobranch.example.com \
  --set urls.graphql=https://graphql.cobranch.example.com \
  --set httpRoute.enabled=true --set 'httpRoute.parentRefs[0].name=gateway' \
  --set telemetry.traces.endpoint=http://otel-collector.observability:4318 --set telemetry.traces.headersSecret=otlp \
  --set-json telemetry.traces.sampleRatio=0.25 --set telemetry.env.OTEL_RESOURCE_ATTRIBUTES=deployment.environment.name=ci \
  >"$out/routes.yaml"
helm template cobranch "$chart" --namespace cobranch --set blob.url=s3://cobranch-objects \
  --set postgresql.enabled=false --set externalDatabase.existingSecret=cobranch-database \
  --set urls.console=https://cobranch.example.com --set urls.rest=https://cobranch.example.com/api \
  --set urls.graphql=https://cobranch.example.com/graphql \
  --set httpRoute.enabled=true --set 'httpRoute.parentRefs[0].name=gateway' >"$out/external.yaml"
kubeconform -strict -summary -kubernetes-version 1.37.0 -schema-location default \
  -schema-location 'https://raw.githubusercontent.com/datreeio/CRDs-catalog/main/{{.Group}}/{{.ResourceKind}}_{{.ResourceAPIVersion}}.json' \
  "$out"/*.yaml
yq '.postgresql.image.repository + ":" + .postgresql.image.tag' "$chart/values.yaml" >.ci/postgresql-image
