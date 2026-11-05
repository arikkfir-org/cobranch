# Cobranch jobs

> **Snapshot.** This design lives in its [jobs page](https://claude.ai/artifact/FqmP4JjVLtfXc5q4uAZ68y), linked with
> Cobranch's other designs; this file is generated from it, with its diagrams, as of 2026-11-02, and synced again once
> Cobranch's shape settles.

**Status**: Proposed 2026-11-02 · [ENG-214](https://linear.app/arikkfir/issue/ENG-214) · Builds the [product’s
jobs](product.md#jobs) on [branch status](branch-status.md) · Adds the controller workload · Built by
[ENG-214](https://linear.app/arikkfir/issue/ENG-214), in six pull requests

A repository declares jobs in `/cobranch.yaml`; pushes, tags, annotations, reviews, events, schedules and members
trigger them; each run executes as its job’s own principal, in a pod in its organization’s job namespace or as a signed
webhook elsewhere, receives the secrets its job may read, and reports the annotation of its job’s name, which a branch’s
merge requirements can require.

## Scope

How a repository runs work on its commits ([ENG-214](https://linear.app/arikkfir/issue/ENG-214)): jobs declared in
`/cobranch.yaml`, the events that trigger them, where and as whom a run executes, the secrets it receives, and the
annotations it reports, which a branch's [merge requirements](branch-status.md#merge-requirements) can require. The
[product design](product.md#jobs) fixed the events, the three kinds of work and the override policy; the
[architecture](architecture.md#job-isolation) fixed one namespace per organization and the controller; [principals and
permissions](principals-and-permissions.md#jobs) fixed a principal per job name, the job ceiling and `secrets.read`.
This page builds them on [Git hosting](git-hosting.md#a-push) and [branch status](branch-status.md), and names what it
adds and changes.

**This slice runs jobs end to end; cloud tokens, artifacts and pinned security come later**

![This slice runs jobs end to end; cloud tokens, artifacts and pinned security come later](jobs/scope.svg)

Every later slice builds on the run, its principal and its namespace; agents run their pods beside jobs’.

Choices beyond the agreed designs are under [Decisions](#decisions), and three questions for the owner under [Open
questions](#open-questions).

## `/cobranch.yaml`

Jobs live beside the merge requirements, in the same file. Its three top-level keys are read in different places:
`repository` and `branches` only from `main`, `jobs` from wherever the event happened.

```yaml
# /cobranch.yaml
repository:
  outsidePushes: wait          # wait (the default) or run: the work of tips pushed from outside the organization
  jobs:
    ceiling: Automation        # the repository role every job stays within; Automation by default
branches:
  main:
    requires:
      annotations: [ci/build, ci/test]
jobs:
  ci/build:                    # the job's name: its principal, and the annotation it reports
    on:
      push: {}                 # every branch; or branches: [main, "release/**"]
    script: |
      make build
    image: golang:1.27         # optional; the installation's script image by default
    secrets:
      API_TOKEN: 0193a8c2-5c1e-7f20-9d4b-3e1f6a2b7c90   # an environment variable, from a secret's ID
  ci/test:
    on:
      push: {}
      manual: {}               # a member with jobs.run may start it from the Console
    pod:                       # a Kubernetes pod spec
      containers:
        - name: test
          image: golang:1.27
          command: [make, test]
          resources:
            requests: {cpu: "2", memory: 4Gi}
            limits: {memory: 4Gi}
    timeout: 30m               # 1h by default, 24h at most
  deploy:
    on:
      annotation:
        names: [ci/build, ci/test]
        statuses: [successful]
        branches: [main]
    webhook:
      url: https://deploy.example.com/hooks/cobranch
      signingSecret: 0193a8c2-71d0-7a44-8e5f-0c2d9b1e4f63
  nightly:
    on:
      schedule:
        - cron: "0 3 * * *"    # UTC; only main's file schedules
    script: make nightly
```

**Repository settings come from main; jobs come from where the event happened**

![Repository settings come from main; jobs come from where the event happened](jobs/where-read.svg)

The Git server parses and records each tip’s file in the push’s transaction; everything else reads the recorded copy.

- **One job per name,** the map's key: 1 to 100 lowercase letters, digits and `-`, `_`, `.`, `/`, starting with a letter
  or digit, the [annotation](branch-status.md#annotations) rules, because a job reports the annotation of its own name.
  So `branches.main.requires.annotations: [ci/build]` requires job `ci/build` to pass.
- **Exactly one kind of work:** `pod`, `script` or `webhook` ([Where a run executes](#where-a-run-executes)).
- **`permissions`,** optional: what the run's token may do, within the ceiling; `[repo.read, annotations.write]` by
  default ([Who a run is](#who-a-run-is)).
- **Checked strictly, as the merge requirements are.** Unknown keys, a key named twice, a misspelt trigger, a secret's
  ID that isn't a UUID, a pod spec that names what jobs may not use: a push that brings such a file into `main` is
  refused, naming the line, as a broken `branches` key already is. On any other branch the push is accepted, and the
  branch shows the error and runs nothing until it is fixed.
- **The Git server records each tip's file.** Every push stores, in its transaction, the parsed `/cobranch.yaml` at each
  branch tip it moves, by the file's blob, as the [architecture](architecture.md#settings-secrets-and-models) agreed:
  the push worker, the APIs and the controller read definitions from PostgreSQL, never from Git.

## Triggers

| Trigger | Fires | Definitions from | Runs at |
| --- | --- | --- | --- |
| `push: {branches}` | A push creates or moves a branch matching `branches` (every branch when omitted) | The branch's new tip | The new tip |
| `tag: {tags}` | A push creates or moves a tag matching `tags` (every tag when omitted) | `main`'s tip | The tagged commit |
| `annotation: {names, statuses, branches}` | An annotation of a listed name reaches a listed status (`successful` by default) on the head of a live branch matching `branches` | That head | That head |
| `review: {states, branches}` | A review is submitted on a branch's head, from [ENG-216](https://linear.app/arikkfir/issue/ENG-216) on | That head | That head |
| `event: {names}` | A principal with `jobs.run` posts an event of a listed name | `main`'s tip | The branch the event names, or `main` |
| `schedule: [{cron}]` | Each slot of a five-field cron, in UTC, at most ten minutes late | `main`'s tip | `main`'s tip |
| `manual: {}` | A principal with `jobs.run` starts the job on a branch | That branch's head | That branch's head |

**A push queues its runs with its processing; the controller runs them**

![A push queues its runs with its processing; the controller runs them](jobs/trigger.svg)

Annotations, reviews, events and manual starts queue their runs the same way, in the API’s transaction; schedule slots
in the controller’s.

- **Matching is a query, so the change's own transaction does it.** Definitions are rows already, so the transaction
  that makes a change also queues the runs it triggers, each with its annotation set Pending by the job: the push
  worker's for pushes and tags, the API's for annotations, reviews, events and manual starts, the controller's for
  schedule slots. A branch whose required jobs were just queued goes from Processing to Processing, never through
  Blocked, and nothing is lost between a change and its runs
  ([architecture](architecture.md#events-and-background-work)).
- **Per ref, per job.** A push that moves three branches queues each matching job once per branch, at that branch's new
  tip. A branch deleted, or a tag deleted, triggers nothing.
- **A newer run supersedes.** A run queued for a job and a ref cancels that job's waiting, queued and running runs on
  the same ref at other commits, each with reason `superseded by #n`. At the same commit, a run of the job that hasn't
  ended stands and nothing new is queued, so a second push of a commit, or its tag, runs nothing twice; a Waiting one
  alone gives way, to a run that needn't wait. Supersession is the only concurrency rule of this slice.
- **Chains, never loops.** A run triggered by an annotation that another run set names that run as its cause. A job
  never runs twice in one chain, so `deploy` may follow `ci/build`, but a cycle stops where it would repeat. A job's own
  name in its `annotation.names` is refused.
- **What an annotation trigger sees.** An annotation created, or whose status changes, through the APIs, and a run's own
  annotation as the run starts and ends. The Pending a job's annotation turns to as its run is queued, and the Failed of
  a cancellation or a supersession, trigger nothing, so stopping a run never starts another.
- **Schedules.** Every minute the controller reads `main`'s file in each repository that schedules jobs and queues each
  job's latest slot of the last ten minutes, once, whichever replica gets there first; a slot missed for longer is
  skipped. It forgets the slots it queued after a day.
- **Events carry data.** `POST /v1/orgs/{org}/repos/{repo}/events` takes `{name, branch?, data}`, `data` any JSON up to
  64 KiB, which the run reads as `/cobranch/event.json` and a webhook receives in its body. An outside system posts them
  with an API key holding `jobs.run`.

## Branches and `main`

**Agreed** (product, phase 1): a branch may add or override any job. Here that means the branch's own `/cobranch.yaml`
decides the jobs of its events, whole: its jobs are those its file declares, an override is a job of the same name, and
a job it drops doesn't run on it.

**A branch’s own file decides its jobs; main’s decides everything else**

![A branch’s own file decides its jobs; main’s decides everything else](jobs/override.svg)

A branch’s copy of `main`’s jobs is what it had when it branched or last merged `main`; a dropped job leaves its
required annotation missing, and the branch Blocked.

- **What stays `main`'s.** The `repository` settings (ceiling, outside pushes), the merge requirements, schedules, tags
  and events are read from `main` only, so a branch can't lift its own gate, raise its own ceiling, schedule work, or
  decide what a tag runs.
- **Dropping a job doesn't dodge a requirement.** `main`'s requirements still name the annotation, so the branch stays
  Blocked without it.
- **Overriding one does, until phase 2.** A branch can replace `ci/test` with `exit 0`, and run as the same principal
  with the same secrets. That is the agreed phase 1 trade-off: a job is only as safe as the people who may push to its
  repository. Phase 2's pinned job security lets `main` limit what a branch may override.
- **A branch from before a job existed** doesn't run it until it merges `main` in, and being behind `main` already
  blocks it.

## Who a run is

**Agreed** ([principals](principals-and-permissions.md#jobs)): each job name in a repository is one principal, kept
across runs, so the audit log reads "job `ci/build` of `acme/api`".

**A run may do what its job declares, within main’s ceiling; it reads only the secrets granted to its job**

![A run may do what its job declares, within main’s ceiling; it reads only the secrets granted to its job](jobs/principal.svg)

A job holds no role but Secret reader; everything else it may do, its definition says, and the ceiling caps.

- **Created on first use,** when a run of a new name is first queued: principal kind `job`, owned by the repository,
  never signing in. A renamed job is a new principal; the old one keeps its history and grants.
- **Its permissions come from its declaration, capped by the ceiling.** A run's token may do what its job lists in
  `permissions`, as far as `main`'s `repository.jobs.ceiling` role allows, on its own repository only. The ceiling is
  Automation by default: read, annotate, review, post. A run of a job that lists more than the ceiling fails as it is
  queued, naming the permission; the ceiling is `main`'s at that moment, so a branch can't raise it, and a change to it
  on `main` reaches every branch's next runs.
- **`secrets.read` is its one grant.** **Agreed:** a job receives no role but secrets. A principal with
  `org.grants.manage` grants the new built-in role Secret reader to the job on its repository or on one secret
  ([Secrets](#secrets)).
- **The run token** is a credential of its own, `cbr_…`, made when the run starts and kept as the other credentials are,
  hashed, of kind `run_token`, naming the run; valid until the run ends and never longer than its timeout, at most 24
  hours. The REST server, the GraphQL server and the Git server accept it; its scope is the run's repository. The
  architecture's ID tokens for other clouds come later ([scope](#scope)).
- **Annotations it reports are its own.** The controller sets the job's annotation as the job: Pending when queued, In
  progress when it starts, Successful or Failed when it ends. The run may also set the job's annotation's summary and
  link, and other annotations of its own, with its token.

## A run's life

**A run waits, queues, runs and ends; a newer run or a member can stop it on the way**

![A run waits, queues, runs and ends; a newer run or a member can stop it on the way](jobs/life.svg)

The job’s annotation follows: Pending while Waiting or Queued, In progress while Running, Successful or Failed at the
end, a cancellation reading Failed with its reason. A member with `jobs.run` re-runs a finished run as a new one, the
next attempt.

| Run | Its annotation | When |
| --- | --- | --- |
| Waiting | Pending: `waits for a member to start it` | The tip was pushed from outside the organization, and `main` doesn't let such runs start ([below](#the-outside-push-gate)) |
| Queued | Pending | Waiting for the controller, or for room in the organization's quota |
| Running | In progress | Its pod started, or its webhook was delivered |
| Succeeded | Successful | Exit code 0, or the webhook's receiver reported Successful |
| Failed | Failed, with the reason | Any other exit, a timeout, a pod that vanished, a refusal: a secret the job may not read, a pod spec jobs may not use, an installation that runs no pods |
| Cancelled | Failed: `cancelled by …` or `superseded by #n` | A member with `jobs.run` cancelled it, or a newer run superseded it |

- **Runs are their own queue.** A controller replica claims a queued run `FOR UPDATE SKIP LOCKED` under a lease, as the
  push worker claims pushes; a replica that dies leaves its runs to the next once the lease expires. Replicas poll their
  runs' pods every 5 seconds, and every 5 minutes list every pod they own, so a run whose pod ended while no controller
  ran still ends.
- **Re-run, cancel, start.** `jobs.run` re-runs a finished run as the next attempt of the same definition at the same
  commit, cancels a waiting, queued or running one, and starts a waiting one. Each is an event naming who did it.
- **A re-run** keeps its run's trigger and cause, counts its attempt from the first run, names who re-ran it, and
  supersedes nothing: re-running an older commit's run leaves the newer ones alone. Its job is checked against `main`'s
  ceiling again, and a run of the job at that commit that hasn't ended refuses it.
- **Each run has a number** in its repository, `#42`, for the Console and the reasons.

## The outside-push gate

**Agreed** ([visibility](principals-and-permissions.md#visibility)): a repository setting, off by default, decides
whether jobs run for branch tips pushed from outside the organization.

**A tip pushed from outside the organization runs nothing until a member starts it, unless main allows it**

![A tip pushed from outside the organization runs nothing until a member starts it, unless main allows it](jobs/outside.svg)

A newer push supersedes a waiting run. `manual`, `event`, `tag` and `schedule` runs never wait: a member chose them, or
`main`’s definitions run.

- **Outside** means the push that last moved the branch came from Anonymous, or from a user who isn't a member of the
  organization. A push by a member, an API key, an agent or a job is inside.
- **`repository.outsidePushes: run`** in `main`'s file lets such runs queue like any other. **`wait`**, the default,
  leaves them Waiting, with the job's annotation Pending and the reason, until a member with `jobs.run` starts each one,
  after reading the branch. A newer push supersedes them, waiting or not.
- **What the gate covers:** every run whose definitions come from such a tip: `push`, `annotation` and `review`. A
  `manual` start or an `event` is a member's or a key's own choice, and `tag` and `schedule` run `main`'s definitions.

## Where a run executes

**Agreed** (product): pod specs in Cobranch's own cluster; webhooks for anything elsewhere. A `script` is a pod of one
container.

### Job namespaces

**Agreed** ([architecture](architecture.md#job-isolation)): each organization's runs execute in a namespace of their
own, with no ingress and egress only to the internet, the cluster's DNS and the installation's own servers.

**An organization’s runs execute in a namespace of its own, reaching only the internet, DNS and Cobranch’s servers**

![An organization’s runs execute in a namespace of its own, reaching only the internet, DNS and Cobranch’s servers](jobs/namespace.svg)

The prefix, `<namespace>-jobs-` by default, keeps two installations in one cluster apart; the organization’s ID never
changes. Runs reach the REST, GraphQL and Git servers’ pods on their port, and nothing else inside the cluster. The
namespace names the installation’s controller ClusterRole as its owner, so uninstalling deletes it.

- **Its name** is the chart's `jobs.namespacePrefix`, the release's namespace and `-jobs-` by default, then the
  organization's ID: `cobranch-jobs-0193a8…`. The prefix keeps two installations in one cluster apart, and the ID never
  changes, unlike a slug.
- **The controller creates it** with the organization's first run, labelled with the installation
  (`cobranch.dev/installation`, the release's namespace and name) and the organization, enforcing the chart's Pod
  Security level (`restricted` by default), and in it: the two NetworkPolicies (deny all ingress; allow egress to DNS on
  port 53, at the peers `jobs.egress.dns` names or any address, to the installation's REST, GraphQL and Git servers, and
  to every address outside `jobs.egress.except`, which lists the private, link-local and shared ranges, cloud metadata
  servers included), a LimitRange whose defaults are `jobs.resources`, an optional ResourceQuota from `jobs.quota`, and
  RoleBindings to the chart's ClusterRoles: one lets the controller run pods there, the other lets the REST and GraphQL
  servers read their pods, logs and Secrets. Copies of the installation's `imagePullSecrets` sit beside them, for
  [agents' pods](agents.md#the-agents-pod) alone: a job's pod may not name them.
- **It goes with the organization,** whose deletion deletes it, **and with the installation:** it names the
  installation's controller ClusterRole as its owner, so uninstalling the chart, which deletes that role, has
  Kubernetes's garbage collector delete the namespace and every run in it, with no controller left to clean up.

### Pods and scripts

**The controller starts a run’s pod, follows it and keeps its logs**

![The controller starts a run’s pod, follows it and keeps its logs](jobs/pod-run.svg)

The controller polls every 5 seconds. A timeout deletes the pod and fails the run; a pod that vanished fails it too.
While it runs, the REST and GraphQL servers read its pod’s logs from Kubernetes, masked as the archive is.

- **`pod`** is a Kubernetes pod spec. The controller refuses what would leave the namespace's bounds: a ServiceAccount
  or its token, image pull secrets, which would pull with the installation's credentials, host namespaces, host paths,
  node names, node selectors, affinities, tolerations, priority classes, volumes other than `emptyDir` and ephemeral
  ones, and variables read from the namespace's Secrets or ConfigMaps, which hold the other runs'. Its field names count
  only as Kubernetes spells them: one spelt in another case, or given twice, is refused. It sets what every run needs:
  `restartPolicy: Never`, `activeDeadlineSeconds` from the timeout, the operator's `jobs.nodeSelector`,
  `jobs.tolerations` and `jobs.affinity`, its labels, and no service links.
- **`script`** is shorthand for a pod of one container in `image`, the chart's `jobs.script.image` by default, which is
  Cobranch's own image, carrying Bash and Git, running the script with `bash -eo pipefail`. The script travels in the
  run's Secret, never in the pod's command line.
- **The checkout.** An init container of Cobranch's own image fetches the run's commit into `/workspace`, which every
  container mounts, over Git's HTTPS URL with the run's token; `checkout: false` skips it.
- **What every container gets:** `/workspace`, `/cobranch/event.json`, and `COBRANCH_URL`, `COBRANCH_GIT_URL`,
  `COBRANCH_TOKEN`, `COBRANCH_ORG`, `COBRANCH_REPO`, `COBRANCH_SHA`, `COBRANCH_REF`, `COBRANCH_JOB`, `COBRANCH_RUN` and
  `COBRANCH_EVENT`, plus each secret the job names, as the environment variable it names. `COBRANCH_URL` and
  `COBRANCH_GIT_URL` are the installation's Services in the cluster unless `jobs.urls` names others. The token and the
  secrets come from the run's Secret, which the controller writes once the pod exists, owned by it, so deleting the pod
  deletes it.
- **Logs.** When the pod ends, the controller archives each container's log to the object store, under
  `logs/<repo-id>/<run-id>/<container>`, up to `jobs.logs.maxBytes` each, masking the run's secret values. While the run
  runs, the REST and GraphQL servers read its pod's logs from Kubernetes, up to `jobs.logs.maxBytes` each, masked with
  the values its Secret lists, and the Console reads them again every five seconds; maintenance deletes archived logs
  after `jobs.logs.retention`.
- **Following runs.** Every `JOBS_POLL_INTERVAL` (5 s) the controller claims queued runs and reads its pods' states;
  every `JOBS_SWEEP_INTERVAL` (5 min) it deletes what ended without it: pods of runs cancelled or superseded, namespaces
  of deleted organizations, and expired run tokens. A pod left by an earlier attempt at a run is deleted, and the run
  tried again once its lease ends.
- **Room.** A pod the quota refuses keeps its run Queued, retried as runs end, with the reason on the run.

### Webhooks

**A webhook job hands its run to a system elsewhere, which reports back as the job**

![A webhook job hands its run to a system elsewhere, which reports back as the job](jobs/webhook.svg)

A receiver that never reports leaves the run to its timeout. Webhooks receive no secrets but the signature.

- **The controller posts** the run as JSON (the run, its job, repository, commit, ref and event, `event.json`'s data,
  the run's token and when it expires, and the REST URL), signed with HMAC-SHA256 over the body with the value of
  `signingSecret`, a secret the job must be allowed to read, in `X-Cobranch-Signature: sha256=…`.
- **Delivered once 2xx;** retried after a failure or a timeout of ten seconds, a second after the first and twice as
  long after each, for as long as the run's timeout allows. Redirects aren't followed.
- **The receiver reports through the REST API,** with the run's token: it sets the job's annotation, In progress while
  it works, and Successful or Failed, which ends the run. A receiver that never reports leaves the run to its timeout.
- **Never into the cluster.** The URL must be `https`, and its host must resolve outside `jobs.egress.except`, checked
  at the address the controller then connects to, so a webhook reaches what a job pod could, and not Cobranch's own
  Services or the cloud's metadata server. An operator who needs a private receiver lists its range in
  `jobs.webhooks.allowedCIDRs`.

## Secrets

**Agreed** ([principals](principals-and-permissions.md#secrets),
[architecture](architecture.md#settings-secrets-and-models)): secrets live encrypted in organization and repository
settings, `/cobranch.yaml` names them by ID, and `secrets.read` decides which jobs receive them.

**A secret is set once in settings and reaches only the runs whose job may read it**

![A secret is set once in settings and reaches only the runs whose job may read it](jobs/secrets.svg)

A grant can name one secret and reach only it, as a cloud’s IAM policy set on one resource does.

- **Where they live.** An organization's settings hold the secrets its repositories share; a repository's its own. Each
  has an ID, a name for people, and a value sealed with the installation's secrets key, bound to its row, as identity
  providers' client secrets already are. `secrets.manage` adds, replaces and deletes them from the Console's settings
  pages or the APIs; no API, page or log returns a value once written.
- **Named by ID** in `secrets` and `signingSecret`, so renaming a secret, or an organization and a repository each
  holding one of the same name, changes nothing.
- **Granted with Secret reader,** a new built-in role of repositories holding `secrets.read` alone: on a repository it
  reaches the repository's secrets, and on one secret, which becomes a scope for secret permissions only as the
  principals design decided, that secret alone. An organization's own secrets reach a job through a grant on each; a
  grant at the organization waits for organization roles that jobs can hold. Granting takes `org.grants.manage` and
  `secrets.read` on the secret.
- **A job is granted once it is a principal,** from its first queued run: the repository's Secrets tab lists its jobs,
  and a run that names a secret its job may not read fails, naming it, until someone grants it.
- **Checked at the start of every run.** The controller opens each secret the job names, if the job holds `secrets.read`
  on it, and writes them into the run's Secret; otherwise the run fails, naming the secret, before anything runs.

## The chart

The chart adds one workload and the Kubernetes access it needs.

**The controller binds itself only where it made a job namespace, and an admission policy holds it there**

![The controller binds itself only where it made a job namespace, and an admission policy holds it there](jobs/rbac.svg)

Cluster-scoped names start with the release’s namespace, so two installations of one release name never share them; the
job namespaces name the controller’s role as their owner, so uninstalling deletes them; the servers mount a token for
`jobs-logs`. `jobs.pods: false` renders none of it: the controller still delivers webhooks; pods and scripts don’t run.

| Object | What it allows |
| --- | --- |
| Deployment `<release>-controller` | 2 replicas, a disruption budget of one, rolling out by surging; the only workload with Kubernetes write access |
| ClusterRole and binding `<namespace>-<release>-controller` | Create and delete namespaces, which name this role as their owner; create RoleBindings, and `bind` only the ClusterRoles below; read itself |
| Role and binding `<release>-controller-image-pull-secrets` | Read only the Secrets `imagePullSecrets` names, in the release's namespace, to copy them into each job namespace; rendered only when it names any |
| ClusterRole `<namespace>-<release>-jobs-runner` | Pods, their logs, Secrets, NetworkPolicies, LimitRanges and ResourceQuotas, bound to the controller in each job namespace |
| ClusterRole `<namespace>-<release>-jobs-logs` | Read pods, their logs and Secrets, the last for the values the logs mask, bound to the servers' ServiceAccount in each job namespace, whose token the REST and GraphQL servers mount |
| ValidatingAdmissionPolicy `<namespace>-<release>-jobs` | Holds the controller to namespaces carrying its prefix and installation label, and its RoleBindings to the runner role for itself and the logs role for the servers |

- **No cluster-wide reach beyond namespaces.** The controller reads and writes pods and Secrets only where it bound
  itself, and the admission policy keeps it from binding itself anywhere else. It needs Kubernetes 1.30 or later.
  Cluster-scoped objects carry the release's namespace in their names, so two installations of one release name never
  share them; `jobs.admissionPolicy: false` leaves the holding to the cluster's own policies.
- **`jobs.pods: false`** renders none of the RBAC and no policy, for an operator who can't grant it: the controller
  still runs, without a Kubernetes token, and delivers webhooks; pod and script runs fail with
  `this installation runs no pods`, and no server mounts a token.

| Value | Default | What it sets |
| --- | --- | --- |
| `controller.replicas`, `resources`, placement | 2 | The controller workload |
| `jobs.pods` | `true` | Whether runs execute pods at all |
| `jobs.namespacePrefix` | `<namespace>-jobs-` | Job namespaces' names |
| `jobs.namespaceLabels` | None | More labels on job namespaces, for the cluster's own policies |
| `jobs.podSecurity` | `restricted` | The Pod Security level job namespaces enforce |
| `jobs.egress.except` | Private, link-local and shared ranges | Where jobs and webhooks may not connect |
| `jobs.egress.dns` | Any address, on port 53 | What the DNS policy admits |
| `jobs.resources` | Requests 250m, 512Mi; memory limit 2Gi | The LimitRange's defaults |
| `jobs.quota` | None | A ResourceQuota per job namespace |
| `jobs.nodeSelector`, `tolerations`, `affinity` | None | Where job pods run |
| `jobs.script.image` | Cobranch's own image | `script`'s default image |
| `jobs.urls.rest`, `git` | The Services in the cluster | Where runs reach the REST API and the Git server |
| `jobs.timeout.default`, `max` | 1h, 24h | Runs' timeouts |
| `jobs.logs.maxBytes`, `retention` | 10 MiB, `720h` (30 days) | The most of each container's log kept or read live; how long maintenance keeps them |
| `jobs.webhooks.allowedCIDRs` | None | Private ranges a webhook may reach |
| `jobs.admissionPolicy` | `true` | Whether the chart ships its admission policy |

## The Console and APIs

**Runs show on a branch’s Jobs tab and a commit’s page, and each has a page of its own**

![Runs show on a branch’s Jobs tab and a commit’s page, and each has a page of its own](jobs/console.svg)

A waiting run’s Start button says whose push it would run, so a member reads the branch before starting it.

| REST, under `/v1/orgs/{org}` | GraphQL | Takes |
| --- | --- | --- |
| `GET /repos/{repo}/runs?sha=`, `GET …/runs/{n}` | `Repository.runs(sha:)`, `Repository.run` | `repo.read` |
| `GET …/runs/{n}/logs`, `GET …/runs/{n}/logs/{container}` (text) | `Run.logs` | `repo.read` |
| `POST …/runs/{n}/start`, `/cancel`, `/rerun` | `startRun`, `cancelRun`, `rerunRun` | `jobs.run` |
| `POST /repos/{repo}/branches/{branch}/jobs/{job}/runs` | `runJob` | `jobs.run`, the job declaring `manual` |
| `POST /repos/{repo}/events` | `postEvent` | `jobs.run` |
| `GET`, `POST /secrets`; `GET`, `PUT`, `DELETE /secrets/{id}` | `Organization.secrets`, `createSecret`, `updateSecret`, `deleteSecret` | `secrets.manage` where it lives; names, never values; a change names its ETag in `If-Match` |
| `GET`, `POST /repos/{repo}/secrets` | `Repository.secrets`, `createSecret(repositoryId:)` | `secrets.manage` on the repository |
| `GET`, `POST /secrets/{id}/access`, `DELETE …/access/{principal}` | `Secret.access`, `grantSecret`, `revokeSecret` | reading: `secrets.manage` on it or `org.grants.manage`; granting: `org.grants.manage` and `secrets.read` on it |
| `GET /repos/{repo}/jobs` | `Repository.jobPrincipals` | `repo.read` |

- **A repository's Jobs tab** lists its runs, newest first; **a commit's page** lists the runs at the commit beneath
  its annotations, and **a branch's Jobs tab** (`?tab=jobs`) those on its head, with Start for a waiting run, Run for a
  manual job, and Re-run and Cancel.
- **A run's page** shows its trigger, its cause, its definition as it ran, its timeline and its logs, live while it
  runs.
- **An organization's Secrets page and a repository's Secrets tab** list their secrets, by name, with who is granted
  each, add, change and delete them, and grant them to the organization's members, API keys, jobs and agents.

## Data model

**This slice’s tables: recorded settings, job principals, runs and secrets**

![This slice’s tables: recorded settings, job principals, runs and secrets](jobs/er.svg)

`secrets` and `grants` carry trigger `audit()`, the value kept out of the log; runs are history, not data people manage,
so they don’t. Every new row carries `org_id`.

## Rollout

**Six pull requests, each keeping main working**

![Six pull requests, each keeping main working](jobs/rollout.svg)

Upgrading to the fourth needs Kubernetes 1.30 for the admission policy, or `jobs.pods: false`.

- **Each pull request keeps `main` working** and is Part of [ENG-214](https://linear.app/arikkfir/issue/ENG-214); the
  sixth closes it. Runs queued by the first wait for the controller the fourth brings; nothing requires a job's
  annotation until a repository says so in its own `/cobranch.yaml`.
- **The first** records each tip's `/cobranch.yaml`, makes job principals and queues runs from pushes and tags, with the
  gate, the ceiling and supersession, and lists, starts and cancels runs through the APIs and the Console. Re-runs and
  manual starts come with the fifth, beside the other triggers.
- **The second** keeps secrets in organization and repository settings, adds Secret reader, and manages and grants them
  through the APIs and the Console, so jobs can be granted theirs before the controller runs them.
- **The third declares the chart's `controller` and `jobs` values,** which place nothing, so an operator sets the values
  their cluster needs before the controller arrives: the values schema refuses keys the chart doesn't know.
- **The fourth needs the operator's consent:** the chart starts installing cluster-scoped RBAC and an admission policy,
  and the controller runs pods and scripts, with the checkout, secrets and archived logs. An operator who can't grant
  them sets `jobs.pods: false`.
- **The fifth brings the remaining triggers:** annotations, reviews, posted events, manual starts and schedules, and
  re-runs, through the APIs and the Console.
- **The sixth** delivers webhooks and adds the logs API, with its `jobs-logs` role, and the archived logs' retention.
- **A repository adopts jobs** by pushing a `jobs` key into `main`, and requires them by naming their annotations under
  `branches`.

## Risks

- **A branch can rewrite a job and keep its secrets,** until phase 2 pins job security; the outside-push gate keeps
  strangers out, members are trusted.
- **Isolation needs a cluster that enforces NetworkPolicies.** On one that doesn't, a job reaches the cluster's
  Services; the chart can't detect it, so its notes say so.
- **A webhook that never reports** holds its run, and a required annotation, until the timeout; the run's page shows it
  waiting for its receiver.
- **The REST and GraphQL servers hold a Kubernetes token** where runs execute pods: it reads the job namespaces' pods,
  logs and Secrets, the runs' tokens among them, which a compromised server could use for their lifetimes.
- **Job pods are the operator's capacity.** Without `jobs.quota`, one organization's runs can fill the nodes
  `jobs.nodeSelector` names.
- **Pod Security `restricted` refuses images that need root;** an operator who wants them lowers `jobs.podSecurity` to
  `baseline`.
- **Recreating the controller's ClusterRole deletes the job namespaces,** since they name the role they were made under:
  runs then in progress fail when their pods vanish, and each organization's next run makes its namespace again. Only an
  uninstall deletes the role.

## Decisions

| Proposal | Why | Decision |
| --- | --- | --- |
| `/cobranch.yaml` holds jobs under `jobs`, repository settings under `repository`, beside `branches`; checked strictly, a broken file refused on `main` | Settings live in Git; one file, one parser, typos caught | Proposed |
| A job reports the annotation of its own name, so job names follow the annotation rules | Merge requirements name annotations; one name for both | Proposed |
| Push, annotation, review and manual runs read the branch's own file; tags, events and schedules read `main`'s; repository settings come from `main` only | Phase 1 lets a branch add or override any job; a tag names any commit; a branch must not lift its own gate or ceiling | Agreed |
| Cron triggers come from `main`'s file only | A branch cannot schedule work | Agreed |
| The Git server records each tip's parsed file in the push's transaction | The architecture's "each push records each tip's file"; only the Git server reads Git objects | Agreed |
| The changing transaction queues its runs, with Pending annotations | Nothing lost, no Blocked flicker; definitions are already rows | Proposed |
| Runs are their own queue, claimed under a lease; the architecture's general `tasks` table waits for its first general subscriber | Less to build now; the same claiming pattern | Changed |
| A newer run of a job on a ref supersedes its runs at other commits; at the same commit one that hasn't ended stands, a Waiting one giving way to a run that needn't wait | The only concurrency rule a first slice needs; superseding at the same commit would let an outsider's push of a member's commit cancel the member's run | Changed |
| A job never runs twice in one chain of annotation triggers | Chains such as build then deploy, without loops | Proposed |
| Annotations trigger runs when the APIs set them or a run starts or ends; queueing, cancelling and superseding don't | Stopping a run must not start another; Cobranch's own Pending is bookkeeping, not news | Changed |
| A re-run supersedes nothing and keeps its run's trigger and cause | A member re-running an older run means that run, not the newest; its chain stays one chain | Changed |
| A schedule slot runs at most ten minutes late, once across replicas, else is skipped | A controller that was down doesn't fire a backlog of slots; two replicas don't fire one twice | Changed |
| One principal per job name; permissions from its declaration within `main`'s ceiling, Automation by default; `secrets.read` its one grant, through the new Secret reader role | The principals design | Agreed |
| Run tokens `cbr_…`, valid for the run, at most 24 hours, scoped to its repository | The principals design's lifetimes | Agreed |
| Job namespaces are `<prefix><organization ID>`, the prefix a chart value, `<namespace>-jobs-` by default; cluster-scoped objects are named `<namespace>-<release>-…` | Two installations in one cluster must not share namespaces or roles, even under one release name; IDs never change | Changed |
| The controller polls, every 5 seconds for runs and pods and every 5 minutes for what ended without it, rather than watching | One query and one list per pass; any replica may follow any run; a missed event can't strand a run | Changed |
| Scripts run in Cobranch's own image by default, which carries Bash and Git | One image the installation already pulls, pinned to its version | Changed |
| Runs reach the installation's REST, GraphQL and Git servers in the cluster, through an egress rule for those pods alone | Inside a cluster, its public URL may not resolve or may route through a gateway runs can't reach | Changed |
| The LimitRange sets containers' defaults only; the quota bounds an organization | A maximum would refuse a job that declares more; the quota already caps the whole | Changed |
| The controller binds itself per job namespace, held there by an admission policy the chart ships; `jobs.pods: false` turns pods off | The narrowest RBAC Kubernetes can express for namespaces made at run time | Proposed |
| Job namespaces enforce Pod Security `restricted` by default | Least privilege by default; the operator may lower it | Proposed |
| Job namespaces name the installation's controller ClusterRole as their owner | Uninstalling deletes them and their runs through Kubernetes's garbage collector; a cleanup the controller ran would never run once it is uninstalled | Changed |
| A script is a pod of one container; the checkout is an init container; secrets come as environment variables | One way to run code; scripts need the code | Proposed |
| Webhooks are signed with a secret the job may read, delivered by the controller, refused inside the cluster, and report back through the API | The architecture's signed, retried webhooks; no SSRF into Cobranch | Proposed |
| The servers mask a live log with the values the run's Secret lists, which their logs role reads, rather than the secrets its job may read now | The values the pod holds, its token's among them, even after a secret changes or a grant goes; the servers hold the secrets key already | Changed |
| Outside pushes leave runs Waiting until a member starts them, unless `main` says `run`, under `repository.outsidePushes` | The principals design's gate; a setting of the repository, since more than jobs will follow a push | Agreed |
| A job's ceiling is checked as each run is queued, against `main`'s ceiling then | One check for every branch; `main`'s file needn't be refused for a job it can't run | Changed |
| Run tokens are credentials of kind `run_token`, beside personal tokens and API keys | One way to authenticate a bearer token | Changed |
| ID tokens for clouds, artifacts, caches, path filters, other concurrency policies and organization-wide jobs come later | Keeps this slice to its issue | Proposed |

## Open questions

Taken on their recommendations on 2026-11-02, while the owner was away, so the work could go on; any of them can be
revisited.

1. **Should a branch's own file decide its jobs whole?** Taken: yes. The alternative, `main`'s jobs merged with the
   branch's, makes dropping a job impossible and reading a branch's jobs harder, and buys nothing a requirement doesn't
   already hold.
2. **Pod Security `restricted` by default for job namespaces?** Taken: yes, with `baseline` one value away. Many CI
   images run as root and fail under `restricted`; an installation that runs such images lowers it knowingly.
3. **Do webhooks report only through the API?** Taken: yes. A receiver that answers with a verdict in its response would
   need a second path, and the run's token already lets it report, as any CI system reports annotations today.

Cobranch is a Kfirs project, created by Arik Kfir.
