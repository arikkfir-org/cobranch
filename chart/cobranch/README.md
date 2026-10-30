# Cobranch's Helm chart

Installs Cobranch on your Kubernetes cluster: its Auth, REST and GraphQL servers and the Console, a Job that migrates
the database before each rollout, an hourly maintenance CronJob, and a PostgreSQL unless you bring yours. You bring a
bucket and an identity provider. Cobranch's designs and
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
  `urls.rest`, `urls.graphql` and `urls.auth` with `httpRoute.enabled` for an HTTPRoute each. Keep every URL under one
  parent domain: the session cookie is set for the one host they share, or else for their nearest common parent.
- **Sign-in.** Set `urls.console` and `urls.auth`, the Console's and the Auth server's public URLs: without `urls.auth`
  the Auth server isn't deployed and the Console says sign-in isn't set up. Then list your identity providers in `auth.providers`: any OpenID Connect provider by its issuer, or
  `preset: google`, with the client you registered there (its redirect URI is `urls.auth` plus `/callback`) and a
  Secret of yours holding the client's secret. `auth.signUp` says who may sign up (`anyone`, `domains` or `invited`),
  `auth.admins` lists the first administrators' emails, and `organizations.selfServe` lets every user create
  organizations. Without a provider the Console says sign-in isn't set up yet either
  ([the design](https://github.com/arikkfir-org/cobranch/blob/main/docs/cobranch/designs/sign-in-and-organizations.md)).
- **Database.** The bundled PostgreSQL generates its password on install and keeps it. Under Argo CD, or anything else
  that renders without the cluster, set `postgresql.auth.existingSecret`. For your own database, set
  `postgresql.enabled=false` and `externalDatabase.existingSecret`, a Secret holding its URL.
- **Telemetry.** Logs go to stdout as JSON; set `telemetry.traces.endpoint` to an OTLP endpoint for traces.
- **Demo data.** `seed.enabled` adds Cobranch's demo data after each migration, and lets the sign-in page sign in as
  any of its users with no provider, for previews of Cobranch's own pull requests. It is off by default; leave it off.

## Upgrade

```bash
helm upgrade cobranch oci://me-west1-docker.pkg.dev/arikkfir/cobranch/charts/cobranch --version 1.2.4 \
  --namespace cobranch --reuse-values
```

The new version's migrate Job brings the schema forward while the old pods serve; the new pods turn ready once it is
done. Each version's migrations keep the previous version's pods working, so roll back one version at most.
