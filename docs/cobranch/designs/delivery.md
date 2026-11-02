# Cobranch delivery

> **Snapshot.** This design lives in its [delivery page](https://claude.ai/artifact/Hhr4cGRUk6JtUDWRAUgWXd), linked with
> Cobranch's other designs; this file is generated from it, with its diagrams, as of 2026-11-02, and synced again once
> Cobranch's shape settles.

**Status**: Proposed 2026-11-02 · [ENG-218](https://linear.app/arikkfir/issue/ENG-218) · Builds the [product’s
delivery](product.md#delivery) on [jobs](https://claude.ai/artifact/FqmP4JjVLtfXc5q4uAZ68y) · A task of the controller ·
Built by [ENG-218](https://linear.app/arikkfir/issue/ENG-218), in five pull requests

Cobranch delivers what a repository declares, like Argo CD but built in: `main`’s `/cobranch.yaml` maps branch patterns
to targets, each a cluster and a namespace; every push to a matching branch renders the branch’s GitOps roots in a
sandboxed pod and applies them to its environment; the Console shows each environment’s revision and health; and
deleting the branch deletes what it created.

## Scope

How Cobranch delivers a repository's branches ([ENG-218](https://linear.app/arikkfir/issue/ENG-218)): the GitOps roots
and targets declared in `/cobranch.yaml`, an environment per target and branch, rendering, applying, pruning and tearing
down, the clusters Cobranch reaches, and each environment's revision and health. The [product
design](product.md#delivery) fixed an Argo CD-like controller built in, an environment per branch, and target clusters
by branch pattern; the [architecture](architecture.md#events-and-background-work) fixed that every reaction is queued
with its change and run by the controller. Delivery runs in the controller
[jobs](https://claude.ai/artifact/FqmP4JjVLtfXc5q4uAZ68y) introduce, from the same branch events, in the same job
namespaces, and with the same secrets.

**This slice delivers every matching branch end to end; promotion, waves and cloud identities come later**

![This slice delivers every matching branch end to end; promotion, waves and cloud identities come later](delivery/scope.svg)

Delivery runs in [ENG-214](https://linear.app/arikkfir/issue/ENG-214)’s controller and job namespaces, from the push
worker’s transaction, beside jobs.

Choices beyond the agreed designs are under [Decisions](#decisions), and four questions for the owner under [Open
questions](#open-questions).

## `/cobranch.yaml`

Delivery lives under `delivery`, beside `repository`, `branches` and `jobs`. Its two keys are read in different places:
`targets` only from `main`, `roots` from the branch being delivered.

```yaml
# /cobranch.yaml
delivery:
  targets:                                    # read from main only
    production:
      branches: [main]
      cluster: 0193a8c2-5c1e-7f20-9d4b-3e1f6a2b7c90   # a secret holding a kubeconfig
      namespace: acme-api
    staging:
      branches: [main]
      cluster: 0193a8c2-71d0-7a44-8e5f-0c2d9b1e4f63
      namespace: acme-api-staging
    preview:
      branches: ["feature/**", "fix/**"]      # no cluster: this installation's own
      namespace: acme-api-${branch.slug}
  roots:                                      # read from the delivered branch's tip
    - path: deploy/chart
      helm:
        release: api                          # the directory's name by default
        valuesFiles: [values.yaml, values-${target}.yaml]
        values:
          image:
            tag: ${commit}
    - path: deploy/overlays/${target}
      kustomize: {}
    - path: deploy/extra                      # plain manifests: every .yaml, .yml and .json under it
      targets: [preview]                      # only these targets; every target by default
      substitute: true                        # ${…} in its files too
```

**Where a branch may deliver comes from main; what it delivers comes from the branch**

![Where a branch may deliver comes from main; what it delivers comes from the branch](delivery/where-read.svg)

The Git server records each tip’s parsed file in the push’s transaction
([ENG-214](https://linear.app/arikkfir/issue/ENG-214)); delivery reads the recorded copy, never Git.

- **One target per name,** the map's key: 1 to 40 lowercase letters, digits and `-`, starting with a letter, since it
  becomes part of the target's principal, its annotation and often a namespace.
- **`branches`** takes the patterns jobs' `branches` take: `*` within a segment, `**` across them. A branch may match
  several targets, and gets an environment in each.
- **`cluster`** is a secret's ID, the secret holding a kubeconfig with one context ([Other clusters](#other-clusters)).
  Without it, the target is this installation's own cluster ([This installation's
  cluster](#this-installations-cluster)).
- **`namespace`** is required, and renders to a DNS label: an environment owns exactly one namespace.
- **A root** is a directory of the commit: `helm` renders the chart there, `kustomize` builds the kustomization there,
  and neither reads every manifest under it, in path order. `targets` limits a root to some targets.
- **Checked strictly,** as the rest of the file is: a push that brings a broken `delivery` into `main` is refused,
  naming the line; on any other branch the push is accepted, and its environments show the error and deliver nothing
  until it is fixed.

### Variables

Strings in `delivery` take `${…}`. A root that sets `substitute: true` takes them in its plain manifests, its values
files and its kustomization's output too, never in a chart's templates, which have Helm's own. `$${…}` writes a literal
`${…}`, and an unknown name, or one with no value, such as `${repository}` where `cobranch render` isn't told it, is an
error.

| Variable | Is | For `preview` of `feature/Login` at `3b7d0c4…` |
| --- | --- | --- |
| `${target}` | The target's name | `preview` |
| `${branch}` | The branch's name | `feature/Login` |
| `${branch.slug}` | The name lowercased, each run of other characters than letters and digits a `-`, trimmed, at most 40 characters | `feature-login` |
| `${commit}`, `${commit.short}` | The commit delivered, whole and its first 7 characters | `3b7d0c4…`, `3b7d0c4` |
| `${namespace}` | The environment's namespace, rendered | `acme-api-feature-login` |
| `${repository}`, `${organization}` | Their slugs | `api`, `acme` |

## Targets and environments

**Agreed** (product): every branch becomes an environment, and `main` is one like any other. Here an **environment** is
a target and a live branch it matches.

**Every live branch a target matches gets an environment of its own**

![Every live branch a target matches gets an environment of its own](delivery/environments.svg)

`main` is one branch like any other: its environments are those its targets give it. A branch matching no target has
none.

- **Made with the push** that creates or moves a matching branch, or with the push into `main` that adds a target or
  widens its `branches`: the push worker's transaction makes the environment and queues its first delivery.
- **One namespace, one environment.** Two environments may not share a cluster and namespace: the second fails, naming
  the first. Two targets naming the same secret count as one cluster.
- **Each target is a principal,** created on first use, kind `target`, owned by the repository, never signing in, as
  each job name is ([Who delivers](#who-delivers)).
- **Ends with its branch,** with its target, or when `main` stops matching the branch: its teardown deletes what it
  created ([Teardown](#teardown)).

## Roots and rendering

**A push queues a delivery; a sandboxed pod renders it; the controller applies it**

![A push queues a delivery; a sandboxed pod renders it; the controller applies it](delivery/render.svg)

A deleted branch, a target that no longer matches, a member’s Redeliver and the periodic resync queue deliveries the
same way; a resync re-applies the last render without rendering again.

- **Rendered in a pod, never in the controller.** A chart's templates and a kustomization are the repository's code:
  they can loop for ever, read files, or fetch remote bases. The controller holds the cluster's credentials, so a
  delivery renders in a pod in the organization's [job
  namespace](https://claude.ai/artifact/FqmP4JjVLtfXc5q4uAZ68y#namespaces), which has no ingress and reaches only DNS
  and the internet, under its LimitRange, as a run does.
- **The render pod** is Cobranch's own image running `cobranch render`, after the
  [checkout](https://claude.ai/artifact/FqmP4JjVLtfXc5q4uAZ68y#pods) init container fetches the commit. It reads the
  roots from the commit's `/cobranch.yaml`, renders each in order, and uploads the manifests with
  `PUT /v1/orgs/{org}/repos/{repo}/deliveries/{id}/manifests`, using a token scoped to that one delivery, `cbd_…`, valid
  for the render's timeout, 10 minutes by default. The REST server stores the render in the object store, under
  `deliveries/<repo-id>/<delivery-id>`, at most 16 MiB.
- **Helm** renders the chart at `path` as `helm template` would, release `release`, namespace the environment's, its
  values from `valuesFiles` (relative to `path`, in order) then `values`. `.Capabilities` are the target cluster's: the
  controller reads its version and API groups before the render and passes them in, so charts that check for an API
  render as they would install. Dependencies come from the chart's `charts/` directory; rendering downloads no chart.
  Hooks and `NOTES.txt` are left out until delivery orders work in waves ([Decisions](#decisions)), and a chart whose
  `kubeVersion` the cluster's version doesn't satisfy fails, as `helm install` does.
- **Kustomize** builds the kustomization at `path`, with no plugins and no Helm inflation. Remote bases work, since the
  pod reaches the internet as a job does.
- **Plain manifests** are every `.yaml`, `.yml` and `.json` under `path`, in path order, each file one or more
  documents.
- **Nothing outside the commit.** A root, a values file, or a link anywhere under a root that leads outside the checkout
  fails the render, and so does a render larger than 16 MiB.
- **`cobranch render`** also runs on a developer's checkout:
  `cobranch render --target preview --branch feature/login --commit "$(git rev-parse HEAD)"` prints, as one YAML stream,
  what Cobranch would apply, from the working tree; `--kube-version` and `--api-versions` stand in for the cluster's.

## Applying

**The controller applies only what the environment may own, in an order that lets CRDs land first**

![The controller applies only what the environment may own, in an order that lets CRDs land first](delivery/apply.svg)

Every check runs before the first apply, so a render with one refused object changes nothing. Pruning deletes only
objects that still carry the environment’s label, and never a CRD.

- **Every object is checked before any is applied.** A namespaced object without a namespace takes the environment's;
  one naming another is refused. A cluster-scoped object is refused in this installation's cluster; in another, the
  kubeconfig's RBAC decides. A `Namespace` is refused everywhere: the environment's namespace is the environment's.
- **Ownership.** Every object carries the label `cobranch.dev/environment` with the environment's ID. An object that
  already exists without it, or with another environment's, is refused: Cobranch never adopts what it didn't create.
- **Order.** Objects apply in Helm's install order by kind, CRDs first; the controller waits up to a minute for each CRD
  to be Established before applying the rest.
- **Server-side apply,** field manager `cobranch`, forcing conflicts: what the repository declares is what the
  environment gets, and fields others set and the repository doesn't stay theirs.
- **Inventory and pruning.** After a whole apply, the environment's inventory becomes the objects just applied, with
  their UIDs, in one transaction; then the objects of the previous inventory that this one lacks are deleted, in reverse
  order, if they still carry the label. CRDs are never pruned: deleting one deletes every object of its kind in the
  cluster.
- **Resync.** Every `delivery.resync` (10 minutes by default) the controller re-applies each environment's last render
  and reads its health again, so a change made by hand is put back. A resync renders nothing and reports no annotation.

## A delivery's life

**A delivery waits, queues, renders, applies and ends; a newer one supersedes it on the way**

![A delivery waits, queues, renders, applies and ends; a newer one supersedes it on the way](delivery/life.svg)

Applying is never interrupted: a newer delivery waits for it, then applies over it. A teardown goes from Queued to
Deleting and ends Succeeded with its environment.

| Delivery | `delivery/<target>` | When |
| --- | --- | --- |
| Waiting | Pending: `waits for a member to start it` | The branch's tip was pushed from outside the organization and `main`'s `repository.outsidePushes` is `wait`, the default ([the gate](https://claude.ai/artifact/FqmP4JjVLtfXc5q4uAZ68y#outside)): a member with `delivery.run` starts it |
| Queued | Pending | Waiting for the controller, or for the environment's running delivery to end |
| Rendering | In progress | Its render pod runs |
| Applying | In progress | The controller applies, prunes and records |
| Succeeded | In progress until Healthy, then Successful | Applied and pruned; the environment is Synced at the commit |
| Failed | Failed, with the reason | A render that failed or timed out, an object refused, an apply the cluster refused, a secret the target may not read |
| Superseded | Failed: `superseded by #n` | A newer delivery of the same environment was queued while it waited, queued or rendered |

- **One at a time per environment.** A controller replica claims a queued delivery `FOR UPDATE SKIP LOCKED` under a
  lease, as runs are claimed, and skips an environment whose delivery is applying. Superseding deletes a rendering
  delivery's pod; an applying one finishes first.
- **Redeliver, start, pause, resume.** `delivery.run` redelivers an environment's branch head, starts a waiting
  delivery, and pauses an environment, which then takes no delivery and no resync until resumed; resuming delivers the
  head. A teardown still runs on a paused environment.
- **Each delivery has a number** in its repository, `#17`, for the Console and the reasons.

## Teardown

**Agreed** (product): deleting the branch deletes what it created.

**Deleting a branch deletes everything its environments created**

![Deleting a branch deletes everything its environments created](delivery/teardown.svg)

A target `main` drops, or a branch it no longer matches, tears down the same way. Teardown deletes only what still
carries the environment’s label, and never a CRD.

- **What it deletes:** every object of the inventory still carrying the environment's label, in reverse install order,
  and then the namespace if Cobranch created it. It waits until each is gone, so finalizers run, up to
  `delivery.teardownTimeout` (10 minutes), then fails with what remains.
- **What it keeps:** CRDs, objects someone relabelled, and the environment's history: its deliveries, renders until
  maintenance deletes them, and its last inventory.
- **A merged branch** keeps its environment until it is deleted, as it keeps its other state.

## Health and revision

**Each object’s status gives the environment its health, and the commit its annotation**

![Each object’s status gives the environment its health, and the commit its annotation](delivery/health.svg)

Health is read after each apply, every 15 seconds while Progressing, and at each resync. A merge requirement may name
`delivery/preview`, so a branch merges only once its preview runs.

- **Revision** is the commit of the environment's last successful delivery; **sync** is Synced when the cluster holds
  that render, Out of sync while a newer commit waits or a delivery failed, and Paused.
- **Health** follows the kstatus rules Kubernetes' own tools share, so Deployments, StatefulSets, DaemonSets, Jobs,
  PersistentVolumeClaims, Services and any object with a `Ready` condition read as they do everywhere else; an object
  without a status is Healthy once it exists.
- **The annotation** `delivery/<target>` on the delivered commit is the target's own, as a job's is its own: a merge
  requirement may name it, and a job may trigger on it, so tests run against a preview once it is up. Job names may not
  start with `delivery/`.

## Who delivers

- **A target is a principal,** one per name in a repository, so the audit log reads "target `production` of `acme/api`".
- **It holds no role**, only Secret reader on the secret its `cluster` names, granted by a principal with
  `org.grants.manage`. A delivery whose target may not read its secret fails before rendering, naming the secret.
- **Its token** `cbd_…` may only upload its delivery's manifests: the render pod can't read the repository, set
  annotations or call any other API.

## Clusters

### This installation's cluster

**In this installation’s cluster, an environment gets a namespace Cobranch makes and fences**

![In this installation’s cluster, an environment gets a namespace Cobranch makes and fences](delivery/in-cluster.svg)

The controller applies as the delivery account, never as itself, so an environment’s manifests can do only what
`<release>-delivery` allows, and only in its own namespace. `delivery.inCluster.enabled: false` renders none of it.

- **The controller makes the namespace,** labelled with the installation and the environment, enforcing the chart's Pod
  Security level, `restricted` by default. A namespace that exists without those labels is refused, so a repository
  can't deliver into `kube-system`, another installation's namespaces or another organization's.
- **It fences it:** NetworkPolicies admitting traffic only from the namespace itself and from the namespaces
  `delivery.inCluster.ingressFrom` selects (the operator's Gateway, typically), and letting it out only to DNS and to
  every address outside `jobs.egress.except`, so an environment reaches neither Cobranch's PostgreSQL nor the cloud's
  metadata server; a LimitRange and an optional ResourceQuota from `delivery.inCluster.resources` and `quota`.
- **It applies as `<release>-delivery`,** a ServiceAccount it impersonates, bound in each environment namespace to the
  ClusterRole of the same name (`admin`'s rules), so a render can do only what a namespace's admin can, there. The
  admission policy keeps that account off the namespace and its guards.
- **`delivery.inCluster.enabled: false`** renders none of it, and targets without a `cluster` fail with
  `this installation delivers to no cluster of its own`.

### Other clusters

**Another cluster is a kubeconfig in a secret, opened only for the target it is granted to**

![Another cluster is a kubeconfig in a secret, opened only for the target it is granted to](delivery/remote.svg)

A branch can’t point a target at another secret: targets come from `main`. A target whose principal may not read its
secret fails each delivery, naming the secret.

- **A kubeconfig of one context**, with a token or a client certificate; exec plugins and auth providers are refused,
  since the controller would run them. The other cluster's RBAC is the boundary: give the kubeconfig's identity what the
  target's namespace needs, and nothing more.
- **The namespace** is created when missing, if the kubeconfig may, and labelled as the in-cluster one is; teardown
  deletes it only if Cobranch created it.
- **Reaching it** is the controller's, never a job pod's: the operator lets the controller's pods reach the clusters'
  API servers.

## Nothing lost

**Agreed** ([architecture](architecture.md#why-no-broker)): every reaction is a row queued with its change, so nothing
is lost while the controller is down.

**Nothing is lost while the controller is down: deliveries wait as rows, and applying again is safe**

![Nothing is lost while the controller is down: deliveries wait as rows, and applying again is safe](delivery/crash-safety.svg)

The inventory is written only after a whole apply, and pruning reads the one before it, so an interrupted delivery never
deletes what it hadn’t replaced. A teardown is safe to repeat: an object already gone counts as deleted.

- **Queued with the change.** The push worker queues deliveries and teardowns in the transaction that processes the
  push, beside the runs it queues, and so does `main`'s push that changes the targets.
- **Leases.** A replica that dies leaves its delivery to the next once its lease expires; the render, once stored, is
  reused.
- **Idempotent.** Applying a render again changes nothing it already applied; pruning reads the last inventory a whole
  apply recorded; a teardown that finds an object gone counts it deleted.

## The Console and APIs

**Environments have pages of their own, and show beside the branches and commits they deliver**

![Environments have pages of their own, and show beside the branches and commits they deliver](delivery/console.svg)

A waiting delivery’s Start button names whose push it would deliver, as a waiting run’s does.

| REST, under `/v1/orgs/{org}/repos/{repo}` | GraphQL | Takes |
| --- | --- | --- |
| `GET /environments`, `GET /environments/{id}` | `Repository.environments`, `Branch.environments` | `repo.read` |
| `GET /environments/{id}/objects`, `/deliveries` | `Environment.objects`, `Environment.deliveries` | `repo.read` |
| `GET /deliveries/{n}`, `/manifests`, `/logs` | `Delivery.manifests`, `Delivery.logs` | `repo.read` |
| `POST /environments/{id}/redeliver`, `/pause`, `/resume` | `redeliver`, `pauseEnvironment`, `resumeEnvironment` | `delivery.run` |
| `POST /deliveries/{n}/start` | `startDelivery` | `delivery.run` |
| `PUT /deliveries/{id}/manifests` | None | The delivery's own `cbd_…` token |

- **`delivery.run`** is a new permission, held by the built-in roles that hold `jobs.run`.
- **An environment's page** shows its objects with their health and kind, its deliveries with each render's manifests
  and its pod's logs, and Redeliver, Pause and Resume.

## The chart

| Value | Default | What it sets |
| --- | --- | --- |
| `delivery.inCluster.enabled` | `true` | Whether targets without a `cluster` deliver into this installation's cluster |
| `delivery.inCluster.podSecurity` | `restricted` | The Pod Security level environment namespaces enforce |
| `delivery.inCluster.ingressFrom` | None | Namespace selectors admitted into environment namespaces, such as the Gateway's |
| `delivery.inCluster.resources`, `quota` | Requests 100m, 128Mi; memory limit 2Gi; no quota | Each environment namespace's LimitRange and ResourceQuota |
| `delivery.render.timeout`, `resources` | 10m; requests 250m, 512Mi, memory limit 2Gi | Render pods |
| `delivery.resync` | 10m | How often each environment is re-applied and its health read |
| `delivery.teardownTimeout` | 10m | How long a teardown waits for objects to go |
| `delivery.renders.keep` | 10 per environment | Renders kept; maintenance deletes older ones |

## Data model

**This slice’s tables: targets, environments, deliveries and what each environment holds**

![This slice’s tables: targets, environments, deliveries and what each environment holds](delivery/er.svg)

None carries trigger `audit()`: targets and environments follow from `/cobranch.yaml` and branches, whose history is
Git’s, and deliveries are history themselves. Every row carries `org_id`.

## Rollout

**Five pull requests, each keeping main working; three wait for a part of jobs**

![Five pull requests, each keeping main working; three wait for a part of jobs](delivery/rollout.svg)

An operator upgrading to the fourth grants the chart’s delivery RBAC, or sets `delivery.inCluster.enabled: false`.

- **The first two need nothing of jobs:** the schema, rendering and `cobranch render`, then applying, pruning and
  health, tested against a real API server. Nothing delivers until the fourth.
- **The fourth needs the operator's consent:** the chart starts granting the controller namespaces, `bind` on the
  delivery roles and impersonation. An operator who can't grant them sets `delivery.inCluster.enabled: false` and
  delivers only to other clusters.
- **A repository adopts delivery** by pushing a `delivery` key into `main`.

## Risks

- **A branch can deliver anything its target's credential allows,** since its roots are its own, until phase 2 pins job
  security for delivery too; `main`'s targets keep a feature branch off production, and the outside-push gate keeps
  strangers out.
- **In this installation's cluster, environments are the operator's capacity and exposure.** Without
  `delivery.inCluster.quota`, one organization's environments can fill the nodes, and a LoadBalancer Service costs
  money: an operator hosting many organizations sets a quota.
- **Fencing needs a cluster that enforces NetworkPolicies,** as jobs' does.
- **Forced server-side apply takes fields back** from controllers that set what the repository also declares, such as an
  autoscaler's `replicas`: leave such fields out of the manifests.
- **CRDs outlive their environments,** by design; they are deleted by hand.

## Decisions

| Proposal | Why | Decision |
| --- | --- | --- |
| `delivery` in `/cobranch.yaml`: `targets` from `main` only, `roots` from the delivered branch | A branch tests its own charts in its own environment, but can't send itself to production | Proposed |
| Targets name a cluster and a namespace by branch pattern; an environment is a target and a live branch | The product's environment per branch and target clusters by branch pattern | Agreed |
| Rendering in a pod in the organization's job namespace, uploading through REST with a delivery-scoped token | Charts and kustomizations are repository code; the controller holds cluster credentials | Proposed |
| Server-side apply with field manager `cobranch`, forcing conflicts, in Helm's kind order | Git is the source of truth; no client-side three-way merge to keep | Proposed |
| Ownership by label; never adopt an object another environment or nobody of Cobranch's created | Two environments, or an operator's objects, are never overwritten | Proposed |
| An inventory per environment, written after a whole apply; pruning and teardown from it; CRDs never deleted | Deletes exactly what Cobranch made, and stays safe after a crash | Proposed |
| Health by kstatus' rules; the environment takes the worst | Rules Kubernetes' tools already share, for every kind | Proposed |
| Each delivery reports `delivery/<target>` on its commit, as the target's principal | Merge requirements and job triggers can follow delivery, with no new mechanism | Proposed |
| One principal per target, holding only Secret reader on its cluster's secret | The jobs model: least privilege, audit by name | Proposed |
| Other clusters through a kubeconfig secret, exec plugins refused | No new credential store; the other cluster's RBAC bounds it | Proposed |
| This installation's cluster on by default, in namespaces the controller makes, fences and applies to as an impersonated, namespace-bound account | Works out of the box like `jobs.pods`, without granting renders anything cluster-wide | Proposed |
| Resync re-applies the last render every 10 minutes | Self-healing, as GitOps promises, without rendering again | Proposed |
| Deliveries are their own queue, claimed under a lease, one at a time per environment | The jobs model; an apply is never interleaved with another | Proposed |
| Waves, hooks, promotion, charts from registries, secrets in manifests, cloud identities, several namespaces and diffs come later | Keeps this slice to its issue | Proposed |

## Open questions

1. **Do targets come from `main` only, and roots from the branch?** Recommended: yes. Roots from `main` only would keep
   a branch from testing its own chart changes; targets from the branch would let it deliver anywhere.
2. **Do renders run in the organization's job namespace, rather than in the controller?** Recommended: yes. It costs a
   pod's start, seconds per delivery, and buys the jobs' sandbox for code the repository controls.
3. **Is delivery into this installation's cluster on by default?** Recommended: yes, fenced as above, with `false` one
   value away for operators who host many organizations or can't grant the RBAC.
4. **Does every environment self-heal by default?** Recommended: yes, every 10 minutes. The alternative, reporting drift
   without fixing it, needs a diff view this slice defers.

Cobranch is a Kfirs project, created by Arik Kfir.
