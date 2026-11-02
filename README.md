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

Foundations. The Helm chart installs Cobranch's skeleton, whose servers answer only their version ([ENG-195](https://linear.app/arikkfir/issue/ENG-195)), pipeline `main` publishes the images of `main`'s newest commit, and each GitHub release publishes the chart at its version ([ENG-207](https://linear.app/arikkfir/issue/ENG-207)). Sign-in through any OpenID Connect provider, sessions, organizations with their members, invitations and join requests, their roles, built in and custom, with the grants of them, and their API keys, which scripts send as bearer tokens, are in ([ENG-197](https://linear.app/arikkfir/issue/ENG-197), [the design](docs/cobranch/designs/sign-in-and-organizations.md)). Git hosting has begun: an organization's repositories, with the grants of their roles, and personal tokens, which act as their user narrowed to the permissions they list, through the APIs and the Console; and the Git server, which clones, fetches and takes pushes over HTTPS with a personal token, recording each push before it answers, with the push worker, which then maps each push's commits to their authors; and a repository's code through the APIs and the Console: its branches, a branch's first-parent history and its commits, with the users behind them and the pushes that brought them. Pack compaction and Git over SSH, with the SSH keys users add through the APIs and the Console, are in too ([ENG-198](https://linear.app/arikkfir/issue/ENG-198), [the design](docs/cobranch/designs/git-hosting.md)). Browsing code is in: a repository's directories and files at any branch, tag or commit, with Markdown rendered as GitHub renders it and each directory's README beneath its listing, and its history through every parent from any of them, with a graph of its lines in the Console, through the APIs and the Console, which the REST and GraphQL servers read from the Git server over an internal call that carries the viewer's own token, so the Git server itself checks who reads ([ENG-228](https://linear.app/arikkfir/issue/ENG-228), [the design](docs/cobranch/designs/code-browsing.md)). Branch status is in: principals set annotations on commits through the APIs, such as a CI system's verdict, and the Console shows them on a commit's page and in a branch's history; `main`'s `/cobranch.yaml` names the annotations a merge needs and whether history stays linear, and every other branch shows its status as a request to merge into `main`, Processing, Blocked, Empty, Merged or Ready, with the checks behind it, through the APIs and the Console; a push into a branch with merge requirements brings only Ready branches' commits, unless its pusher holds `repo.bypass`, and a branch a push brings into `main` turns Merged, naming who merged it; a principal holding `repo.merge` also asks Cobranch to merge a Ready branch, by a merge commit or a fast-forward, through the APIs or the Console's Merge button, and one holding `repo.bypass` too merges any branch ([ENG-199](https://linear.app/arikkfir/issue/ENG-199), [the design](docs/cobranch/designs/branch-status.md)). Operators' metrics are in: how long each Console page takes to show and how many Git reads it takes, how long each REST route and GraphQL field takes, what each of the Git server's throwaway repositories costs and how often its pack cache hits, exported over OpenTelemetry, pushed over OTLP or scraped in Prometheus's format ([ENG-235](https://linear.app/arikkfir/issue/ENG-235), [the design](docs/cobranch/designs/monitoring.md)). Issues have begun: an organization's members create, assign and move issues through six states, Backlog to Canceled, each named by the organization's issue key and a number, such as `ACME-42`, with a priority, an assignee, the repository it is about and its activity, through the APIs and the Console; Owners also delete them, and change the key; branch links, labels and agents follow ([ENG-217](https://linear.app/arikkfir/issue/ENG-217), [the design](docs/cobranch/designs/issues.md)). The work is tracked in Linear's [Cobranch project](https://linear.app/arikkfir/project/cobranch-56ee907dd1af), and the designs are cross-linked pages that start from the [product design](https://claude.ai/artifact/BsCFv8jQ7XunQP2T64Sjoy); [`docs/cobranch/designs/`](docs/cobranch/designs/) holds snapshots of them.

## Layout

One repository holds every part of Cobranch, laid out as the [technical architecture](docs/cobranch/designs/architecture.md) agreed. Paths marked _planned_ arrive with the slices that need them.

| Path | What it holds |
| --- | --- |
| `cmd/cobranch/` | The one binary: a subcommand per workload (the API servers `auth-server`, `rest-server`, `graphql-server` and `admin-server`, the `migrate` Job, Git server, push worker, controller, `maintenance`, agent), so every workload runs the same build at one version; today `auth-server`, `rest-server`, `graphql-server`, `admin-server`, `migrate`, `git-server`, `push-worker` (with `git-hook`, the Git server's pre-receive hook), `maintenance` and `version` |
| `internal/` | The Go packages: `services/` (the domain: `auth`, sign-in, sessions, personal tokens, SSH keys and the authorizer; `orgs`, organizations, members, invitations, join requests, roles, grants and API keys; `repos`, repositories and the grants on them; `issues`, an organization's issues and their activity; `git`, what a fetch serves and the record of pushes; `code`, a repository's branches, history, commits, directories and files as people browse them, the annotations principals set on its commits, and each branch's status with the merge requirements behind it; `admin`, the installation's administration), `adapters/` (transports, storage, outside systems: `authserver`, `oidc`, `postgres`, `rest`, `graphql`, `adminapi`, `server`, `blob`, `gitserver`, `gitinternal`, `markdown`, `pushworker`) and `system/` (configuration, telemetry, build information) |
| `web/console/` | The Console, which Caddy serves as static files (image `cobranch-console`): sign-in, the welcome page (create, join, invitations), an organization's home, issues, members, roles, API keys and settings under its slug, its repositories' pages under theirs, with their files at any branch, tag or commit, Markdown and READMEs rendered, branches with their status and a Merge button, history through every parent with its graph, and commits with their annotations, and the viewer's settings with their personal tokens and SSH keys, routed in the browser |
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
makes its own database there; without `COBRANCH_TEST_DATABASE_URL` they are skipped):

```bash
docker run -d --name cobranch-postgres -p 5432:5432 -e POSTGRES_HOST_AUTH_METHOD=trust postgres:18.6-alpine
export COBRANCH_TEST_DATABASE_URL='postgres://postgres@localhost:5432/postgres?sslmode=disable'

go generate ./...                      # the REST and GraphQL stubs from api/, the queries from db/; CI fails if they differ
go vet ./... && go test -race ./...    # every test
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
export COBRANCH_SECRETS_KEY="$(openssl rand -hex 24)"    # keep it: it seals the providers' secrets the Admin server adds
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
