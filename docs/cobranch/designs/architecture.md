# Cobranch technical architecture

> **Snapshot.** This design lives in its [architecture page](https://claude.ai/artifact/L6NYy7k4ep31FEPAxQiPjv), linked
> with Cobranch's other designs; this file is generated from it, with its diagrams, as of 2026-11-02, and synced again
> once Cobranch's shape settles.

**Status**: Agreed 2026-10-27 · [ENG-193](https://linear.app/arikkfir/issue/ENG-193) ·
[ENG-203](https://linear.app/arikkfir/issue/ENG-203) · Revised after review, 2026-10-27 · Builds on the [product
design](product.md#architecture) · Detailed by the [Helm chart](https://claude.ai/artifact/LyMHXgSoNQNgHtHNfEjvA8)

Cobranch runs as a handful of Go workloads over one store, PostgreSQL, which keeps Git’s packs too unless the operator
names a bucket, and a Git push returns only after PostgreSQL has committed it. Everything else here follows from those
two choices.

## Scope

The technical design of Cobranch's foundations ([ENG-193](https://linear.app/arikkfir/issue/ENG-193)): the workloads and
how they talk, the data they keep, the push path, the API conventions, blob storage and the stack. It turns the
Architecture of the [product design](product.md#architecture) into parts.

Each of these has a design of its own and only attaches here: [principals and
permissions](principals-and-permissions.md) ([ENG-196](https://linear.app/arikkfir/issue/ENG-196)), the [Helm
chart](https://claude.ai/artifact/LyMHXgSoNQNgHtHNfEjvA8) ([ENG-195](https://linear.app/arikkfir/issue/ENG-195)), and
later jobs and CI, delivery, agents and AI review, issues, and Slack. Choices this page makes beyond the product design
are marked **Agreed** and collected under [Decisions](#decisions).

## Constraints

Seven constraints from the [product design](product.md#principles) decide most of the architecture.

- **One Helm chart, any cluster.** No cloud's API is called directly; the operator brings the domain, ingress, TLS and
  autoscaling.
- **Stateless workloads.** Any pod can be killed at any time. State lives only in PostgreSQL and the object store; a
  pod's disk is a cache.
- **Few moving parts.** An operator runs PostgreSQL and nothing else; **Changed** (2026-11-01, [Git object
  storage](git-object-storage.md)): the object store is PostgreSQL's large objects unless they name a bucket.
  **Agreed:** no message broker, no Redis, no search cluster in the foundations; phase 2's semantic search keeps its
  embeddings in PostgreSQL with pgvector ([phase 2](product.md#phase-2)).
- **Synchronous pushes.** When `git push` returns, its commits and branches are in PostgreSQL and on screen. **Changed**
  (2026-11-01, [Git hosting](git-hosting.md#a-push)): their principals and status follow from the push worker, so only
  the Git server is on a push's path.
- **Many organizations per installation.** Every row an organization owns carries its ID, and every query is scoped by
  it.
- **One permission model.** REST, GraphQL, the Admin API and Git check the same grants through the same code.
- **Agents use the front door.** Agents, jobs and AI review call the same APIs as people, as principals of their own.

## Components

Ten workloads, plus the job and agent pods the controller creates and a migration Job per upgrade. Only the Git server
reads Git objects or moves refs, and only the controller makes and watches pods; the REST and GraphQL servers' one
Kubernetes call reads a running job's logs.

**All state lives in PostgreSQL and the object store, itself PostgreSQL by default**

![All state lives in PostgreSQL and the object store, itself PostgreSQL by default](architecture/components.svg)

The **controller** is a workload the product design did not list: the push worker only records what follows a push, so
the slow, retried and outward-facing work (pods, webhooks, model calls) gets a workload of its own.

**Four API workloads.** **Changed** (review, 2026-10-27): the API server is now the Auth, GraphQL, REST and Admin
servers, so the operator routes, scales and exposes each on its own ([Routes](#routes)). They share one service layer
and check the same grants. The [Helm chart](https://claude.ai/artifact/LyMHXgSoNQNgHtHNfEjvA8)'s first slice
([ENG-195](https://linear.app/arikkfir/issue/ENG-195)) brings the GraphQL and REST servers; the Auth server arrives with
sign-in ([ENG-197](https://linear.app/arikkfir/issue/ENG-197)), and the Admin server with the Back-office.

**Internal calls** (the GraphQL and REST servers to the Git server) are plain HTTP with JSON on a port the chart never
exposes. **Changed** (owner, 2026-11-01): a call carries the viewer's own token, which the caller sends on and the Git
server checks itself, instead of a secret the chart generates ([code browsing](code-browsing.md)). Every workload is the
same Go binary, so both ends of a call share one Go type for its body.

## Deployment

The chart runs nine Deployments, one CronJob, a migration Job per upgrade and optionally PostgreSQL. The [Helm chart
design](https://claude.ai/artifact/LyMHXgSoNQNgHtHNfEjvA8#installs) details what its first slice installs.

**Four images, all at the chart’s app version, run every workload**

![Four images, all at the chart’s app version, run every workload](architecture/images.svg)

The `cobranch` image runs `cobranch <workload>`: `auth-server`, `graphql-server`, `rest-server`, `admin-server`,
`git-server`, `push-worker`, `controller`, `maintenance` or `migrate`.

| Workload | Kind | Replicas | Disk | Kubernetes access |
| --- | --- | --- | --- | --- |
| Console | Deployment | 2 | None | No token |
| Back-office | Deployment | 1 | None | No token |
| Auth server | Deployment | 2 | None | No token |
| GraphQL server | Deployment | 2 | None | Read `pods/log` in the job namespaces |
| REST server | Deployment | 2 | None | Read `pods/log` in the job namespaces |
| Admin server | Deployment | 1 | None | No token |
| Git server | Deployment | 2 | Ephemeral volume, the pack cache | No token |
| Push worker | Deployment | 2, or none while no push waits | None | No token |
| Controller | Deployment | 2 | None | Create job namespaces and their policies; create, watch and delete pods and Secrets in them |
| Maintenance | CronJob, hourly | 1 | Ephemeral volume, for repacking | No token |
| Migration | Job, per upgrade | 1 | None | No token |
| PostgreSQL | StatefulSet, optional | 1 | Persistent volume | No token |

No workload elects a leader. Replicas share work by claiming rows in PostgreSQL (`FOR UPDATE SKIP LOCKED`), so a dead
replica only delays its claimed rows until their lease expires. Every Deployment gets a disruption budget of one and
rolls out by surging; the chart sets no autoscaler.

### Routes

**Changed** (review, 2026-10-27): every surface is a workload with a Service of its own, and the operator routes hosts
and paths to them with the cluster's ingress or Gateway API. The chart takes each surface's public URL as a value, for
redirects, CORS and links, and can render the routes as `HTTPRoute`s, off by default.

**The operator routes hosts and paths to each workload’s Service**

![The operator routes hosts and paths to each workload’s Service](architecture/routes.svg)

Each workload serves as if it were alone; any hosts and prefixes work, as long as every host sits under one parent
domain, which the session cookie names.

- **One parent domain.** The Auth server sets the session cookie for the parent domain of every Cobranch host, so it
  reaches every API; the APIs answer CORS for the Console's and the Back-office's origins only.
- **Each workload serves as if it were alone.** **Changed** (cobranch#1 review, 2026-10-28): the Console and GraphQL
  answer at `/`, and the REST and Admin servers' paths start with their version, `/v1/`, never `/api/`. A host of its
  own or a path on a shared host is the operator's choice: a route that adds a prefix strips it (an `HTTPRoute`
  `URLRewrite`), and the pages use relative URLs, so they work under any prefix.
- **The Admin server and the Back-office can stay internal,** on a gateway only operators reach.

### Job isolation

**Agreed** (review, 2026-10-27): each organization's jobs, and for now its agents' pods, run in a namespace of their
own. Its NetworkPolicies admit no ingress at all, and allow egress to the internet and to cluster DNS only, never to the
cluster's pods or Services.

**A job or agent reaches the internet and nothing else in the cluster**

![A job or agent reaches the internet and nothing else in the cluster](architecture/isolation.svg)

The controller creates the namespace and its policies with the organization’s first job or agent task, and binds the
chart’s role that lets the REST and GraphQL servers read its pods’ logs.

### Dependencies

**The operator brings the stores, the sign-in and the routes in; the chart makes its own secrets**

![The operator brings the stores, the sign-in and the routes in; the chart makes its own secrets](architecture/dependencies.svg)

Without a bucket, the objects live in PostgreSQL ([Git object storage](git-object-storage.md)); a bucket’s credentials
come from the cloud’s workload identity or a Secret. The chart never rotates the secrets it generated.

### Upgrades

**Changed** (review, 2026-10-27): a `cobranch migrate` Job runs before the servers roll out, and pods never migrate. It
applies SQL and Go migrations by their numbers (goose runs both), and also any it lacks below the highest it has, since
parallel pull requests merge in any order; every release's migrations are compatible with the release before it: they
keep everything its pods read, so those pods keep working while the new ones roll out, and rolling back one release
needs no down migration. The [Helm chart](https://claude.ai/artifact/LyMHXgSoNQNgHtHNfEjvA8) decides how the Job runs
first, under Helm and Argo CD alike.

**A migration Job runs before the new pods; old pods keep working**

![A migration Job runs before the new pods; old pods keep working](architecture/upgrades.svg)

- **Pods wait for their schema.** A pod starts once the schema has every migration it has, and stays unready until then.
- **Removals wait a release.** A release adds columns and tables first, and stops reading old ones; a later release
  drops what neither it nor the release before it reads. So a rollback goes back one release at most.
- **Long backfills are tasks.** A data migration too long for the Job runs afterwards as a controller task, in batches
  that resume, and the code reads both shapes until it finishes.
- **One migrator at a time.** The Job takes an advisory lock, so two upgrades started together migrate in turn.
- **Seed data for previews.** A second set, `db/seeds/` (Go only, with its own version table), runs in the same Job
  after the schema's migrations, only when the chart turns seeding on, as preview installations do ([Helm
  chart](https://claude.ai/artifact/LyMHXgSoNQNgHtHNfEjvA8)).

## Data model

PostgreSQL holds every fact Cobranch shows, Git refs included; the object store holds only bytes. One database and one
schema serve every organization.

**Agreed:** primary keys are UUIDv7, which sort by creation time. The API shows them with a type prefix (`org_…`,
`repo_…`, `br_…`), which is how GraphQL's `node(id:)` knows the type. Organizations and repositories have slugs for
URLs, and a rename keeps the old slug as a redirect.

**Organizations own repositories; principals push and author their commits**

![Organizations own repositories; principals push and author their commits](architecture/er-git.svg)

Every row an organization owns also carries its `org_id`. The diagrams name the columns that matter to the design; the
migrations hold the rest.

**Branches, reviews and runs hang off a repository’s commits and branches**

![Branches, reviews and runs hang off a repository’s commits and branches](architecture/er-branches.svg)

Issues, agents and delivery add tables in their own designs. The `events` and `tasks` tables are under [Events and
background work](#events-and-background-work).

- **Commits belong to a repository.** The same SHA in two repositories is two rows; the foundations have no forks.
- **SHAs are `bytea`,** so SHA-1 and SHA-256 repositories both fit.
- **History is kept.** Deleting a branch deletes its ref but keeps its `branches` row, with its chat, reviews and
  status.
- **Settings live in Git.** Repository and branch settings, merge requirements included, come from the default branch's
  `/cobranch.yaml`; PostgreSQL keeps a parsed copy, replaced by the push that changes the file. Organization settings
  live in PostgreSQL only.
- **Secrets are rows,** encrypted, each with the repositories that may use it ([below](#settings-secrets-and-models)).
- **Unknown authors get principals.** A commit author or committer whose email nobody holds becomes a login-less user,
  which a user who later verifies that email [claims](principals-and-permissions.md#emails-and-login-less-users).
- **Scoping is the data layer's job.** **Agreed:** every query function takes its organization; row-level security is a
  later hardening.

## Git storage and pushes

**Agreed:** a repository is immutable packs in the object store plus its refs in PostgreSQL, and a Git server pod only
caches packs. **Changed** (2026-11-01): the object store is PostgreSQL's large objects by default, a bucket or a
directory if the operator names one ([Git object storage](git-object-storage.md)). A push is accepted by a pre-receive
hook that does not exit until the Git server has committed it. **Changed** (2026-11-01): the Git server commits the push
itself, and the push worker claims it afterwards ([Git hosting](git-hosting.md#a-push)).

### Storage

**PostgreSQL holds the refs and the object store the packs; a Git server pod only caches**

![PostgreSQL holds the refs and the object store the packs; a Git server pod only caches](architecture/storage.svg)

A pack and the refs that need it are recorded in one transaction, so a ref never points at an object no listed pack
holds. Every push adds a pack, so the maintenance CronJob repacks a repository whose pack count passes a threshold and
swaps the pack list in one transaction.

**A pack is written once and deleted a day after nobody lists it**

![A pack is written once and deleted a day after nobody lists it](architecture/pack-life.svg)

**Git itself speaks the protocol.** The `git` CLI runs `upload-pack`, `receive-pack`, `index-pack` and `repack`, so
every Git client works and pack negotiation is Git's own; Go drives it. A pure-Go implementation (go-git with its own
storage) was set aside for protocol coverage and speed on large repositories.

### A push

**A push returns only after PostgreSQL commits it**

![A push returns only after PostgreSQL commits it](architecture/push.svg)

While a large push is indexed, the hook writes progress to the client as `remote:` lines. Until the push worker has
processed a push, its commits and branches show without their principals and status; a push it fails waits for a retry.

Only what the screen must show is synchronous: the refs, commits and branches. The push worker adds principals and
status, and jobs, AI review, webhooks, delivery and notifications start from the tasks it queues, after the client has
returned. **Agreed:** every push is atomic, all refs or none, as `git push --atomic` is; a pre-receive hook decides for
the whole push anyway.

### Merges and settings take the same road

**Every ref, whoever moves it, goes through the Git server’s push transaction**

![Every ref, whoever moves it, goes through the Git server’s push transaction](architecture/ref-road.svg)

Repository and branch settings live in the default branch's `/cobranch.yaml`, so a change made in the Console takes
effect when `cobranch/settings` merges, through the same merge requirements as any other branch.

### Which pushes are accepted

**A push into a protected branch must merge Ready branches**

![A push into a protected branch must merge Ready branches](architecture/accept.svg)

**Agreed:** clients cannot push refs under `cobranch/`, and a push into a branch with merge requirements must merge
Ready branches unless the pusher holds [`repo.bypass`](principals-and-permissions.md#permissions). Otherwise a direct
push would skip every requirement.

**Agreed** (review, 2026-10-27): a [path rule](#settings-secrets-and-models) that restricts pushes refuses a push, into
any branch, that adds commits changing its paths, unless the rule names the pusher or one of their groups. The rule's
list decides alone, so `repo.bypass` does not lift it. Its approvals are a merge requirement like any other, checked by
the Ready branches the push must merge.

### Failure modes

| Failure | The client sees | Afterwards | Recovery |
| --- | --- | --- | --- |
| Recording the push fails | Push rejected: Cobranch could not record it | Nothing changed; an unlisted pack in the object store | Retry; the pack is deleted after a day |
| Another push moved a ref first | Push rejected: fetch first | Nothing changed | Fetch, merge or rebase, push again |
| The push committed, its answer lost | Nothing yet: the Git server asks again with the push ID | Committed | A known push ID is accepted as it was |
| Push worker down or failing | Push accepted | Commits and branches recorded; principals, status and jobs wait | A push worker that starts claims every waiting push; a failed one is retried, at most an hour apart |
| Git server pod dies mid-push | Connection dropped | Nothing changed, or committed | The next fetch shows which; a retry is safe |
| A bucket unavailable | Push rejected; fetches only from cached packs | Nothing changed | Retry once the bucket is back |
| PostgreSQL unavailable | Push and fetch rejected | Nothing changed | Everything waits for PostgreSQL |
| Push into a protected branch merges no Ready branch | Push rejected, naming the requirement | Nothing changed | Merge a Ready branch, or use the bypass |

### A fetch

**A fetch reads one snapshot of the refs and the packs**

![A fetch reads one snapshot of the refs and the packs](architecture/fetch.svg)

A compaction that commits during a fetch does not disturb it: its new pack holds every object of the packs it replaced,
and those stay in the object store for a day.

## Events and background work

Every change appends an event in the transaction that makes it, and every reaction is a task row the controller claims.
Nothing is lost between a change and its reaction, and no broker is needed.

**Every change queues its reactions in the same transaction**

![Every change queues its reactions in the same transaction](architecture/events.svg)

One task per subscriber, so a slow webhook never holds up a job. The `events` table is what jobs, webhooks, the activity
view, audit and live updates all read.

**A task is claimed under a lease, retried, and parked when its attempts run out**

![A task is claimed under a lease, retried, and parked when its attempts run out](architecture/task-life.svg)

A task’s `kind` and `key` are unique while it is open, so queueing one twice is harmless. A cron task’s key names the
job and the minute, so a schedule fires once however many replicas run.

**The controller runs a job’s pod, records the result and keeps its logs**

![The controller runs a job’s pod, records the result and keeps its logs](architecture/job-run.svg)

**An agent pod that dies resumes from its saved transcript**

![An agent pod that dies resumes from its saved transcript](architecture/agent-task.svg)

The AI reviewer is a built-in agent of every organization, configured by each repository’s `/cobranch-review.yaml`; the
controller gives it a review task on every push.

- **Live updates.** A client that reconnects passes the last event ID it saw, and the GraphQL server replays from
  `events`.
- **Maintenance.** The hourly CronJob compacts packs, deletes unreferenced objects, applies log retention, and drops
  expired sessions and tokens.
- **Day-one events** (2026-10-27): `branch.created`, `branch.pushed`, `branch.merged`, `branch.deleted`, `tag.pushed`,
  `review.submitted` and `annotation.changed`. Jobs also take cron and manual triggers.

### Why no broker

**Agreed, and kept after review** (2026-10-27): a broker such as NATS would not make events safer, and the controller
can still scale to zero.

**Nothing is lost while the controller is down: its tasks wait as rows**

![Nothing is lost while the controller is down: its tasks wait as rows](architecture/events-down.svg)

A broker would not make this safer: Cobranch would commit to PostgreSQL and then publish, and would need the same outbox
to survive a crash between the two.

- **Nothing is lost.** A change, its event and its tasks commit together, so no reaction is lost and none fires for a
  change that rolled back. `NOTIFY` only wakes a listener sooner; a workload that was down claims the waiting rows when
  it starts, and a task whose claimant died is claimed again when its lease expires.
- **Scale to zero.** The controller can scale to zero on the operator's autoscaler: KEDA's PostgreSQL trigger counts
  open tasks, as its NATS trigger would count pending messages, and the push worker on the same trigger over waiting
  pushes, since no push waits for it. Workloads that serve requests need an HTTP scaler either way.
- **What it costs.** `NOTIFY` can serialize commits on one lock, so at thousands of writes a second Cobranch would batch
  its notifications. One PostgreSQL carries this far past a first installation's load, and the event and task stores are
  interfaces, so NATS JetStream can replace them without the services changing.

## Settings, secrets and models

**Agreed** (review, 2026-10-27): repository settings stay code and organization settings stay data, and both hold what
jobs and agents need from outside, secrets and models.

- **In effect, and pending.** The Console shows the settings in effect, from the default branch's `/cobranch.yaml`, and
  the pending changes: every other branch whose `/cobranch.yaml` differs, each with its diff, however many there are.
  Each push records each tip's file, so listing them is one query.
- **A settings change merges like any branch.** **Agreed** (review, 2026-10-27): merge requirements gain path rules,
  which apply only when a branch changes matching paths, such as `/cobranch.yaml`. A path rule can require approval from
  named principals or groups, and refuse pushes that touch its paths from anyone else.

**A secret is written once, kept encrypted, and reaches only the runs allowed to use it**

![A secret is written once, kept encrypted, and reaches only the runs allowed to use it](architecture/secrets.svg)

A grant can name one secret and reach only it, as a cloud’s IAM policy set on one resource does.

- **Secrets** live in an organization's settings or a repository's: an ID, a name and a value encrypted with the chart's
  secrets key, which nobody reads back once written. `secrets.manage` adds, replaces and deletes them there.
- **`secrets.read` decides who receives one,** granted on the organization, a repository or one secret
  ([secrets](principals-and-permissions.md#secrets)). An agent task receives the secrets its agent may read, whoever
  assigned it; a job run, the secrets its `/cobranch.yaml` names and its job may read.
- **Until phase 2 pins job security,** a branch's job of the same name receives the same secrets, so a secret is only as
  safe as the people who may push to its repository. Jobs for tips pushed from outside the organization stay off unless
  the repository allows them ([visibility](principals-and-permissions.md#visibility)).
- **Rotating the secrets key** is a maintenance task that re-encrypts every value.

**Each agent runs on the model its admin picks; the organization holds the providers’ credentials**

![Each agent runs on the model its admin picks; the organization holds the providers’ credentials](architecture/models.svg)

An example. Any provider the organization has credentials for can serve any of its agents, and an admin can switch an
agent’s model at any time.

- **Model providers:** Anthropic (Claude), Google (Gemini), xAI (Grok), OpenAI (GPT) and DeepSeek. An organization adds
  a provider by its credentials, kept as an organization secret that its agents may read.
- **An admin picks each agent's provider and model** (`org.principals.manage`), so one organization runs several agents
  on different models, the AI reviewer included. The agent harness reaches every provider through one interface, with an
  adapter each.

## API conventions

Three APIs serve authenticated principals, and callers without a credential only to read public repositories; the Auth
server signs them in. **Changed** (review, 2026-10-27): each is a workload of its own ([Components](#components)).

**Three APIs are thin transports over one service layer**

![Three APIs are thin transports over one service layer](architecture/api.svg)

The Auth server stands beside them: it signs people in and issues tokens, and serves no data.

- **GraphQL** is the Console's surface, at `/`: `node(id:)`, connections, and live updates as subscriptions.
- **REST** lives under `/v1/`, with paths that follow ownership (`/v1/orgs/{org}/repos/{repo}/branches/{branch}`).
  **Agreed:** REST is the SDKs' surface, generated from the checked-in document with the Go server stubs.
- **Admin** is the Back-office's surface: a REST API under `/v1/` from a document of its own. **Agreed** (review): each
  operation checks an ordinary permission, granted at the installation, an organization or a repository, so a principal
  can administer one organization without the installation ([scopes](principals-and-permissions.md#scopes-and-grants)).
- **One shape for an object.** camelCase JSON fields, RFC 3339 timestamps in UTC, and the data model's typed IDs, so an
  object reads the same in either.

### Authentication

[Principals and permissions](principals-and-permissions.md#principal-kinds) designs the principals and grants; these are
the credentials each caller presents.

**Every credential resolves to one principal, whose grants decide**

![Every credential resolves to one principal, whose grants decide](architecture/credentials.svg)

The session cookie is `HttpOnly`, `Secure` and `SameSite=Lax`, set by the Auth server after sign-in at an identity
provider, for the parent domain of Cobranch’s hosts. The APIs answer CORS for the Console’s and the Back-office’s
origins only, and cookie sessions can only send mutations as JSON bodies, which a cross-site form cannot produce.

### Errors

**A caller never learns that an object it cannot see exists**

![A caller never learns that an object it cannot see exists](architecture/errors.svg)

- **REST** answers with problem details (RFC 9457, `application/problem+json`): `type`, `title`, `status`, `detail`, a
  stable `code` such as `branch.not_found`, and the `requestId`. A validation error lists each field with its own code.
  A rate limit is `429` with `Retry-After`.
- **GraphQL** puts the same `code` in each error's `extensions`. The codes are defined once in the service layer, and
  the SDKs expose them as constants.

### Pagination, versions and writes

| Concern | Convention |
| --- | --- |
| Pagination | By cursor only: REST takes `first` and `after` and answers `{"items": […], "nextCursor": "…"}`; GraphQL returns Relay connections. **Agreed:** 50 items by default, 100 at most. A cursor encodes the sort key and the ID and survives inserts; no offsets, and a total count only where cheap |
| REST versions | The major version is in the path; `v1` changes are additive; a breaking change ships as `v2` beside it, and the old one announces its end with `Deprecation` and `Sunset` |
| GraphQL versions | Never versioned: a field is marked `@deprecated` and removed no sooner than two minor releases later |
| SDK versions | The product's version |
| Idempotency | A REST `POST` accepts an `Idempotency-Key`; a repeat within a day returns the first answer |
| Concurrency | Mutable objects carry a version: `If-Match` in REST, `expectedVersion` in GraphQL; a mismatch is `409` |
| Tracing | Requests may carry a W3C `traceparent`, which every hop propagates; every response names its request ID |

## Blob storage

One object store, reached only through Go Cloud's `blob` package, holds every large or binary object; PostgreSQL holds
the keys and decides who may read them. **Changed** (2026-11-01): the store is PostgreSQL's large objects by default, so
a backup of the database holds everything, or a cloud's bucket or a directory if the operator names one ([Git object
storage](git-object-storage.md)); only the workloads that read or write objects take its URL. Keys are built from IDs,
never names, so a rename moves nothing.

| Prefix | Holds | Written by | Kept |
| --- | --- | --- | --- |
| `git/<repo-id>/` | Packs and their indexes | Git server; maintenance | Until superseded or never listed, then a day |
| `logs/<repo-id>/<run-id>/` | A finished run's logs, one object per step | Controller | The operator's retention, 90 days by default |
| `uploads/<org-id>/<upload-id>` | Attachments to messages, reviews and issues; avatars | REST and GraphQL servers | While something refers to them |
| `artifacts/<repo-id>/<run-id>/` | Files a job run publishes | Job pods, through the REST server | Set by the CI design |
| `agents/<org-id>/<task-id>/` | Large tool outputs and files of an agent task | Agent pods, through the REST server | As long as the task |

**Every read of a stored file passes a permission check first**

![Every read of a stored file passes a permission check first](architecture/blob-read.svg)

- **Objects are written whole.** A running job's logs come from the Kubernetes API while its pod lives, and from the
  object store once the controller has archived them.
- **Deletion follows ownership.** Deleting a repository or an organization queues a task that deletes its prefixes.
- **Encryption at rest is the store's,** the database's or the bucket's, configured by the operator.

## Observability

**Proposed** (cobranch#1 review, 2026-10-28): every workload logs to stdout and traces each request with OpenTelemetry,
and the operator decides where both go. Metrics, added with [ENG-235](https://linear.app/arikkfir/issue/ENG-235)
([monitoring](monitoring.md)), go the same way. Cobranch calls no monitoring service of its own.

**One trace follows a change from the click to the job it starts**

![One trace follows a change from the click to the job it starts](architecture/traces.svg)

The probes are not traced. Webhooks carry `traceparent` too, so a receiver that speaks Trace Context joins the trace.

- **Logs.** `log/slog`: JSON, one object per line, inside a Kubernetes pod, for the cluster's log collector; text
  elsewhere, for a person; `COBRANCH_LOG_FORMAT` overrides either. Every record names its workload and version and,
  inside a request, its trace and span (`traceId`, `spanId`).
- **Traces.** W3C Trace Context carries one trace across every hop: the Console's calls, the internal calls between
  workloads, and the tasks and pushes in PostgreSQL, whose rows keep the `traceparent` of the transaction that queued
  them. Each workload is a service named after itself, in namespace `cobranch`.
- **Request IDs.** Every response names its request's ID (`X-Request-Id`): the caller's, when it sends one, and
  otherwise the trace ID, so the ID in an error report finds both the logs and the trace. Each span records it as
  `http.request.id`.

**The operator decides where logs, traces and metrics go**

![The operator decides where logs, traces and metrics go](architecture/telemetry.svg)

With no OTLP endpoint set, neither in `OTEL_EXPORTER_OTLP_ENDPOINT` nor in the traces’ or metrics’ own, nothing leaves
the pod over OTLP, and metrics leave it only when `OTEL_METRICS_EXPORTER=prometheus` serves them at `/metrics`; trace
IDs still name every request in the logs.

OpenTelemetry's own variables, unchanged, say where traces go, and the chart sets them from its values:
`OTEL_EXPORTER_OTLP_ENDPOINT`, `OTEL_EXPORTER_OTLP_HEADERS` for a vendor's key, `OTEL_TRACES_SAMPLER` and its argument,
and `OTEL_RESOURCE_ATTRIBUTES`. Cobranch exports over OTLP with HTTP and protobuf, which every backend below takes.

| To see traces in | Point the endpoint at |
| --- | --- |
| Any backend, with sampling and fan-out | An OpenTelemetry Collector in the cluster: the setup the chart documents |
| Google Cloud Trace | A Collector that signs its calls with the cluster's Google credentials: Google's exporter, or OTLP to the Telemetry API with the `googleclientauth` extension. Headers alone can't carry Google's short-lived tokens |
| Sentry | The project's OTLP endpoint, its key in `OTEL_EXPORTER_OTLP_HEADERS` from a Secret |
| Jaeger, Grafana Tempo, Honeycomb, Datadog | Their OTLP endpoint, directly or through a Collector |

Metrics are OpenTelemetry's too ([monitoring](monitoring.md)): how long each Console page takes to show, how long each
route and GraphQL field takes to answer, what each throwaway repository costs and how often the pack cache hits.
`OTEL_METRICS_EXPORTER` picks how they leave: over OTLP, to `OTEL_EXPORTER_OTLP_METRICS_ENDPOINT` or the traces'
endpoint, the default once either is set; or at `/metrics` for the operator's Prometheus scraper, on a port the chart
names `metrics` on every server and the push worker. With no exporter and no endpoint, they stay in the pods.

## Stack and layout

**Agreed:** Go for every service and TypeScript with React for both web apps, in one monorepo
([repository](repository.md)) that builds one server image and two web images. The contracts are written first and the
code is generated from them.

**Dependencies point inward: the services import no adapter**

![Dependencies point inward: the services import no adapter](architecture/layers.svg)

| Concern | Choice | Why |
| --- | --- | --- |
| Services | Go 1.27; one binary, `cobranch <workload>` | Octomaton's language and layering; Go Cloud and `client-go` are Go |
| HTTP | The standard library's `net/http` routing | No framework to outlive |
| REST | OpenAPI 3.1 written first; `oapi-codegen` | The document is the contract and the SDKs' source |
| GraphQL | Schema written first; `gqlgen` | Typed resolvers from the schema |
| PostgreSQL | `pgx`; `sqlc` from SQL queries; `goose` SQL and Go migrations, run by `cobranch migrate` | SQL stays SQL, checked at build time |
| Git | The `git` CLI in the image; `golang.org/x/crypto/ssh` | Wire compatibility and Git's own speed |
| Blob storage | `gocloud.dev/blob`, with Cobranch's own driver over PostgreSQL's large objects | One API over the database, every cloud's buckets and a directory |
| Kubernetes | `client-go`, in the controller and for log reads | The only two places that touch the cluster |
| Models | One interface in the agent harness, an adapter per provider | Each agent runs on its own provider and model |
| Sign-in | `go-oidc` with `golang.org/x/oauth2` | Standard OpenID Connect |
| Configuration | Environment variables, read with `envconfig` | As Octomaton does |
| Telemetry | `log/slog` on stdout, JSON in a cluster; OpenTelemetry traces over OTLP, W3C Trace Context; OpenTelemetry metrics over OTLP or at `/metrics` ([observability](#observability), [monitoring](monitoring.md)) | No cloud's monitoring built in |
| Web apps | TypeScript, React and Vite, static files that Caddy serves | No Node.js at run time |
| Web data | GraphQL Code Generator types, through urql | Typed queries; subscriptions for live updates |
| Web components | Headless primitives styled with the [design system's](design-system.md) CSS variables | Settled with [ENG-194](https://linear.app/arikkfir/issue/ENG-194) |
| SDKs | Generated from the OpenAPI document: Go, Python, TypeScript | One contract, three languages |
| Tests | Table-driven Go tests; integration on a real PostgreSQL, its large objects and a `file://` bucket; end to end on kind with real `git` | The push path is only proven end to end |

```
go.mod                        one module: cobranch.dev
cmd/cobranch/                 the binary: auth-server, graphql-server, rest-server, admin-server, git-server,
                              push-worker, controller, maintenance, migrate, agent
internal/services/            domain logic: organizations, repositories, branches, annotations, events
internal/adapters/            postgres, blob, git, kube, rest, graphql, oidc
internal/system/              configuration, telemetry, build information
api/openapi.yaml              the REST contract
api/admin.yaml                the Admin API's contract
api/graphql/                  the GraphQL schema
db/migrations/                numbered SQL and Go migrations
db/seeds/                     seed data for previews, Go, run only when the chart turns seeding on
web/console/  web/back-office/  web/ui/
sdk/go/  sdk/python/  sdk/typescript/
chart/cobranch/               the Helm chart
docs/cobranch/                the designs, once synced from these pages
.octomaton.yaml  .tekton/     Cobranch's own CI, on Octomaton until Cobranch can build itself
```

## Decisions

Choices beyond the product design, as of the owner's review on 2026-10-27: it changed three, kept the broker-less
design, and settled the open questions.

| Choice | Why | Decision |
| --- | --- | --- |
| PostgreSQL is the only stateful dependency, objects included unless the operator names a bucket; `NOTIFY` and a task table stand in for a broker | One less system for every operator; a change and its reactions commit together; one backup holds everything (2026-11-01, [Git object storage](git-object-storage.md#decisions)) | Changed |
| A repository is immutable packs in the object store plus refs in PostgreSQL; Git server pods only cache | Keeps the Git server stateless | Agreed |
| The `git` CLI speaks the protocol, driven by Go | Every client works, at Git's own speed | Agreed |
| A controller workload takes all asynchronous work | Keeps the push worker fast | Agreed |
| The Git server commits each push; the push worker claims it from PostgreSQL afterwards, woken by `NOTIFY` | Only the Git server is on a push's path, and the push worker can scale to zero (2026-11-01, [Git hosting](git-hosting.md#decisions)) | Changed |
| Every push is atomic: all refs or none | A pre-receive hook decides for the whole push anyway | Agreed |
| A push into a protected branch must merge Ready branches, unless the pusher may bypass | Otherwise a direct push skips every requirement | Agreed |
| Merge requirements are branch settings in the default branch's `/cobranch.yaml` | Repository and branch settings live in `/cobranch.yaml` | Agreed |
| Day-one events: the four, plus `tag.pushed`, `review.submitted` and `annotation.changed` | Decided on 2026-10-27 | Agreed |
| One Go binary for every workload; internal calls are HTTP with JSON | One version everywhere, one type per call | Agreed |
| The Auth, GraphQL, REST and Admin servers are workloads of their own, routed by the operator under one parent domain | The operator routes, scales and exposes each surface on its own | Changed |
| A `cobranch migrate` Job runs SQL and Go migrations compatible with the previous release, before the servers roll out; pods never migrate | No race to migrate at startup, and the old release keeps working | Changed |
| REST is the SDKs' surface, GraphQL the Console's, a separate Admin API the Back-office's | Each where its tooling is strongest; admin permissions at any scope | Changed |
| Each organization's jobs and, for now, agent pods run in a namespace of their own: no ingress, egress to the internet and DNS only | A job or agent reaches nothing in the cluster that another organization uses | Agreed |
| Cron triggers come from the default branch's `/cobranch.yaml` only | A branch cannot schedule work | Agreed |
| Secrets are encrypted rows in organization or repository settings; `secrets.read` grants decide who receives them; `/cobranch.yaml` names them by ID | The chart already holds keys; no secret store to add; agents hold outside credentials the same way | Agreed |
| An admin picks each agent's provider and model: Anthropic, Google, xAI, OpenAI or DeepSeek | Agents of one organization can each run on the model that suits them | Agreed |
| Logs on stdout, JSON in a cluster; one OpenTelemetry trace across every workload, exported over OTLP wherever the operator says; the request ID is the trace ID unless the caller sent one; metrics over OTLP or scraped at `/metrics` | Any log stack, trace backend and metrics backend, with no vendor's code in Cobranch | Proposed |
| The Console shows the settings in effect and every pending change | A settings change is a branch like any other | Agreed |
| Path rules: merge requirements for changes to paths such as `/cobranch.yaml`, which can also refuse pushes there | Settings changes need stricter approval | Agreed |
| UUIDv7 keys, shown with a type prefix | Sortable keys; `node(id:)` knows the type | Agreed |
| The stack above | Octomaton's proven pieces, contracts first | Agreed |
| Settings take effect from the default branch's `/cobranch.yaml`; clients cannot push under `cobranch/` | A branch's own settings count only once merged | Agreed |
| Each agent task runs in its own pod, its transcript appended to PostgreSQL as it goes | A restarted pod resumes where it stopped | Agreed |
| The AI reviewer reads the default branch's `/cobranch-review.yaml` | A branch cannot rewrite its own reviewer's instructions | Agreed |
| Later: push limits and the Git server's cache placement. Phase 2: Git LFS and all search, semantic search on pgvector embeddings included. The operator's: backup and restore | Not needed for the foundations | Deferred |

## Open questions

None: the review on 2026-10-27 settled every question and proposal on this page, under [Decisions](#decisions).

Cobranch is a Kfirs project, created by Arik Kfir.
