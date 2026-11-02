# Cobranch's Helm chart

Installs Cobranch on your Kubernetes cluster: its Auth, REST, GraphQL and Admin servers, the Console and the
Back-office, a Job that migrates the database before each rollout, an hourly maintenance CronJob, and a PostgreSQL unless you bring yours. You bring an
identity provider, and a bucket only if you choose one. Cobranch's designs and
source: <https://github.com/arikkfir-org/cobranch>.

## Install

Every version is published to `oci://me-west1-docker.pkg.dev/arikkfir/cobranch/charts/cobranch`, readable by anyone,
beside its images; the chart's version, its app version and its images' tag are one, such as `1.2.3`
([releases](https://github.com/arikkfir-org/cobranch/releases)).

```bash
helm install cobranch oci://me-west1-docker.pkg.dev/arikkfir/cobranch/charts/cobranch --version 1.2.3 \
  --namespace cobranch --create-namespace
helm test cobranch --namespace cobranch
```

Every value has a default: `values.yaml` lists them, and `values.schema.json` checks them at install.

- **Git's packs** live in the database by default, each a large object, so PostgreSQL is the one store to run and a
  backup of the database holds every repository whole; give `postgresql.storage.size` room for them. For packs that
  outgrow it, set `blob.url` to a cloud's bucket (`s3://`, `gs://` or `azblob://`), with its credentials from
  `blob.envFromSecret` or the Git server's workload identity (`serviceAccount.annotations`); to try Cobranch out, to a
  directory (`file://`) on `blob.volume`, a volume that lasts and that every Git server and maintenance mount
  ([the design](https://github.com/arikkfir-org/cobranch/blob/main/docs/cobranch/designs/git-object-storage.md)). Back
  the database up with `pg_dump`, which takes the large objects with it unless you name schemas or tables, or with your
  operator's snapshots, and restore it as the role Cobranch connects as, which owns the large objects.
- **Moving packs** from one store to another, such as from a bucket into the database: set `blob.url` to the new store
  and `blob.moveFrom` to the old one, as `blob.url` named it; a `file://` one on a lasting volume, never an emptyDir,
  which maintenance can't see. The Git server writes to the new store and reads from the
  old one what the new one lacks, and the maintenance CronJob copies the old store over every hour, each object checked
  by its size and MD5, logging how many are left. Once a run logs that none are, remove `blob.moveFrom`; nothing
  deletes the old store, so delete it when you like. Until then, moving back is the same move the other way round.

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
  `SELECT count(*) FROM pushes WHERE processed_at IS NULL AND retry_at <= now()`. The maintenance CronJob compacts a repository's packs into one once it has `maintenance.compaction.packs` (8), on a disk of `maintenance.storage.sizeLimit` and with the Git server's ServiceAccount for a bucket, and deletes what compaction replaced a day later; null leaves packs alone, as a `file://` URL on a `blob.volume` that is an `emptyDir` does, since only each Git server's own pod holds that directory. Set `urls.gitSsh` too, such as `ssh://git@git.cobranch.example.com`, for Git over SSH with the SSH keys users add in the Console: the Git server also listens on port 2222, behind Service `<fullname>-git-server-ssh` (`<fullname>` is the release's name when it holds `cobranch`, and `<release>-cobranch` otherwise; `gitServer.ssh.service`: ClusterIP on port 22 by default, or LoadBalancer with your cloud's annotations), which no route reaches, so expose it yourself and name its port in `urls.gitSsh` unless it is 22. Its host key is a Secret the chart generates on install and keeps; under Argo CD, or anything else that renders without the cluster, set `gitServer.ssh.hostKey.existingSecret`, a Secret holding a private key in PEM under `key` (`gitServer.ssh.hostKey.secretKey`), such as `ssh-keygen -t ed25519 -N '' -f host_key` writes
  ([the design](https://github.com/arikkfir-org/cobranch/blob/main/docs/cobranch/designs/git-hosting.md)). The REST and GraphQL servers read repositories' trees and files from the Git server's port 8081, behind Service `<fullname>-git-server-internal`, which no route names, each read carrying the viewer's own token, which the Git server checks itself; if your NetworkPolicies select the Git server's pods, admit the REST and GraphQL servers' pods to that port ([the design](https://github.com/arikkfir-org/cobranch/blob/main/docs/cobranch/designs/code-browsing.md)).
- **Quotas.** A user creates 5 organizations for themselves, and an organization holds 100 repositories and stores 1 GiB
  of Git data, its repositories' packs, unless `limits.organizationsPerUser`, `limits.repositoriesPerOrganization` and
  `limits.gitDataPerOrganization` (such as `5Gi`) say otherwise; 0 caps nothing. A creation or a push that would cross
  one is refused with a message that names it, and nothing over it is stored. Organizations an administrator creates
  for a user count against nobody
  ([the design](https://github.com/arikkfir-org/cobranch/blob/main/docs/cobranch/designs/quotas.md)).
- **Secrets key.** The Auth and Admin servers seal the identity providers the Back-office adds, their client secrets,
  with a key the chart generates on install and keeps. Under Argo CD, or anything else that renders without the
  cluster, set `secretsKey.existingSecret`, a Secret holding 32 characters or more under `key` (`secretsKey.secretKey`). A lost key loses those
  client secrets, which administrators then enter again.
- **Database.** The bundled PostgreSQL generates its password on install and keeps it. Under Argo CD, or anything else
  that renders without the cluster, set `postgresql.auth.existingSecret`. For your own database, set
  `postgresql.enabled=false` and `externalDatabase.existingSecret`, a Secret holding its URL.
- **Telemetry.** Logs go to stdout as JSON; set `telemetry.traces.endpoint` to an OTLP endpoint for traces.
- **Demo data.** `seed.enabled` adds Cobranch's demo data after each migration, and lets the sign-in page sign in as
  any of its users with no provider, for a demo installation such as Cobranch's own pull request previews. With the
  Git server, a Job after each install and upgrade also pushes a history to the demo organization's website, unless it
  has one; NetworkPolicies that select the servers must admit the Job (pods labelled `app.kubernetes.io/component: seed`)
  to the Auth, REST and Git servers' port `8080`, or it waits for them until its deadline. It is off by default; leave it
  off.

## Upgrade

```bash
helm upgrade cobranch oci://me-west1-docker.pkg.dev/arikkfir/cobranch/charts/cobranch --version 1.2.4 \
  --namespace cobranch --reset-then-reuse-values
```

`--reset-then-reuse-values` (Helm 3.14 or later) keeps the values you set and takes the new version's defaults for the
rest. `--reuse-values` keeps the previous version's defaults too, so a value a version adds or changes stays as it was:
from a version before the Back-office's settings, it keeps `auth.signUp: anyone` and `organizations.selfServe: true`,
which then lock those settings in the Back-office.

From a version before quotas, the built-in limits apply as soon as the new pods serve: a user who already created 5
organizations, or an organization that already holds 100 repositories or 1 GiB of Git data, keeps what it has and is
refused more. Set `limits` before upgrading to lift them.

The new version's migrate Job brings the schema forward while the old pods serve; the new pods turn ready once it is
done. Each version's migrations keep the previous version's pods working, so roll back one version at most.
