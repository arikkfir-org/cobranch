#!/bin/sh
# Lints chart/devhub and validates what it renders: with the bundled database, a host per workload and traces, and with
# an external database, one host with a path per API and no traces. Writes the bundled PostgreSQL's image to
# .ci/postgresql-image, for the cluster to load.
set -eu
chart=chart/devhub
out=.ci/manifests
mkdir -p "$out"
helm lint --strict "$chart" --set blob.url=file:///var/lib/devhub/blobs
helm template devhub "$chart" --namespace devhub --set blob.url=gs://devhub-objects \
  --set urls.console=https://devhub.example.com --set urls.rest=https://api.devhub.example.com \
  --set urls.graphql=https://graphql.devhub.example.com \
  --set httpRoute.enabled=true --set 'httpRoute.parentRefs[0].name=gateway' \
  --set telemetry.traces.endpoint=http://otel-collector.observability:4318 --set telemetry.traces.headersSecret=otlp \
  --set-json telemetry.traces.sampleRatio=0.25 --set telemetry.env.OTEL_RESOURCE_ATTRIBUTES=deployment.environment.name=ci \
  >"$out/routes.yaml"
helm template devhub "$chart" --namespace devhub --set blob.url=s3://devhub-objects \
  --set postgresql.enabled=false --set externalDatabase.existingSecret=devhub-database \
  --set urls.console=https://devhub.example.com --set urls.rest=https://devhub.example.com/api \
  --set urls.graphql=https://devhub.example.com/graphql \
  --set httpRoute.enabled=true --set 'httpRoute.parentRefs[0].name=gateway' >"$out/external.yaml"
kubeconform -strict -summary -kubernetes-version 1.37.0 -schema-location default \
  -schema-location 'https://raw.githubusercontent.com/datreeio/CRDs-catalog/main/{{.Group}}/{{.ResourceKind}}_{{.ResourceAPIVersion}}.json' \
  "$out"/*.yaml
yq '.postgresql.image.repository + ":" + .postgresql.image.tag' "$chart/values.yaml" >.ci/postgresql-image
