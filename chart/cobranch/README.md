# Cobranch's Helm chart

Installs Cobranch on your Kubernetes cluster: its REST and GraphQL servers and the Console, a Job that migrates the
database before each rollout, and a PostgreSQL unless you bring yours. You bring a bucket. Cobranch's designs and
source: <https://github.com/arikkfir-org/cobranch>.

## Install

Every version is published to `oci://me-west1-docker.pkg.dev/arikkfir/cobranch/charts/cobranch`, readable by anyone,
beside its images; the chart's version, its app version and its images' tag are one, such as `1.2.3`
([releases](https://github.com/arikkfir-org/cobranch/releases)).

```bash
helm install cobranch oci://me-west1-docker.pkg.dev/arikkfir/cobranch/charts/cobranch --version 1.2.3 \
  --namespace cobranch --create-namespace --set blob.url='gs://my-cobranch-bucket'
helm test cobranch --namespace cobranch
```

`blob.url` is the only required value: a Go Cloud URL (`s3://`, `gs://`, `azblob://` or `file://`), with its
credentials from `blob.envFromSecret` or the servers' workload identity (`serviceAccount.annotations`). `values.yaml`
lists every value, and `values.schema.json` checks them at install.

- **Routes.** Every workload serves as if it were alone, so you choose where each lives: a host each, or paths on one
  host (`https://cobranch.example.com/api`), the prefix stripped. Route them from your Gateway, or set `urls.console`,
  `urls.rest` and `urls.graphql` with `httpRoute.enabled` for an HTTPRoute each.
- **Database.** The bundled PostgreSQL generates its password on install and keeps it. Under Argo CD, or anything else
  that renders without the cluster, set `postgresql.auth.existingSecret`. For your own database, set
  `postgresql.enabled=false` and `externalDatabase.existingSecret`, a Secret holding its URL.
- **Telemetry.** Logs go to stdout as JSON; set `telemetry.traces.endpoint` to an OTLP endpoint for traces.
- **Demo data.** `seed.enabled` adds Cobranch's demo data after each migration, for previews of Cobranch's own pull
  requests. It is off by default; leave it off.

## Upgrade

```bash
helm upgrade cobranch oci://me-west1-docker.pkg.dev/arikkfir/cobranch/charts/cobranch --version 1.2.4 \
  --namespace cobranch --reuse-values
```

The new version's migrate Job brings the schema forward while the old pods serve; the new pods turn ready once it is
done. Each version's migrations keep the previous version's pods working, so roll back one version at most.
