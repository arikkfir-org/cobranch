#!/bin/sh
# Lints chart/cobranch with its defaults, packs in the database included, and validates what it renders: with the
# bundled database, packs in a Google Cloud Storage bucket, a host per workload, SSH behind a load balancer, Google and
# traces, and with an external database, packs moving from the database into an S3 bucket, one host with a path per
# workload, SSH with the operator's host key, another provider, an autoscaled push worker, no traces, its own limits
# and the demo data with its seed Job. Writes the bundled PostgreSQL's image to .ci/postgresql-image, for the cluster
# to load.
set -eu
chart=chart/cobranch
out=.ci/manifests
mkdir -p "$out"
helm lint --strict "$chart"
helm template cobranch "$chart" --namespace cobranch --set blob.url=gs://cobranch-objects \
  --set urls.console=https://cobranch.example.com --set urls.rest=https://api.cobranch.example.com \
  --set urls.graphql=https://graphql.cobranch.example.com --set urls.auth=https://auth.cobranch.example.com \
  --set urls.admin=https://admin-api.cobranch.example.com --set urls.backOffice=https://admin.cobranch.example.com \
  --set urls.git=https://git.cobranch.example.com --set urls.gitSsh=ssh://git@git.cobranch.example.com \
  --set gitServer.ssh.service.type=LoadBalancer \
  --set 'auth.providers[0].id=google' --set 'auth.providers[0].name=Google' --set 'auth.providers[0].preset=google' \
  --set 'auth.providers[0].clientId=1234.apps.googleusercontent.com' \
  --set 'auth.providers[0].clientSecret.secretName=cobranch-google' --set 'auth.admins[0]=alice@example.com' \
  --set httpRoute.enabled=true --set 'httpRoute.parentRefs[0].name=gateway' \
  --set telemetry.traces.endpoint=http://otel-collector.observability:4318 --set telemetry.traces.headersSecret=otlp \
  --set-json telemetry.traces.sampleRatio=0.25 --set telemetry.env.OTEL_RESOURCE_ATTRIBUTES=deployment.environment.name=ci \
  >"$out/routes.yaml"
helm template cobranch "$chart" --namespace cobranch --set blob.url=s3://cobranch-objects --set blob.moveFrom=postgres:// \
  --set postgresql.enabled=false --set externalDatabase.existingSecret=cobranch-database \
  --set urls.console=https://cobranch.example.com --set urls.rest=https://cobranch.example.com/api \
  --set urls.graphql=https://cobranch.example.com/graphql --set urls.auth=https://cobranch.example.com/auth \
  --set urls.admin=https://cobranch.example.com/admin --set urls.backOffice=https://cobranch.example.com/back-office \
  --set urls.git=https://cobranch.example.com/git --set pushWorker.replicas=null \
  --set urls.gitSsh=ssh://git@cobranch.example.com:2222 --set gitServer.ssh.service.port=2222 \
  --set gitServer.ssh.hostKey.existingSecret=cobranch-ssh-host-key \
  --set 'auth.providers[0].id=my-idp' --set 'auth.providers[0].issuer=https://idp.example.com' \
  --set 'auth.providers[0].clientId=cobranch' --set 'auth.providers[0].clientSecret.secretName=cobranch-idp' \
  --set 'auth.providers[0].clientSecret.key=secret' --set auth.signUp=domains --set 'auth.signUpDomains[0]=example.com' \
  --set organizations.selfServe=false --set limits.organizationsPerUser=0 --set limits.repositoriesPerOrganization=500 \
  --set limits.gitDataPerOrganization=20Gi --set seed.enabled=true \
  --set httpRoute.enabled=true --set 'httpRoute.parentRefs[0].name=gateway' >"$out/external.yaml"
kubeconform -strict -summary -kubernetes-version 1.37.0 -schema-location default \
  -schema-location 'https://raw.githubusercontent.com/datreeio/CRDs-catalog/main/{{.Group}}/{{.ResourceKind}}_{{.ResourceAPIVersion}}.json' \
  "$out"/*.yaml
yq '.postgresql.image.repository + ":" + .postgresql.image.tag' "$chart/values.yaml" >.ci/postgresql-image
