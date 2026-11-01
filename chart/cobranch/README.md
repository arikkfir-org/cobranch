# Cobranch's Helm chart

Installs Cobranch on your Kubernetes cluster: its Auth, REST, GraphQL and Admin servers, the Console and the
Back-office, a Job that migrates the database before each rollout, an hourly maintenance CronJob, and a PostgreSQL unless you bring yours. You bring a
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
  `urls.rest`, `urls.graphql`, `urls.auth`, `urls.admin`, `urls.backOffice` and `urls.git` with `httpRoute.enabled` for an HTTPRoute each. Keep every URL under one
  parent domain: the session cookie is set for the one host they share, or else for their nearest common parent. The
  Console's pages route in the browser under `urls.console`'s path, so set it when a route mounts the Console at a prefix.
- **Sign-in.** Set `urls.console` and `urls.auth`, the Console's and the Auth server's public URLs: without `urls.auth`
  the Auth server isn't deployed and the Console says sign-in isn't set up. Then list your identity providers in `auth.providers`: any OpenID Connect provider by its issuer, or
  `preset: google`, with the client you registered there (its redirect URI is `urls.auth` plus `/callback`) and a
  Secret of yours holding the client's secret. `auth.signUp` says who may sign up (`anyone`, `domains` or `invited`),
  `auth.admins` lists the first administrators' emails, and `organizations.selfServe` lets every user create
  organizations. Left unset, `auth.signUp` and `organizations.selfServe` are the Back-office's to change (anyone signs
  up, and creates organizations, until an administrator changes them); set, the Back-office only shows them. Without a provider the Console says sign-in isn't set up yet either
  ([the design](https://github.com/arikkfir-org/cobranch/blob/main/docs/cobranch/designs/sign-in-and-organizations.md)).
- **Back-office.** Set `urls.admin` and `urls.backOffice`, together, to run the Admin server and the Back-office, where
  the administrators that `auth.admins` names list and create organizations, enter one (recorded in its audit trail),
  list the users and change the installation's settings and identity providers. Both may stay on a gateway only your
  operators reach ([the design](https://github.com/arikkfir-org/cobranch/blob/main/docs/cobranch/designs/back-office.md)).
- **Git.** Set `urls.git`, the Git server's public URL, to run the Git server and the push worker. Clients clone, fetch
  and push at `urls.git` plus `/<organization>/<repository>.git`, with a personal token as the password. The Git
  server records each push before it answers, and the push worker does what follows, woken by the database: nobody
  calls it, so it has no Service. Keep `gitServer.storage.sizeLimit`, the Git server's disk, above
  `gitServer.cache.size` by a few `gitServer.maxPushSize`. Set `pushWorker.replicas` to null to leave its count to an
  autoscaler, which may scale it to zero while no push waits: KEDA's PostgreSQL scaler, for one, counting
  `SELECT count(*) FROM pushes WHERE processed_at IS NULL AND retry_at <= now()`
  ([the design](https://github.com/arikkfir-org/cobranch/blob/main/docs/cobranch/designs/git-hosting.md)).
- **Secrets key.** The Auth and Admin servers seal the identity providers the Back-office adds, their client secrets,
  with a key the chart generates on install and keeps. Under Argo CD, or anything else that renders without the
  cluster, set `secretsKey.existingSecret`, a Secret holding 32 characters or more under `key` (`secretsKey.secretKey`). A lost key loses those
  client secrets, which administrators then enter again.
- **Database.** The bundled PostgreSQL generates its password on install and keeps it. Under Argo CD, or anything else
  that renders without the cluster, set `postgresql.auth.existingSecret`. For your own database, set
  `postgresql.enabled=false` and `externalDatabase.existingSecret`, a Secret holding its URL.
- **Telemetry.** Logs go to stdout as JSON; set `telemetry.traces.endpoint` to an OTLP endpoint for traces.
- **Demo data.** `seed.enabled` adds Cobranch's demo data after each migration, and lets the sign-in page sign in as
  any of its users with no provider, for a demo installation such as Cobranch's own pull request previews. It is off
  by default; leave it off.

## Upgrade

```bash
helm upgrade cobranch oci://me-west1-docker.pkg.dev/arikkfir/cobranch/charts/cobranch --version 1.2.4 \
  --namespace cobranch --reset-then-reuse-values
```

`--reset-then-reuse-values` (Helm 3.14 or later) keeps the values you set and takes the new version's defaults for the
rest. `--reuse-values` keeps the previous version's defaults too, so a value a version adds or changes stays as it was:
from a version before the Back-office's settings, it keeps `auth.signUp: anyone` and `organizations.selfServe: true`,
which then lock those settings in the Back-office.

The new version's migrate Job brings the schema forward while the old pods serve; the new pods turn ready once it is
done. Each version's migrations keep the previous version's pods working, so roll back one version at most.
