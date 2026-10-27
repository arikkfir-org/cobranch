# DevHub technical architecture

**Status**: agreed on 2026-10-27. This page is the design's source from now on; it was drafted as a [Claude
Doc](https://claude.ai/artifact/7gyNg9dFQXRJtMaFHjYqDj).

DevHub runs as a handful of Go workloads over two stores, PostgreSQL and one blob bucket, and a Git push returns only
after PostgreSQL has committed it. Everything else in this document follows from those two choices.

## Scope

This is the technical design for DevHub's foundations ([ENG-193](https://linear.app/arikkfir/issue/ENG-193)). It turns
the Architecture section of the [DevHub design](https://claude.ai/artifact/ToveH8gtEXkiJcN8KDtxaa) into the workloads
and how they talk, the data they keep, the push path, the API conventions, blob storage and the stack.

Each of these gets its own design and only attaches here: [principals and permissions](principals-and-permissions.md)
([ENG-196](https://linear.app/arikkfir/issue/ENG-196)), jobs and CI, delivery, agents and AI review, issues, and Slack.
Choices this document makes that the product design did not are marked **Agreed** and collected at the end.

## Constraints

Seven constraints from the product design decide most of the architecture.

- **One Helm chart, any cluster.** DevHub installs on any Kubernetes cluster and calls no cloud's API directly. The
  operator brings the domain, ingress, TLS and autoscaling.
- **Stateless workloads.** Any pod can be killed at any time. State lives only in PostgreSQL and in blob storage; a
  pod's disk is a cache.
- **Few moving parts.** An operator runs PostgreSQL and a bucket and nothing else. **Agreed:** no message broker, no
  Redis, no search cluster in the foundations.
- **Synchronous pushes.** When `git push` returns, the commits, branches and their status are already in PostgreSQL and
  on screen.
- **Many organizations per installation.** Every row an organization owns carries its organization's ID, and every query
  is scoped by it.
- **One permission model.** REST, GraphQL and Git check the same grants through the same code, so no surface can be more
  permissive than another.
- **Agents use the front door.** Agents, jobs and AI review call the same APIs as people, with their own principals and
  grants.

## Components

Seven workloads, plus the job and agent pods the controller creates, make up an installation. Only the Git server reads
Git objects, only the push worker moves refs, and only the controller talks to the Kubernetes API.

![All state lives in PostgreSQL and one bucket](architecture/components.svg)

Clients reach three workloads. The Git server holds every push until the push worker commits it, the controller alone
creates pods, and every workload keeps its state in PostgreSQL and the bucket, so any pod can be replaced.

| Workload | What it does | Calls |
| --- | --- | --- |
| Console | Caddy serving the user web app as static files. | Nothing: the browser calls the API server. |
| Back-office | Caddy serving the operator web app, on its own host so the operator can keep it internal. | Nothing: the browser calls the API server. |
| API server | REST, GraphQL with subscriptions, sign-in and sessions; serves uploads and job logs. Runs the database migrations on start. | PostgreSQL, blob storage, identity providers, Git server (content reads, merges). |
| Git server | Git over HTTPS and SSH; an internal API for trees, files, diffs and merges; a disk cache of packs. | PostgreSQL (refs, grants), blob storage (packs), push worker. |
| Push worker | Applies each push to PostgreSQL in one transaction while the Git client waits. | PostgreSQL. |
| Controller | All asynchronous work: delivers events to jobs, webhooks and agents, the AI reviewer included; creates job and agent pods, records job results, and restarts agent pods that die; fires cron events. | PostgreSQL, blob storage, Kubernetes API, outbound webhooks. |
| Maintenance CronJob | Hourly: compacts packs, deletes unreferenced blobs, applies log retention. | PostgreSQL, blob storage. |
| Job pods | A job's pod-spec action, in a namespace of their own. | API server and Git server, with the run's own short-lived token. |
| Agent pods | One long-running pod per agent task. The `devhub agent` harness runs the agent loop with its built-in tools and saves the transcript as it goes, so a restarted pod resumes where it stopped. | API server and Git server, with the task's short-lived token; the model provider. |

The controller is a workload the product design did not list. Pushes need a push worker that stays fast, so the slow,
retried and outward-facing work (pods, webhooks, model calls) moves to a workload of its own rather than sharing the
push worker's pods.

Internal calls (API server to Git server, Git server to push worker) use plain HTTP with JSON on a port the chart never
exposes, authenticated with a shared secret the chart generates. Every workload is the same Go binary, so the two ends
of a call always share one Go type for its body.

## Deployment

The chart runs four images as six Deployments and one CronJob, and optionally PostgreSQL; the controller adds job and
agent pods. The operator supplies a bucket, at least one identity provider and the routes in; PostgreSQL is theirs or
the chart's.

The images are `devhub` (the Go binary plus the `git` CLI, one subcommand per workload), `devhub-console` and
`devhub-back-office` (Caddy with the built web app).

The fourth image, `devhub-agent`, runs agent pods: the binary's `agent` harness plus the agent's built-in tools,
hardened for running commands a model chooses. All four images carry the chart's app version.

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

No workload elects a leader. Replicas share work by claiming rows in PostgreSQL (`FOR UPDATE SKIP LOCKED`), so any
replica can do any piece and a dead one only delays its claimed rows until their lease expires. Every Deployment gets a
disruption budget of one and rolls out by surging; the chart sets no autoscaler.

### Routes

One host serves the Console, the API and Git over HTTPS, split by path, so the session cookie is first-party and the API
needs no CORS. The Back-office gets a second host. The chart can render these routes as an Ingress or a Gateway API
`HTTPRoute`, off by default.

| Host and path | Goes to |
| --- | --- |
| Main host, `/api/`, `/graphql`, `/auth/` | API server |
| Main host, `/<org>/<repo>.git/` | Git server |
| Main host, anything else | Console |
| Back-office host, `/api/` | API server |
| Back-office host, anything else | Back-office |
| SSH, port 22 outside, 2222 on the Service | Git server, exposed by the operator or not at all |

### Dependencies

- **PostgreSQL 16 or later.** The chart's StatefulSet, or an external server named by a connection URL in a Secret.
- **One bucket.** A Go Cloud URL (`s3://`, `gs://`, `azblob://`, or `file://` on a volume for a single-node install).
  Credentials come from the cloud's workload identity or a Secret.
- **Identity providers.** OpenID Connect providers, and GitHub's OAuth, configured in the chart's values.
- **Generated secrets.** Signing keys for sessions and tokens, and the internal call secret. The chart creates them on
  install when absent and never rotates them on upgrade.

Configuration reaches the binary through environment variables only, rendered by the chart.

### Upgrades

The API server migrates the schema on start under a PostgreSQL advisory lock
([ENG-195](https://linear.app/arikkfir/issue/ENG-195)); the other workloads wait until the schema reaches the version
they were built for. Migrations expand before they contract, so pods of the old and the new version run side by side
through a rolling upgrade, and a column is dropped only a release after nothing reads it.

## Data model

PostgreSQL holds every fact DevHub shows, Git refs included; blob storage holds only bytes. One database and one schema
serve every organization, and every row an organization owns carries its `org_id`, even where a join could find it, so
scoping a query never needs a join.

**Agreed:** primary keys are UUIDv7, which sort by creation time. The API shows them with a type prefix (`org_…`,
`repo_…`, `br_…`), which is also how GraphQL's `node(id:)` knows the type. Organizations and repositories also have
slugs for URLs; a rename keeps the old slug as a redirect.

| Area | Table | Key | Holds |
| --- | --- | --- | --- |
| Identity | `principals` | `id` | Every actor: `kind` (user, group, agent, API key, OAuth app, workload identity, job) and display name. Detail in [ENG-196](https://linear.app/arikkfir/issue/ENG-196). |
| Identity | `identities` | `provider`, `subject` | A user's sign-in at an identity provider. A user with none is login-less. |
| Identity | `principal_emails` | `email` | Which principal an email belongs to, and whether it is verified. Maps commit authors. |
| Organizations | `organizations` | `id`; `slug` unique | Name and settings. Organization settings live only here, never in Git. |
| Organizations | `memberships` | `org_id`, `principal_id` | Who belongs. Roles and grants are [ENG-196](https://linear.app/arikkfir/issue/ENG-196)'s. |
| Git | `repositories` | `id`; `org_id`, `slug` unique | Default branch, and a parsed copy of the settings in that branch's `/devhub.yaml`. |
| Git | `refs` | `repo_id`, `name` | Every Git ref and its SHA: the only source of truth for refs. |
| Git | `packs` | `repo_id`, `name` | The repository's packs in blob storage: size, object count, active or superseded. |
| Git | `pushes` | `id` | Who pushed, over which transport, the ref updates, received and committed times. |
| Git | `commits` | `repo_id`, `sha` | Parents, tree, author and committer (name, email, time and principal), message, signature result (Verified, Unverified or Unsigned), first pusher and push. |
| Branches | `branches` | `id`; `repo_id`, `name` unique while live | Base, head, computed status and its reason, ahead and behind counts, merged time, merging principal. Kept after deletion. |
| Branches | `merge_requirements` | `branch_id` | What merging into each branch needs (required annotations, optionally from a named principal, and linear history), copied from the default branch's `/devhub.yaml`. |
| Branches | `annotations` | `id`; `repo_id`, `sha`, `name` unique | Status (pending, in progress, failed, successful), summary, link, creating and last updating principal. |
| Branches | `reviews`, `messages`, `change_requests` | `id` | Reviews on commits; branch chat messages, threads and their code anchors; change requests pointing at messages. |
| Jobs | `job_runs` | `id` | One run of one job on one commit: event, principal, state, pod, times, log location. Detail in the CI design. |
| Agents | `agent_tasks` | `id` | One task of one agent: who assigned it, what it is, its state, its pod, times. Detail in the agents design. |
| Agents | `transcript_entries` | `task_id`, `seq` | Each step of a task's transcript, appended as it happens, so a restarted agent pod resumes from the last one. |
| Work | `events` | `id`, increasing | Append-only record of everything that happened. Feeds reactions, the activity view, audit and live updates. |
| Work | `tasks` | `id`; `kind`, `key` unique while open | Queued work: payload, run-after time, attempts, lease, last error. |

Issues (linked to branches by branch name or by hand), agents and delivery add more tables in their own designs; each of
their rows carries `org_id` too.

A few rules hold across the model:

- **Commits belong to a repository.** The same SHA in two repositories is two rows; the foundations have no forks.
- **SHAs are `bytea`.** Both SHA-1 and SHA-256 repositories fit.
- **History is kept.** Deleting a branch in Git deletes its ref but keeps its `branches` row, so a merged branch's chat,
  reviews and status stay readable.
- **Settings live in Git.** Repository and branch settings, merge requirements included, are read from the default
  branch's `/devhub.yaml`. PostgreSQL keeps a parsed copy for queries, replaced by the push transaction that changes the
  file. Organization settings live in PostgreSQL only.
- **Unknown authors get principals.** The push worker looks a commit's author and committer up by email; one not found
  becomes a new user principal with no identity and an unverified email. A user who later verifies that email takes it
  over; [ENG-196](https://linear.app/arikkfir/issue/ENG-196) designs how.
- **Scoping is the data layer's job.** **Agreed:** every query function takes the organization it works in, and
  PostgreSQL row-level security is a later hardening, not a foundation.

## Git storage and the push path

**Agreed:** a repository is a set of immutable packs in blob storage plus its refs in PostgreSQL, and a Git server pod
only caches packs. A push is accepted by a pre-receive hook that does not exit until the push worker has committed the
push to PostgreSQL.

### Storage

- **Packs are written once.** Each is stored as `git/<repo-id>/pack-<checksum>.pack` with its `.idx`, and never changed.
  The `packs` table lists a repository's active packs.
- **Refs live only in PostgreSQL.** The `refs` table is the repository's ref store. A pack and the refs that need it are
  recorded in the same transaction, so a ref never points at an object no listed pack holds.
- **Each request gets a throwaway repository.** The Git server reads the refs and the pack list in one snapshot, writes
  the refs into a temporary bare repository, and points its `objects/info/alternates` at the pod's cache of that
  repository's packs, downloading any it lacks. The cache evicts the least recently used repositories when its volume
  fills.
- **Git itself speaks the protocol.** The `git` CLI runs `upload-pack`, `receive-pack`, `index-pack` and `repack`, so
  every Git client works and pack negotiation is Git's own; Go drives it and owns everything around it. A pure-Go
  implementation (go-git with its own storage) was the alternative, set aside for protocol coverage and speed on large
  repositories.
- **Packs are compacted.** Every push adds a pack, so the maintenance CronJob repacks a repository whose pack count
  passes a threshold, uploads the result, and swaps the pack list in one transaction. Superseded packs, and packs a
  failed push left behind, are deleted after a day, which lets fetches already in flight finish.

### A push, step by step

![A push returns only after PostgreSQL commits it](architecture/push-path.svg)

The Git server holds the client from step 5 until the transaction commits. Jobs, review and webhooks start afterwards,
from the tasks the transaction queued.

1. The client connects over HTTPS or SSH. The Git server authenticates it and checks its write permission on the
   repository before it reads a byte of the pack.
2. `git receive-pack` writes the pack into Git's quarantine directory and indexes it with `--fix-thin`, so the pack is
   complete on its own.
3. The pre-receive hook, which is the Git server's own binary, builds the push's manifest from the quarantine: the ref
   updates; the new commits, their metadata and signatures; each updated branch tip's `/devhub.yaml`; and the graph
   facts DevHub cannot compute without the objects (which branches now contain their base, which are merged into an
   updated base, ahead and behind counts).
4. The hook uploads the quarantined pack to blob storage.
5. The hook sends the manifest and a push ID to the push worker and waits.
6. The push worker applies it in one transaction: it moves every ref with a compare-and-swap on its old SHA, lists the
   pack, inserts the commits and the push, checks the commits' signatures, maps authors to principals, creates or moves
   branches, recomputes their status, creates a pending annotation for each job the new tips declare, marks merged
   branches Merged with the pusher as the merging principal, applies the new settings when the default branch's
   `/devhub.yaml` changed, appends events and tasks, and `NOTIFY`s.
7. The push worker answers, the hook exits zero, `receive-pack` reports success, and the client returns. The API server
   heard the `NOTIFY` when the transaction committed, so open Consoles are already updating.

**Agreed:** every push is atomic, all refs or none, as `git push --atomic` is. A pre-receive hook decides for the whole
push anyway, and one transaction matches it. While a large push is indexed, the hook writes progress to the client as
`remote:` lines.

Only what the screen must show is synchronous. Jobs, AI review, webhooks, delivery and notifications start from the
tasks the transaction queued, after the client has returned.

A merge DevHub performs takes the same road. The API server asks the Git server to merge; the Git server builds the
merge commit with `git merge-tree --write-tree` (or a fast-forward, or a rebase when linear history is required), writes
the pack, and sends the manifest to the push worker with the requesting principal as the pusher. Only the push worker
ever moves a ref.

Settings take that road as well. Repository and branch settings live in the default branch's `/devhub.yaml`, so a change
made in the Console becomes a commit on the reserved branch `devhub/settings`, which the Git server writes for the
principal who made the change. The change takes effect when that branch merges, through the same merge requirements as
any other branch. **Agreed:** clients cannot push refs under `devhub/`; only DevHub writes them.

**Agreed:** a push into a branch that has merge requirements is accepted only when it merges Ready branches, meaning
every new commit other than a merge commit is in a Ready branch based on it. A bypass permission
([ENG-196](https://linear.app/arikkfir/issue/ENG-196)) lifts the check. Without this rule, a direct push would skip
every requirement.

### Failure modes

| Failure | The client sees | Afterwards | Recovery |
| --- | --- | --- | --- |
| Push worker unreachable or failing | Push rejected: DevHub could not record it | Nothing changed; an unlisted pack in blob storage | The user retries; the pack is deleted after a day |
| Another push moved a ref first | Push rejected: fetch first | Nothing changed | Fetch, merge or rebase, push again, as with any Git server |
| Push worker committed, its answer was lost | Nothing yet: the hook asks again with the same push ID | Committed | The push worker answers a known push ID with its recorded result, so the push succeeds |
| Git server pod dies mid-push | Connection dropped | Either nothing changed or committed | The next fetch shows which; a retry is safe either way |
| Blob storage unavailable | Push rejected; fetches work only from cached packs | Nothing changed | Retry once the bucket is back |
| PostgreSQL unavailable | Push and fetch rejected | Nothing changed | Everything waits for PostgreSQL |
| Push into a branch with requirements merges no Ready branch | Push rejected, naming the requirement | Nothing changed | Push a branch and merge it once Ready, or use the bypass permission |

### A fetch

A clone or fetch authenticates, checks read permission, reads the refs and the pack list in one snapshot, makes sure the
pod's cache holds those packs, and runs `git upload-pack` on a throwaway repository. A compaction that commits during
the fetch does not disturb it: its new pack holds every object of the packs it replaced, and those stay in the bucket
for a day.

## Events and background work

Every change appends an event in the same transaction that makes it, and every reaction is a task row the controller
claims. Nothing is lost between a change and its reaction, and no broker is needed.

- **Events.** The `events` table is append-only and written by the changing transaction itself, an outbox. It is the
  record that jobs, webhooks, the activity view, audit and live updates all read.
- **Tasks.** The same transaction inserts a `dispatch` task per event. Controller replicas claim tasks with `FOR UPDATE
  SKIP LOCKED` under a lease, retry failures with backoff, and park a task after its last attempt, where the Back-office
  shows it. A task's `kind` and `key` are unique while it is open, so queueing one twice is harmless.
- **Dispatch.** A `dispatch` task finds the event's subscribers (jobs whose triggers match, webhooks, agents such as the
  AI reviewer, delivery) and queues one task for each, so a slow webhook never holds up a job.
- **Job runs.** The controller creates the job's pod in the jobs namespace with a Secret holding the run's short-lived
  token, watches its pods, updates the run and its annotation, archives the logs to blob storage when the pod ends, and
  deletes the pod. The CI design owns the rest.
- **Agent tasks.** The controller starts each agent task in a long-running pod of the `devhub-agent` image, in the
  agents namespace, and starts it again if it dies; the harness then resumes from the transcript saved in PostgreSQL.
  The AI reviewer is a built-in agent of every organization, configured by each repository's `/devbot.yaml`, and the
  controller gives it a review task on every push. The agents design owns the rest.
- **Webhooks.** Outbound calls, a job's webhook actions included, are signed with an HMAC of the body and retried with
  backoff.
- **Cron.** A recurring controller task queues each due cron event once a minute. The task key names the job and the
  minute, so a schedule fires once however many replicas run.
- **Live updates.** The transaction sends `NOTIFY` with the event's ID. Each API server listens, loads the event, and
  pushes it to the GraphQL subscriptions whose principals may see it. A client that reconnects passes the last event ID
  it saw, and the API server replays from `events`.
- **Maintenance.** The hourly CronJob compacts packs, deletes unreferenced blobs, applies retention to logs, and drops
  expired sessions and tokens.

Day-one events, as decided on 2026-10-27: `branch.created`, `branch.pushed`, `branch.merged` and `branch.deleted`, plus
`tag.pushed`, `review.submitted` and `annotation.changed`. Jobs also take cron and manual triggers.

One PostgreSQL carries this far past a first installation's load. The event and task stores are interfaces in the code,
so a broker can replace them later without the services changing.

## API conventions

REST and GraphQL are two thin transports over one Go service layer, which owns validation and authorization, so the two
can never disagree about who may do what. Both serve authenticated principals only.

### Shape

- **REST** lives under `/api/v1/`, with paths that follow ownership
  (`/api/v1/orgs/{org}/repos/{repo}/branches/{branch}`). It is described by an OpenAPI 3.1 document written first and
  checked in, from which the Go server stubs and the Go, Python and TypeScript SDKs are generated. **Agreed:** REST is
  the SDKs' surface.
- **GraphQL** is one schema at `/graphql`, also written first, and is the Console's surface. It follows the Relay
  conventions (`node(id:)`, connections) and carries live updates as subscriptions over WebSocket.
- **One shape for an object.** JSON field names are camelCase in both, timestamps are RFC 3339 in UTC, and IDs are the
  typed strings of the data model, so an object reads the same in either.

### Authentication

Every credential resolves to one principal, and the grants of that principal decide the rest.
[ENG-196](https://linear.app/arikkfir/issue/ENG-196) designs the principals and grants; these are the credentials each
caller presents.

| Caller | Credential |
| --- | --- |
| Browser, Console or Back-office | A session cookie (`HttpOnly`, `Secure`, `SameSite=Lax`) naming a revocable session row, set by `/auth/` after sign-in at an identity provider |
| Script, SDK or outside agent | An API key as a bearer token, shown once and stored hashed |
| OAuth app | An access token from DevHub's own OAuth authorization server, in a later slice |
| Workload on another platform | Its platform's OpenID Connect token, exchanged for a short-lived DevHub token (OAuth token exchange), so it stores no secret |
| Job pod or agent run in DevHub | A short-lived token minted for the run, acting as the job's or agent's principal |
| Git over HTTPS | A user's personal token, or any token above, as the Basic password |
| Git over SSH | An SSH key registered to a principal |

Cookie sessions can only send mutations as JSON bodies, which a cross-site form cannot produce, on top of `SameSite`.

### Pagination

Every list is paged by cursor. REST takes `first` and `after` and answers `{"items": […], "nextCursor": "…"}`; GraphQL
returns Relay connections with the same cursors. **Agreed:** pages default to 50 items and stop at 100. A cursor is
opaque, encodes the sort key and the ID, and stays valid while rows are inserted. There are no offsets, and a total
count appears only where it is cheap.

### Errors

- **REST** answers with problem details (RFC 9457, `application/problem+json`): `type`, `title`, `status` and `detail`,
  plus a stable `code` such as `branch.not_found` and the `requestId`. A validation error lists each field with its own
  code.
- **GraphQL** puts the same `code` in each error's `extensions`.
- **One catalog.** The codes are defined once in the service layer, and the SDKs expose them as constants.
- **Existence is a secret.** An object the caller may not see is `404`, never `403`; `403` means the caller sees it but
  may not do this. A stale update is `409`, and a rate limit is `429` with `Retry-After`.

### Versioning

- **REST** carries its major version in the path. Within `v1` changes are additive only; a breaking change ships as `v2`
  beside `v1`, and the old version announces its end with `Deprecation` and `Sunset` headers.
- **GraphQL** is never versioned. A field is marked `@deprecated` and removed no sooner than two minor releases later.
- **SDKs** carry the product's version.

### Writes and tracing

- **Idempotency.** A REST `POST` accepts an `Idempotency-Key`, and a repeat within a day returns the first answer.
- **Optimistic concurrency.** Mutable objects carry a version. REST updates send it as `If-Match`, GraphQL mutations as
  `expectedVersion`, and a mismatch is `409`.
- **Tracing.** Requests may carry a W3C `traceparent`, which every hop propagates, and every response names its request
  ID.

## Blob storage

One bucket, reached only through Go Cloud's `blob` package, holds every large or binary object; PostgreSQL holds their
keys and decides who may read them. Keys are built from IDs, never names, so a rename moves nothing.

| Prefix | Holds | Written by | Kept |
| --- | --- | --- | --- |
| `git/<repo-id>/` | Packs and their indexes | Git server on push; maintenance on compaction | Until superseded or never listed, then one more day |
| `logs/<repo-id>/<run-id>/` | A finished job run's logs, one object per step | Controller | For the retention the operator sets, 90 days by default |
| `uploads/<org-id>/<upload-id>` | Files attached to messages, reviews and issues; avatars | API server | While something refers to them |
| `artifacts/<repo-id>/<run-id>/` | Files a job run publishes | Job pods, through the API server | Set by the CI design |
| `agents/<org-id>/<task-id>/` | Large tool outputs and files an agent task produces; its transcript stays in PostgreSQL | Agent pods, through the API server | As long as the task is kept |

- **Reads go through permissions.** The API server checks the caller's grant, then streams the object, or redirects to a
  signed URL valid for minutes where the bucket's driver can sign (S3, Google Cloud Storage, Azure).
- **Objects are written whole.** Object stores cannot append, so a running job's logs come from the Kubernetes API while
  its pod lives, and from the bucket once the controller has archived them.
- **Deletion follows ownership.** Deleting a repository or an organization queues a task that deletes its prefixes.
- **Encryption at rest is the bucket's,** configured by the operator.

## Stack and repository layout

**Agreed:** Go for every service and TypeScript with React for both web apps, in one monorepo that builds one server
image and two web images. The contracts (OpenAPI, GraphQL schema, SQL) are written first and the code is generated from
them.

| Concern | Choice | Why |
| --- | --- | --- |
| Services | Go 1.27; one binary, `devhub <workload>` | Octomaton's language and layering carry over; Go Cloud and `client-go` are Go; one binary keeps every workload at one version |
| HTTP | The standard library's `net/http` routing | No framework to outlive |
| REST | OpenAPI 3.1 written first; `oapi-codegen` generates the server | The document is the contract and the SDKs' source |
| GraphQL | Schema written first; `gqlgen` | Typed resolvers generated from the schema |
| PostgreSQL | `pgx`; `sqlc` generates Go from SQL queries; `goose` migrations embedded in the binary | SQL stays SQL and is checked at build time |
| Git | The `git` CLI in the image, driven by Go; `golang.org/x/crypto/ssh` for the SSH server | Wire compatibility and Git's own performance |
| Blob storage | `gocloud.dev/blob` | One API over every cloud's buckets and a local directory |
| Kubernetes | `client-go`, in the controller and for the API server's log reads | The only two places that touch the cluster |
| Sign-in | `go-oidc` with `golang.org/x/oauth2` | Standard OpenID Connect |
| Configuration | Environment variables, read with `envconfig` | As Octomaton does |
| Telemetry | `log/slog` as JSON on stdout; OpenTelemetry traces and metrics over OTLP, to wherever the operator points them | No cloud's monitoring built in |
| Web apps | TypeScript, React and Vite, built to static files that Caddy serves | Static files need no Node.js at run time |
| Web data | GraphQL with types from GraphQL Code Generator, through the urql client | Typed queries; subscriptions for live updates |
| Web components | Headless primitives styled with CSS variables exported from the Figma file's variables | Settled with the design system ([ENG-194](https://linear.app/arikkfir/issue/ENG-194)) |
| SDKs | Generated from the OpenAPI document for Go, Python and TypeScript, released with the product | One contract, three languages |
| Tests | Table-driven Go tests; integration tests on a real PostgreSQL and a `file://` bucket; end-to-end tests that install the chart on kind and push with real `git` | The push path is only proven end to end |

The monorepo ([ENG-192](https://linear.app/arikkfir/issue/ENG-192)) follows Octomaton's layering: services hold the
logic in DevHub's own terms and import no I/O library, adapters implement them, and a test fails the build when a
dependency points outward.

```
go.mod                        one module: github.com/arikkfir-org/devhub
cmd/devhub/                   the binary: api-server, git-server, push-worker, controller, maintenance, agent
internal/services/            domain logic: organizations, repositories, branches, annotations, events
internal/adapters/            postgres, blob, git, kube, rest, graphql, oidc
internal/system/              configuration, telemetry, build information
api/openapi.yaml              the REST contract
api/graphql/                  the GraphQL schema
db/migrations/                numbered SQL migrations
web/console/                  the Console
web/back-office/              the Back-office
web/ui/                       components shared by both, from the design system
sdk/go/  sdk/python/  sdk/typescript/
chart/devhub/                 the Helm chart
docs/devhub/                  these designs, published to the docs site under devhub/
.octomaton.yaml  .tekton/     DevHub's own CI, on Octomaton until DevHub can build itself
```

## Decisions

Sixteen choices here went beyond the product design. The decisions of 2026-10-27 changed two of them and added three
that follow from them, and the rest were agreed the same day.

| Proposal | Why | Decision |
| --- | --- | --- |
| PostgreSQL and one bucket are the only stateful dependencies; `NOTIFY` and a task table stand in for a broker | One less system for every operator to run | Agreed |
| A repository is immutable packs in the bucket plus refs in PostgreSQL; Git server pods only cache | Keeps the Git server stateless, as the product design asks | Agreed |
| The `git` CLI speaks the protocol, driven by Go | Every client works, at Git's own speed | Agreed |
| A controller workload takes all asynchronous work | Keeps the push worker fast | Agreed |
| Every push is atomic: all refs or none | A pre-receive hook decides for the whole push anyway | Agreed |
| A push into a branch with requirements must merge Ready branches, unless the pusher may bypass | Otherwise a direct push skips every requirement | Agreed |
| Merge requirements are branch settings in the default branch's `/devhub.yaml` | Decided on 2026-10-27: repository and branch settings live in `/devhub.yaml` | Changed |
| Day-one events: the four, plus `tag.pushed`, `review.submitted` and `annotation.changed` | Decided on 2026-10-27 | Changed |
| One Go binary for every workload; internal calls are HTTP with JSON | One version everywhere, one type per call | Agreed |
| REST is the SDKs' surface, GraphQL the Console's | Each where its tooling is strongest | Agreed |
| One host for the Console, the API and Git over HTTPS; the Back-office on its own | First-party cookies, no CORS, an operator UI that can stay internal | Agreed |
| UUIDv7 keys, shown with a type prefix | Sortable keys; `node(id:)` knows the type | Agreed |
| The stack in the table above | Octomaton's proven pieces, contracts first | Agreed |
| Settings take effect from the default branch's `/devhub.yaml`, copied into PostgreSQL by the push that changes it; clients cannot push under `devhub/` | A branch's own settings take effect only once merged, and `devhub/settings` stays DevHub's | Agreed |
| Each agent task runs in its own pod, and its transcript is appended to PostgreSQL as it goes | A restarted pod resumes where it stopped, with no volume to keep | Agreed |
| The AI reviewer reads the default branch's `/devbot.yaml` | A branch cannot rewrite its own reviewer's instructions | Agreed |

## Open questions

- [ ] **Cache placement.** When should a repository's requests go to the same Git server pods, to cut repeated pack
      downloads? Not needed in the foundations.
- [ ] **Large repositories.** What limits apply to a push's size, and does DevHub support Git LFS, with its objects in
      the bucket?
- [ ] **Cron definitions.** Are cron triggers read from the default branch's `/devhub.yaml` only, or from every
      branch's? The CI design answers this.
- [ ] **Job isolation.** Do every organization's jobs share one namespace, or does each organization get its own, and
      with what network policy? The CI design answers this too.
- [ ] **Back-office API.** Is it the same API under installation-scope grants, or a separate admin API?
      [ENG-196](https://linear.app/arikkfir/issue/ENG-196) defines the scopes.
- [ ] **Backup and restore.** PostgreSQL and the bucket must be restored to matching points. Does DevHub ship a backup
      command, or ask for bucket versioning?
- [ ] **Search.** Is PostgreSQL's full-text search enough for issues and messages at first, and when does code search
      arrive?
- [ ] **Settings approval.** Should a change to the settings in `/devhub.yaml` need approval from named principals,
      beyond the default branch's merge requirements?
- [ ] **Model providers.** Which model providers agents and the AI reviewer use, and how the installation gives them
      credentials.