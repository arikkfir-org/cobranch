# Cobranch

Cobranch is an all-in-one, opinionated development platform for individuals and teams, with AI central to the development process from every angle. It hosts the code, runs the jobs, delivers the result and holds the conversation around each change, so a developer or a team needs nothing else to ship software.

It is a product, not an installation: anyone installs it on their own Kubernetes cluster with one Helm chart and gets a working hub, with sign-in, organizations, Git hosting, CI, GitOps delivery, APIs and SDKs.

Cobranch has two faces: the open-source product at cobranch.dev, which anyone downloads and runs as their own
installation, and cobranch.app, our own installation of it, a paid self-serve service in the way github.com and
gitlab.com are. This repository is the product; running cobranch.app is a later slice, and the product never assumes it.
Cobranch was called DevHub until its rename ([ENG-221](https://linear.app/arikkfir/issue/ENG-221)); version 0.0.1 was
published under that name.

Cobranch is a Kfirs project, created by Arik Kfir, and is released under the [MIT License](LICENSE).

## Status

Foundations. The Helm chart installs Cobranch's skeleton, whose servers answer only their version ([ENG-195](https://linear.app/arikkfir/issue/ENG-195)), pipeline `main` publishes the images of `main`'s newest commit, and each GitHub release publishes the chart at its version ([ENG-207](https://linear.app/arikkfir/issue/ENG-207)). Sign-in through any OpenID Connect provider, sessions, organizations with their members, invitations and join requests, their roles, built in and custom, with the grants of them, and their API keys, which scripts send as bearer tokens, are in ([ENG-197](https://linear.app/arikkfir/issue/ENG-197), [the design](docs/cobranch/designs/sign-in-and-organizations.md)). Git hosting has begun: an organization's repositories, with the grants of their roles, and personal tokens, which act as their user narrowed to the permissions they list, through the APIs and the Console; and the Git server, which clones, fetches and takes pushes over HTTPS with a personal token, recording each push before it answers, with the push worker, which then maps each push's commits to their authors; and a repository's code through the APIs and the Console: its branches, a branch's first-parent history and its commits, with the users behind them and the pushes that brought them. Pack compaction and Git over SSH, with the SSH keys users add through the APIs and the Console, are in too ([ENG-198](https://linear.app/arikkfir/issue/ENG-198), [the design](docs/cobranch/designs/git-hosting.md)). Browsing code is in: a repository's directories and files at any branch, tag or commit, with Markdown rendered as GitHub renders it and each directory's README beneath its listing, and its history through every parent from any of them, with a graph of its lines in the Console, through the APIs and the Console, which the REST and GraphQL servers read from the Git server over an internal call that carries the viewer's own token, so the Git server itself checks who reads ([ENG-228](https://linear.app/arikkfir/issue/ENG-228), [the design](docs/cobranch/designs/code-browsing.md)). Branch status is in: principals set annotations on commits through the APIs, such as a CI system's verdict, and the Console shows them on a commit's page and in a branch's history; `main`'s `/cobranch.yaml` names the annotations a merge needs and whether history stays linear, and every other branch shows its status as a request to merge into `main`, Processing, Blocked, Empty, Merged or Ready, with the checks behind it, through the APIs and the Console; a push into a branch with merge requirements brings only Ready branches' commits, unless its pusher holds `repo.bypass`, and a branch a push brings into `main` turns Merged, naming who merged it; a principal holding `repo.merge` also asks Cobranch to merge a Ready branch, by a merge commit or a fast-forward, through the APIs or the Console's Merge button, and one holding `repo.bypass` too merges any branch ([ENG-199](https://linear.app/arikkfir/issue/ENG-199), [the design](docs/cobranch/designs/branch-status.md)). Deleting a branch is in too: a principal holding `repo.push` deletes one through the APIs, or from its page in the Console or as they merge it there, recorded as a push that deletes its ref and naming who deleted it, while its commits and its merge stay ([ENG-241](https://linear.app/arikkfir/issue/ENG-241), [the design](docs/cobranch/designs/branch-status.md#deleting-a-branch)). Operators' metrics are in: how long each Console page takes to show and how many Git reads it takes, how long each REST route and GraphQL field takes, what each of the Git server's throwaway repositories costs and how often its pack cache hits, exported over OpenTelemetry, pushed over OTLP or scraped in Prometheus's format ([ENG-235](https://linear.app/arikkfir/issue/ENG-235), [the design](docs/cobranch/designs/monitoring.md)). Delivery has begun: `/cobranch.yaml`'s `delivery` key maps branch patterns to targets, each a cluster and a namespace, and names the roots each branch renders, Helm charts, kustomizations and plain manifests, and `cobranch render` prints what a delivery of a commit to one of its targets would apply; every push to a branch a target matches makes the branch's environment of that target on its first push and queues a delivery of the pushed commit, which sets the target's annotation on it, and members list a repository's environments and their deliveries, redeliver, pause and resume an environment and start a waiting delivery through the APIs and the Console. The controller delivers into the installation's own cluster, when the chart's `delivery.inCluster.enabled` lets it: it renders each delivery in a pod of its organization's job namespace, which uploads the render with the delivery's token, then applies it to the environment's namespace, which it makes and fences, as an account bound there alone, prunes what the render stopped holding, and follows the environment's health until it is Healthy, which the target's annotation on the commit follows. It delivers into other clusters too, through the kubeconfig in the organization secret a target names, applies each environment's last render again every 10 minutes, and tears an environment down when its branch is deleted or `main`'s targets no longer give it one: it deletes what the environment held, and the namespace it made ([ENG-218](https://linear.app/arikkfir/issue/ENG-218), [the design](docs/cobranch/designs/delivery.md)). Issues have begun: an organization's members create, assign and move issues through six states, Backlog to Canceled, each named by the organization's issue key and a number, such as `ACME-42`, with a priority, an assignee, the repository it is about and its activity, through the APIs and the Console; Owners also delete them, and change the key. A branch works on the issue its name names, such as `acme-42-login`, or on one a person links it to by hand, and the issue lists its branches: linking a branch moves a Backlog or Todo issue to In progress, and merging it into `main` moves its issue to Done. Issues carry the organization's labels, which its Owners manage, and the list filters by them. People comment on an issue in threads of Markdown messages, its chat, which they follow live as a branch's. An issue assigned to one of the organization's agents hands the agent a task of it, which reassigning the issue cancels ([ENG-217](https://linear.app/arikkfir/issue/ENG-217), [the design](docs/cobranch/designs/issues.md)). Branch chats are in: every branch carries a chat of threads of Markdown messages, a thread perhaps on lines of a file at a commit, which people start, answer, edit, delete and resolve through the APIs and the Console, and follow live, over a GraphQL subscription or the REST API's long poll; and an organization's agents, principals it owns with roles of their own, which members holding `agents.create` make and those holding `agents.assign` assign numbered tasks to, each one capped by its assigner's permissions, through the APIs and the Console. Admins add the model providers the installation knows, each with the organization secret its API key is in, put each agent on one of their models, and name the secrets its tasks receive. A task's token, `cba_…`, acts as its agent capped by its assigner, only while the task hasn't ended, and appends the task's transcript, which the task's page in the Console shows as it grows. The controller runs each task in a pod of its own, whose harness works it with the agent's model and tools, the provider's key held by a model proxy beside it; agents talking in these chats follow ([ENG-215](https://linear.app/arikkfir/issue/ENG-215), [the design](docs/cobranch/designs/agents.md)). Quotas are in: a user creates 5 organizations for themselves, and an organization holds 100 repositories and stores 1 GiB of Git data, unless the chart's values or administrators in the Back-office set other limits, for the installation or for one organization, and a creation or push that would cross one is refused with a message naming it; the APIs and the Console show an organization's usage against its limits, and a user how many organizations they created against theirs ([ENG-238](https://linear.app/arikkfir/issue/ENG-238), [the design](docs/cobranch/designs/quotas.md)). Jobs have begun: a repository declares them in `/cobranch.yaml` under `jobs`, and every push records each branch tip's file and queues the runs its pushes and tags trigger, each as the job's own principal reporting the annotation of its name, Pending while it waits; a newer run on a ref supersedes the job's older ones, runs of a tip pushed from outside the organization wait for a member to start them unless `main`'s file says `repository.outsidePushes: run`, and a job asking for more than `main`'s ceiling role fails; members list, start and cancel runs through the APIs and the Console. Organizations and repositories keep secrets in their settings, sealed with the installation's secrets key and never shown again, and grant them to jobs with the Secret reader role, on the repository or on one secret. The controller runs pods and scripts: each organization's runs execute in a namespace of their own, which it makes with NetworkPolicies that admit no ingress and egress only to DNS, the installation's servers and the internet, after an init container checks the run's commit out with the run's token; each receives the secrets its job may read, and its containers' logs, masked, are kept in the object store as it ends. Annotations, reviews, events that members and API keys post with their data, manual starts and schedules trigger runs too, an annotation set by a run naming it as the cause of the runs it triggers, and members re-run ended runs as their next attempts, through the APIs and the Console. Webhook jobs are in too: the controller posts each run, signed with its job's secret, to its receiver, from addresses the chart's values admit, and the receiver reports the run's end with its token. A run's logs are read through the APIs and the Console, live from its pod while it runs and from the object store once it ends, which keeps them for 30 days by default ([ENG-214](https://linear.app/arikkfir/issue/ENG-214), [the design](docs/cobranch/designs/jobs.md)). The work is tracked in Linear's [Cobranch project](https://linear.app/arikkfir/project/cobranch-56ee907dd1af), and the designs are cross-linked pages that start from the [product design](https://claude.ai/artifact/BsCFv8jQ7XunQP2T64Sjoy); [`docs/cobranch/designs/`](docs/cobranch/designs/) holds snapshots of them.

## Layout

One repository holds every part of Cobranch, laid out as the [technical architecture](docs/cobranch/designs/architecture.md) agreed. Paths marked _planned_ arrive with the slices that need them.

| Path | What it holds |
| --- | --- |
| `cmd/cobranch/` | The one binary: a subcommand per workload (the API servers `auth-server`, `rest-server`, `graphql-server` and `admin-server`, the `migrate` Job, Git server, push worker, controller, `maintenance`, agent), so every workload runs the same build at one version; today `auth-server`, `rest-server`, `graphql-server`, `admin-server`, `migrate`, `git-server`, `push-worker` (with `git-hook`, the Git server's pre-receive hook), `controller` (which runs jobs' pods and scripts and delivers, with `checkout`, which fetches a run's commit, and delivers webhook jobs' runs), `maintenance`, `render` (what a delivery renders, which a delivery's pod and a developer's checkout run), `agent` (the harness that works an agent's task, in the pod its controller starts), `model-proxy` (beside it in that pod, the one container that holds the model provider's key) and `version` |
| `internal/` | The Go packages: `services/` (the domain: `auth`, sign-in, sessions, personal tokens, SSH keys and the authorizer; `orgs`, organizations, members, invitations, join requests, roles, grants and API keys; `repos`, repositories and the grants on them; `issues`, an organization's issues, their labels and their activity; `chats`, the branches' and the issues' chats and their live updates; `agents`, an organization's agents, their tasks and the tasks' transcripts; `harness`, the loop that works a task over its agent's model and tools; `runner`, which runs queued job runs as pods and follows them to their end; `taskrunner`, which runs queued agents' tasks in pods of their own and follows them to their end; `deliverer`, which renders queued deliveries in pods, applies their renders and follows their environments' health; `git`, what a fetch serves and the record of pushes; `code`, a repository's branches, history, commits, directories and files as people browse them, the annotations principals set on its commits, and each branch's status with the merge requirements behind it, and its environments and their deliveries; `delivery`, the targets a branch delivers to and what a delivery renders for each, from `/cobranch.yaml`'s `delivery` key, which `code` parses; `admin`, the installation's administration), `adapters/` (transports, storage, outside systems: `authserver`, `oidc`, `postgres`, `rest`, `graphql`, `adminapi`, `server`, `blob`, `gitserver`, `gitinternal`, `markdown`, `pushworker`, `render`, which renders a delivery's charts, kustomizations and manifests, `cluster`, which applies a render to its target cluster, prunes what it dropped and reads its objects' health, `jobcluster`, which makes job namespaces and runs jobs' pods, agents' task pods and deliveries' render pods in them, `incluster`, which makes and fences environments' namespaces in the installation's own cluster and applies there as the delivery account, `controller`, which drives the runner, the task runner and the deliverer, `checkout`, which fetches a run's commit, `webhook`, which posts webhook jobs' runs to their receivers, `models`, the model providers agents run on, `restclient`, the REST API's calls the harness makes as its task's token, `workspace`, the tools over the task's workspace) and `system/` (configuration, telemetry, build information) |
| `web/console/` | The Console, which Caddy serves as static files (image `cobranch-console`): sign-in, the welcome page (create, join, invitations), an organization's home, issues with their comments, members, roles, API keys, agents, tasks and settings under its slug, its repositories' pages under theirs, with their files at any branch, tag or commit, Markdown and READMEs rendered, branches with their status, a Merge button, a Delete button and their chats, live, with threads on lines a file's page picks, history through every parent with its graph, and commits with their annotations, and the viewer's settings with their personal tokens and SSH keys, routed in the browser |
| `web/back-office/` | The Back-office, which Caddy serves as static files (image `cobranch-back-office`): an installation's organizations (search, create one for a user, enter one) and its users, routed in the browser |
| `web/ui/` | _Planned_: the components the web apps share |
| `api/` | The API contracts, from which `go generate ./...` writes the servers' stubs: REST (`openapi.yaml`), GraphQL (`graphql/`) and the Admin API (`admin.yaml`), which the Back-office calls |
| `db/migrations/` | PostgreSQL's migrations, SQL and Go, embedded in the binary; `cobranch migrate` applies them before the servers roll out. Trigger `audit()` on the audited tables writes `audit_log` with the principal each transaction acts for |
| `db/queries/` | The stores' SQL, from which `go generate ./...` writes `internal/adapters/postgres/queries` with [sqlc](https://sqlc.dev) (`sqlc.yaml`), checked against the schema; no Go file writes a query but the schema version's read of goose's table |
| `db/seeds/` | Demo data, in Go over queries sqlc writes from its `queries.sql`, in every build; `cobranch migrate` adds it after the schema only with `COBRANCH_SEED` (the chart's `seed.enabled`), as a demo installation does |
| `sdk/` | _Planned_: the Go, Python and TypeScript SDKs |
| `chart/cobranch/` | The Helm chart that installs Cobranch |
| `Dockerfile` | Image `cobranch`: the binary, with `git` |
| `docs/cobranch/` | The design documents, published to the docs site under `cobranch/` |
| `.octomaton.yaml`, `.tekton/` | CI, run by [Octomaton](https://github.com/arikkfir-org/octomaton) |

## Development

Go 1.27, [golangci-lint](https://golangci-lint.run) 2.15 and a PostgreSQL 18 server for the tests that need one (each
makes its own database there; without `COBRANCH_TEST_DATABASE_URL` they are skipped). The cluster adapter's tests also start a throwaway API server, from the `kube-apiserver` and `etcd` binaries in the directory `COBRANCH_TEST_KUBE_ASSETS` names, which `.tekton/scripts/kube-assets.sh` downloads into `.ci/kube` on Linux, at the versions CI pins; without it they are skipped:

```bash
docker run -d --name cobranch-postgres -p 5432:5432 -e POSTGRES_HOST_AUTH_METHOD=trust postgres:18.6-alpine
export COBRANCH_TEST_DATABASE_URL='postgres://postgres@localhost:5432/postgres?sslmode=disable'

go generate ./...                      # the REST and GraphQL stubs from api/, the queries from db/; CI fails if they differ
go vet ./... && go test -race ./...    # every test
sh .tekton/scripts/kube-assets.sh && COBRANCH_TEST_KUBE_ASSETS=$PWD/.ci/kube go test ./internal/adapters/cluster/    # also against an API server
golangci-lint run ./...                # the house rules' linters (.golangci.yml)
go build -o bin/ ./cmd/...             # bin/cobranch
bin/cobranch version
```

The workloads read `COBRANCH_` variables. `migrate` brings a database's schema to the build's version and exits; each
server needs the database, and turns ready once the schema is at that version. The Git server and maintenance keep
Git's packs in it too, unless `COBRANCH_BLOB_URL` names a bucket or a directory, and, while packs move from another
store, read that one too, which `COBRANCH_BLOB_MOVE_FROM` names:

```bash
export COBRANCH_DATABASE_URL="$COBRANCH_TEST_DATABASE_URL"
export COBRANCH_SECRETS_KEY="$(openssl rand -hex 24)"    # keep it: it seals the Admin server's providers' secrets and every organization's and repository's secrets
bin/cobranch migrate
bin/cobranch rest-server                                 # :8080: /healthz, /readyz, /v1/version, /v1/me, /v1/orgs/…
COBRANCH_HTTP_ADDR=:8081 bin/cobranch graphql-server     # :8081: /healthz, /readyz, GraphQL at /
COBRANCH_HTTP_ADDR=:8083 COBRANCH_BACK_OFFICE_URL=http://localhost:3001 \
  bin/cobranch admin-server                              # :8083: /healthz, /readyz, /v1/orgs, /v1/users, …
```

`auth-server` signs people in. It needs the Console's URL and its own, and an identity provider or the demo data
(`COBRANCH_SEED=true` with `migrate`, then with `auth-server`, lets the sign-in page sign in as a demo user);
`maintenance` deletes expired sign-in flows, sessions, invitations and join requests once and exits; given `COBRANCH_GIT_COMPACT_AT`, it also deletes the packs nobody lists any more and compacts the repositories that have that many packs or more, working in `COBRANCH_GIT_DATA_DIR`:

```bash
export COBRANCH_CONSOLE_URL=http://localhost:3000 COBRANCH_AUTH_URL=http://localhost:8082
export COBRANCH_AUTH_PROVIDERS='[{"id":"google","name":"Google","preset":"google","clientId":"…"}]'
export COBRANCH_AUTH_CLIENT_SECRET_GOOGLE='…'          # the redirect URI is $COBRANCH_AUTH_URL/callback
COBRANCH_HTTP_ADDR=:8082 bin/cobranch auth-server        # :8082: /providers, /sign-in, /callback, /sign-out
COBRANCH_GIT_COMPACT_AT=8 COBRANCH_GIT_DATA_DIR=/tmp/cobranch-git bin/cobranch maintenance
```

They log text here and JSON in a pod (`COBRANCH_LOG_FORMAT` picks one, `COBRANCH_LOG_LEVEL` the level), and send traces
and metrics wherever OpenTelemetry's own variables point, such as `OTEL_EXPORTER_OTLP_ENDPOINT=http://localhost:4318`, or nowhere
([Observability](https://github.com/arikkfir-org/cobranch/blob/main/docs/cobranch/designs/architecture.md#observability)); `OTEL_METRICS_EXPORTER=prometheus` serves the metrics at `http://localhost:9464/metrics` instead
([Monitoring](docs/cobranch/designs/monitoring.md)).

`render` needs no database: from a checkout's `/cobranch.yaml`, it prints, as one YAML stream, what a delivery of a commit of a branch to one of the branch's targets would apply ([the design](docs/cobranch/designs/delivery.md#roots-and-rendering)); a delivery's pod, whose `COBRANCH_UPLOAD_URL` and `COBRANCH_TOKEN` name its delivery and its token, uploads it instead, since it may hold Secrets. It takes the target from the checkout's own file, or, as a delivery's pod does, with `--namespace`, the environment's namespace, which `main`'s targets grant:

```bash
bin/cobranch render --target preview --branch feature/login --commit "$(git rev-parse HEAD)" \
  --kube-version v1.33.4    # .Capabilities for charts; --api-versions adds the cluster's API groups
```

`agent` works one task of an agent and exits once it ends: the controller runs it in the task's pod, beside `model-proxy`, which holds the provider's key and adds it to the harness's model calls ([the design](docs/cobranch/designs/agents.md#the-agents-pod)). Run by hand, it takes a running task's token in a file, the agent's provider, model and key, and a directory to work in; `COBRANCH_MODEL_URL` points it at another address that speaks the provider's protocol, such as a model proxy, which then needs no key, and `COBRANCH_MAX_TURNS`, `COBRANCH_SHELL_TIMEOUT` and `COBRANCH_MODEL_MAX_TOKENS` bound it:

```bash
COBRANCH_REST_URL=http://localhost:8080 COBRANCH_TOKEN_FILE=/tmp/task-token \
COBRANCH_MODEL_PROVIDER=anthropic COBRANCH_MODEL=… COBRANCH_MODEL_KEY_FILE=/tmp/model-key \
COBRANCH_WORKSPACE=/tmp/workspace bin/cobranch agent
COBRANCH_MODEL_PROVIDER=anthropic COBRANCH_MODEL_KEY_FILE=/tmp/model-key bin/cobranch model-proxy   # on 127.0.0.1:8090
```

CI runs the same on every pull request and merge queue group (check `Continuous Integration`), beside the organization's checks `Docs`, `Linear` and the AI review, pipeline `main` publishes the images of `main`'s newest commit, pipeline `tag` each version's images and chart, and pipeline `preview` each pull request's images, for a preview installation of its own, seeded with demo data (`db/seeds/`). How the repository and its CI fit together: [the Cobranch repository](docs/cobranch/designs/repository.md).

## Installing

Install a version of the chart, which is published beside its images; its guide,
[`chart/cobranch/README.md`](chart/cobranch/README.md), covers the values and upgrades:

```bash
helm install cobranch oci://me-west1-docker.pkg.dev/arikkfir/cobranch/charts/cobranch --version 1.2.3 \
  --namespace cobranch --create-namespace
helm test cobranch --namespace cobranch
```

A version is a [GitHub release](https://github.com/arikkfir-org/cobranch/releases): publishing it creates tag `v1.2.3`,
whose pipeline `tag` adds `1.2.3` to the images pipeline `main` published for the commit and publishes the chart at
`1.2.3` ([Releases](docs/cobranch/designs/repository.md#releases)). Pipeline `main` publishes images `cobranch`,
`cobranch-console` and `cobranch-back-office` of `main`'s newest commit to `me-west1-docker.pkg.dev/arikkfir/cobranch`, tagged with the commit's
first seven characters; an older commit may have none, since a push that a newer one overtakes while it waits is never
built. To follow `main`, install the chart from a checkout with its commit's images:

```bash
helm install cobranch chart/cobranch --namespace cobranch --create-namespace \
  --set-string images.cobranch.tag=$(git rev-parse HEAD | cut -c1-7) \
  --set-string images.console.tag=$(git rev-parse HEAD | cut -c1-7) \
  --set-string images.backOffice.tag=$(git rev-parse HEAD | cut -c1-7)
```

For a commit of your own, build the images, push them where your cluster pulls from, and install with those
repositories and tag `dev`:

```bash
docker build --build-arg VERSION=dev -t registry.example.com/cobranch:dev .
docker build -t registry.example.com/cobranch-console:dev web/console
docker build -t registry.example.com/cobranch-back-office:dev web/back-office
docker push registry.example.com/cobranch:dev && docker push registry.example.com/cobranch-console:dev
docker push registry.example.com/cobranch-back-office:dev
```

Every workload serves as if it were alone, so you choose where each one lives: a host each, or paths on one host
(`https://cobranch.example.com/api`), the prefix stripped. Route them from your Gateway, or set `urls.console`,
`urls.rest`, `urls.graphql`, `urls.auth`, `urls.admin`, `urls.backOffice` and `urls.git` with `httpRoute.enabled` for an
HTTPRoute each. Git over SSH, with `urls.gitSsh`, takes no route: expose the Git server's SSH Service yourself. Logs go to stdout as JSON; to see
traces, set `telemetry.traces.endpoint` to your OpenTelemetry Collector's or a vendor's OTLP endpoint, which receives the metrics too, or set
`telemetry.metrics.exporter: prometheus` for your Prometheus to scrape each server's port `metrics`.

## Contributing

Cobranch follows the organization's house rules (<https://docs.dev.kfirs.com/CONTRIBUTING.md>) and its own [`CLAUDE.md`](CLAUDE.md): a Linear issue for every change, pull requests through the merge queue, and a design document for every meaningful unit of work.
