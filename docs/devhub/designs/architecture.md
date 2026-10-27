# DevHub technical architecture

> **Snapshot.** This design lives in its [architecture page](https://claude.ai/artifact/L6NYy7k4ep31FEPAxQiPjv), linked
> with DevHub's other designs; this file is generated from it, with its diagrams, as of 2026-10-27, and synced again
> once DevHub's shape settles.

**Status**: Agreed 2026-10-27 · [ENG-193](https://linear.app/arikkfir/issue/ENG-193) · Builds on the [product
design](product.md#architecture) · Detailed by the [Helm chart](https://claude.ai/artifact/LyMHXgSoNQNgHtHNfEjvA8)

DevHub runs as a handful of Go workloads over two stores, PostgreSQL and one bucket, and a Git push returns only after
PostgreSQL has committed it. Everything else here follows from those two choices.

## Scope

The technical design of DevHub's foundations ([ENG-193](https://linear.app/arikkfir/issue/ENG-193)): the workloads and
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
- **Stateless workloads.** Any pod can be killed at any time. State lives only in PostgreSQL and blob storage; a pod's
  disk is a cache.
- **Few moving parts.** An operator runs PostgreSQL and a bucket and nothing else. **Agreed:** no message broker, no
  Redis, no search cluster in the foundations.
- **Synchronous pushes.** When `git push` returns, the commits, branches and their status are in PostgreSQL and on
  screen.
- **Many organizations per installation.** Every row an organization owns carries its ID, and every query is scoped by
  it.
- **One permission model.** REST, GraphQL and Git check the same grants through the same code.
- **Agents use the front door.** Agents, jobs and AI review call the same APIs as people, as principals of their own.

## Components

Seven workloads, plus the job and agent pods the controller creates. Only the Git server reads Git objects, only the
push worker moves refs, and only the controller talks to the Kubernetes API.

**All state lives in PostgreSQL and one bucket**

![All state lives in PostgreSQL and one bucket](architecture/components.svg)

The **controller** is a workload the product design did not list: the push worker must stay fast, so the slow, retried
and outward-facing work (pods, webhooks, model calls) gets a workload of its own.

**Internal calls** (API server to Git server, Git server to push worker) are plain HTTP with JSON on a port the chart
never exposes, authenticated with a secret the chart generates. Every workload is the same Go binary, so both ends of a
call share one Go type for its body.

## Deployment

The chart runs six Deployments, one CronJob and optionally PostgreSQL. The [Helm chart
design](https://claude.ai/artifact/LyMHXgSoNQNgHtHNfEjvA8#installs) details what its first slice installs.

**Four images, all at the chart’s app version, run every workload**

![Four images, all at the chart’s app version, run every workload](architecture/images.svg)

The `devhub` image runs `devhub <workload>`: `api-server`, `git-server`, `push-worker`, `controller` or `maintenance`.

| Workload | Kind | Replicas | Disk | Kubernetes access |
| --- | --- | --- | --- | --- |
| Console | Deployment | 2 | None | No token |
| Back-office | Deployment | 1 | None | No token |
| API server | Deployment | 2 | None | Read `pods/log` in the jobs namespace |
| Git server | Deployment | 2 | Ephemeral volume, the pack cache | No token |
| Push worker | Deployment | 2 | None | No token |
| Controller | Deployment | 2 | None | Create, watch and delete pods and Secrets in the jobs and agents namespaces |
| Maintenance | CronJob, hourly | 1 | Ephemeral volume, for repacking | No token |
| PostgreSQL | StatefulSet, optional | 1 | Persistent volume | No token |

No workload elects a leader. Replicas share work by claiming rows in PostgreSQL (`FOR UPDATE SKIP LOCKED`), so a dead
replica only delays its claimed rows until their lease expires. Every Deployment gets a disruption budget of one and
rolls out by surging; the chart sets no autoscaler.

### Routes

One host serves the Console, the API and Git over HTTPS, so the session cookie is first-party and the API needs no CORS.
The chart can render the routes as an Ingress or a Gateway API `HTTPRoute`, off by default.

**One host splits by path; the Back-office gets a host of its own**

![One host splits by path; the Back-office gets a host of its own](architecture/routes.svg)

### Dependencies

**The operator brings the stores, the sign-in and the routes in; the chart makes its own secrets**

![The operator brings the stores, the sign-in and the routes in; the chart makes its own secrets](architecture/dependencies.svg)

The bucket’s credentials come from the cloud’s workload identity or a Secret. The chart never rotates the secrets it
generated.

### Upgrades

Migrations expand before they contract, so pods of the old and the new version run side by side through a rolling
upgrade. The [Helm chart's startup](https://claude.ai/artifact/LyMHXgSoNQNgHtHNfEjvA8#startup) shows the same lock from
the chart's side.

**One new API server migrates; every other pod works on the expanded schema**

![One new API server migrates; every other pod works on the expanded schema](architecture/upgrades.svg)

## Data model

PostgreSQL holds every fact DevHub shows, Git refs included; blob storage holds only bytes. One database and one schema
serve every organization.

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
  `/devhub.yaml`; PostgreSQL keeps a parsed copy, replaced by the push that changes the file. Organization settings live
  in PostgreSQL only.
- **Unknown authors get principals.** A commit author or committer whose email nobody holds becomes a login-less user,
  which a user who later verifies that email [claims](principals-and-permissions.md#emails-and-login-less-users).
- **Scoping is the data layer's job.** **Agreed:** every query function takes its organization; row-level security is a
  later hardening.

## Git storage and pushes

**Agreed:** a repository is immutable packs in blob storage plus its refs in PostgreSQL, and a Git server pod only
caches packs. A push is accepted by a pre-receive hook that does not exit until the push worker has committed it.

### Storage

**PostgreSQL holds the refs and the bucket the packs; a Git server pod only caches**

![PostgreSQL holds the refs and the bucket the packs; a Git server pod only caches](architecture/storage.svg)

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

While a large push is indexed, the hook writes progress to the client as `remote:` lines.

Only what the screen must show is synchronous: jobs, AI review, webhooks, delivery and notifications start from the
tasks the transaction queued, after the client has returned. **Agreed:** every push is atomic, all refs or none, as
`git push --atomic` is; a pre-receive hook decides for the whole push anyway.

### Merges and settings take the same road

**Every ref, whoever moves it, goes through the push worker**

![Every ref, whoever moves it, goes through the push worker](architecture/ref-road.svg)

Repository and branch settings live in the default branch's `/devhub.yaml`, so a change made in the Console takes effect
when `devhub/settings` merges, through the same merge requirements as any other branch.

### Which pushes are accepted

**A push into a protected branch must merge Ready branches**

![A push into a protected branch must merge Ready branches](architecture/accept.svg)

**Agreed:** clients cannot push refs under `devhub/`, and a push into a branch with merge requirements must merge Ready
branches unless the pusher holds [`repo.bypass`](principals-and-permissions.md#permissions). Otherwise a direct push
would skip every requirement.

### Failure modes

| Failure | The client sees | Afterwards | Recovery |
| --- | --- | --- | --- |
| Push worker unreachable or failing | Push rejected: DevHub could not record it | Nothing changed; an unlisted pack in the bucket | Retry; the pack is deleted after a day |
| Another push moved a ref first | Push rejected: fetch first | Nothing changed | Fetch, merge or rebase, push again |
| Push worker committed, its answer lost | Nothing yet: the hook asks again with the push ID | Committed | The push worker answers a known push ID with its result |
| Git server pod dies mid-push | Connection dropped | Nothing changed, or committed | The next fetch shows which; a retry is safe |
| Blob storage unavailable | Push rejected; fetches only from cached packs | Nothing changed | Retry once the bucket is back |
| PostgreSQL unavailable | Push and fetch rejected | Nothing changed | Everything waits for PostgreSQL |
| Push into a protected branch merges no Ready branch | Push rejected, naming the requirement | Nothing changed | Merge a Ready branch, or use the bypass |

### A fetch

**A fetch reads one snapshot of the refs and the packs**

![A fetch reads one snapshot of the refs and the packs](architecture/fetch.svg)

A compaction that commits during a fetch does not disturb it: its new pack holds every object of the packs it replaced,
and those stay in the bucket for a day.

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

The AI reviewer is a built-in agent of every organization, configured by each repository’s `/devbot.yaml`; the
controller gives it a review task on every push.

- **Live updates.** A client that reconnects passes the last event ID it saw, and the API server replays from `events`.
- **Maintenance.** The hourly CronJob compacts packs, deletes unreferenced blobs, applies log retention, and drops
  expired sessions and tokens.
- **Day-one events** (2026-10-27): `branch.created`, `branch.pushed`, `branch.merged`, `branch.deleted`, `tag.pushed`,
  `review.submitted` and `annotation.changed`. Jobs also take cron and manual triggers.

One PostgreSQL carries this far past a first installation's load. The event and task stores are interfaces in the code,
so a broker can replace them later without the services changing.

## API conventions

Both transports serve authenticated principals, and callers without a credential only to read public repositories.

**REST and GraphQL are two thin transports over one service layer**

![REST and GraphQL are two thin transports over one service layer](architecture/api.svg)

- **REST** lives under `/api/v1/`, with paths that follow ownership
  (`/api/v1/orgs/{org}/repos/{repo}/branches/{branch}`). **Agreed:** REST is the SDKs' surface, generated from the
  checked-in document with the Go server stubs.
- **GraphQL** is the Console's surface: `node(id:)`, connections, and live updates as subscriptions.
- **One shape for an object.** camelCase JSON fields, RFC 3339 timestamps in UTC, and the data model's typed IDs, so an
  object reads the same in either.

### Authentication

[Principals and permissions](principals-and-permissions.md#principal-kinds) designs the principals and grants; these are
the credentials each caller presents.

**Every credential resolves to one principal, whose grants decide**

![Every credential resolves to one principal, whose grants decide](architecture/credentials.svg)

The session cookie is `HttpOnly`, `Secure` and `SameSite=Lax`, set by `/auth/` after sign-in at an identity provider.
Cookie sessions can only send mutations as JSON bodies, which a cross-site form cannot produce.

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

One bucket, reached only through Go Cloud's `blob` package, holds every large or binary object; PostgreSQL holds the
keys and decides who may read them. Keys are built from IDs, never names, so a rename moves nothing.

| Prefix | Holds | Written by | Kept |
| --- | --- | --- | --- |
| `git/<repo-id>/` | Packs and their indexes | Git server; maintenance | Until superseded or never listed, then a day |
| `logs/<repo-id>/<run-id>/` | A finished run's logs, one object per step | Controller | The operator's retention, 90 days by default |
| `uploads/<org-id>/<upload-id>` | Attachments to messages, reviews and issues; avatars | API server | While something refers to them |
| `artifacts/<repo-id>/<run-id>/` | Files a job run publishes | Job pods, through the API server | Set by the CI design |
| `agents/<org-id>/<task-id>/` | Large tool outputs and files of an agent task | Agent pods, through the API server | As long as the task |

**Every read of a stored file passes a permission check first**

![Every read of a stored file passes a permission check first](architecture/blob-read.svg)

- **Objects are written whole.** A running job's logs come from the Kubernetes API while its pod lives, and from the
  bucket once the controller has archived them.
- **Deletion follows ownership.** Deleting a repository or an organization queues a task that deletes its prefixes.
- **Encryption at rest is the bucket's,** configured by the operator.

## Stack and layout

**Agreed:** Go for every service and TypeScript with React for both web apps, in one monorepo
([repository](repository.md)) that builds one server image and two web images. The contracts are written first and the
code is generated from them.

**Dependencies point inward: the services import no adapter**

![Dependencies point inward: the services import no adapter](architecture/layers.svg)

| Concern | Choice | Why |
| --- | --- | --- |
| Services | Go 1.27; one binary, `devhub <workload>` | Octomaton's language and layering; Go Cloud and `client-go` are Go |
| HTTP | The standard library's `net/http` routing | No framework to outlive |
| REST | OpenAPI 3.1 written first; `oapi-codegen` | The document is the contract and the SDKs' source |
| GraphQL | Schema written first; `gqlgen` | Typed resolvers from the schema |
| PostgreSQL | `pgx`; `sqlc` from SQL queries; `goose` migrations in the binary | SQL stays SQL, checked at build time |
| Git | The `git` CLI in the image; `golang.org/x/crypto/ssh` | Wire compatibility and Git's own speed |
| Blob storage | `gocloud.dev/blob` | One API over every cloud's buckets and a directory |
| Kubernetes | `client-go`, in the controller and for log reads | The only two places that touch the cluster |
| Sign-in | `go-oidc` with `golang.org/x/oauth2` | Standard OpenID Connect |
| Configuration | Environment variables, read with `envconfig` | As Octomaton does |
| Telemetry | `log/slog` JSON on stdout; OpenTelemetry over OTLP | No cloud's monitoring built in |
| Web apps | TypeScript, React and Vite, static files that Caddy serves | No Node.js at run time |
| Web data | GraphQL Code Generator types, through urql | Typed queries; subscriptions for live updates |
| Web components | Headless primitives styled with the [design system's](design-system.md) CSS variables | Settled with [ENG-194](https://linear.app/arikkfir/issue/ENG-194) |
| SDKs | Generated from the OpenAPI document: Go, Python, TypeScript | One contract, three languages |
| Tests | Table-driven Go tests; integration on a real PostgreSQL and a `file://` bucket; end to end on kind with real `git` | The push path is only proven end to end |

```
go.mod                        one module: github.com/arikkfir-org/devhub
cmd/devhub/                   the binary: api-server, git-server, push-worker, controller, maintenance, agent
internal/services/            domain logic: organizations, repositories, branches, annotations, events
internal/adapters/            postgres, blob, git, kube, rest, graphql, oidc
internal/system/              configuration, telemetry, build information
api/openapi.yaml              the REST contract
api/graphql/                  the GraphQL schema
db/migrations/                numbered SQL migrations
web/console/  web/back-office/  web/ui/
sdk/go/  sdk/python/  sdk/typescript/
chart/devhub/                 the Helm chart
docs/devhub/                  the designs, once synced from these pages
.octomaton.yaml  .tekton/     DevHub's own CI, on Octomaton until DevHub can build itself
```

## Decisions

Sixteen choices went beyond the product design. The decisions of 2026-10-27 changed two and added three that follow from
them; the rest were agreed the same day.

| Choice | Why | Decision |
| --- | --- | --- |
| PostgreSQL and one bucket are the only stateful dependencies; `NOTIFY` and a task table stand in for a broker | One less system for every operator | Agreed |
| A repository is immutable packs in the bucket plus refs in PostgreSQL; Git server pods only cache | Keeps the Git server stateless | Agreed |
| The `git` CLI speaks the protocol, driven by Go | Every client works, at Git's own speed | Agreed |
| A controller workload takes all asynchronous work | Keeps the push worker fast | Agreed |
| Every push is atomic: all refs or none | A pre-receive hook decides for the whole push anyway | Agreed |
| A push into a protected branch must merge Ready branches, unless the pusher may bypass | Otherwise a direct push skips every requirement | Agreed |
| Merge requirements are branch settings in the default branch's `/devhub.yaml` | Repository and branch settings live in `/devhub.yaml` | Changed |
| Day-one events: the four, plus `tag.pushed`, `review.submitted` and `annotation.changed` | Decided on 2026-10-27 | Changed |
| One Go binary for every workload; internal calls are HTTP with JSON | One version everywhere, one type per call | Agreed |
| REST is the SDKs' surface, GraphQL the Console's | Each where its tooling is strongest | Agreed |
| One host for the Console, the API and Git over HTTPS; the Back-office on its own | First-party cookies, no CORS, an operator UI that can stay internal | Agreed |
| UUIDv7 keys, shown with a type prefix | Sortable keys; `node(id:)` knows the type | Agreed |
| The stack above | Octomaton's proven pieces, contracts first | Agreed |
| Settings take effect from the default branch's `/devhub.yaml`; clients cannot push under `devhub/` | A branch's own settings count only once merged | Agreed |
| Each agent task runs in its own pod, its transcript appended to PostgreSQL as it goes | A restarted pod resumes where it stopped | Agreed |
| The AI reviewer reads the default branch's `/devbot.yaml` | A branch cannot rewrite its own reviewer's instructions | Agreed |

## Open questions

- [ ] **Cache placement.** When should a repository's requests go to the same Git server pods, to cut repeated pack
  downloads? Not needed in the foundations.
- [ ] **Large repositories.** What limits apply to a push's size, and does DevHub support Git LFS, with its objects in
  the bucket?
- [ ] **Cron definitions.** Are cron triggers read from the default branch's `/devhub.yaml` only, or every branch's? The
  CI design answers this.
- [ ] **Job isolation.** One namespace for every organization's jobs, or one each, and with what network policy? The CI
  design answers this too.
- [ ] **Back-office API.** The same API under installation-scope grants, or a separate admin API? [Principals and
  permissions](principals-and-permissions.md#scopes-and-grants) defines the scopes.
- [ ] **Backup and restore.** PostgreSQL and the bucket must be restored to matching points. A backup command, or bucket
  versioning?
- [ ] **Search.** Is PostgreSQL's full-text search enough for issues and messages at first, and when does code search
  arrive?
- [ ] **Settings approval.** Should a change to `/devhub.yaml` need approval from named principals, beyond the default
  branch's merge requirements?
- [ ] **Model providers.** Which model providers agents and the AI reviewer use, and how the installation gives them
  credentials.

DevHub is a Kfirs project, created by Arik Kfir.
