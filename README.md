# DevHub

DevHub is an all-in-one, opinionated development platform for individuals and teams, with AI central to the development process from every angle. It hosts the code, runs the jobs, delivers the result and holds the conversation around each change, so a developer or a team needs nothing else to ship software.

It is a product, not an installation: anyone installs it on their own Kubernetes cluster with one Helm chart and gets a working hub, with sign-in, organizations, Git hosting, CI, GitOps delivery, APIs and SDKs.

DevHub is a Kfirs project, created by Arik Kfir, and is released under the [MIT License](LICENSE).

## Status

Foundations. The Helm chart installs DevHub's skeleton, whose servers answer only their version ([ENG-195](https://linear.app/arikkfir/issue/ENG-195)), pipeline `main` publishes the images of `main`'s newest commit, and each GitHub release publishes the chart at its version ([ENG-207](https://linear.app/arikkfir/issue/ENG-207)). The work is tracked in Linear's [DevHub project](https://linear.app/arikkfir/project/devhub-56ee907dd1af), and the designs are cross-linked pages that start from the [product design](https://claude.ai/artifact/BsCFv8jQ7XunQP2T64Sjoy); [`docs/devhub/designs/`](docs/devhub/designs/) holds snapshots of them, published to the hub's docs site under `devhub/designs/`, behind its sign-in.

## Layout

One repository holds every part of DevHub, laid out as the [technical architecture](docs/devhub/designs/architecture.md) agreed. Paths marked _planned_ arrive with the slices that need them.

| Path | What it holds |
| --- | --- |
| `cmd/devhub/` | The one binary: a subcommand per workload (the API servers `auth-server`, `rest-server`, `graphql-server` and `admin-server`, the `migrate` Job, Git server, push worker, controller, maintenance, agent), so every workload runs the same build at one version; today `rest-server`, `graphql-server`, `migrate` and `version` |
| `internal/` | The Go packages: `services/` (the domain), `adapters/` (transports, storage, outside systems) and `system/` (configuration, telemetry, build information) |
| `web/console/` | The Console, which Caddy serves as static files (image `devhub-console`); a placeholder page until its first screen |
| `web/back-office/`, `web/ui/` | _Planned_: the Back-office and the components the web apps share |
| `api/` | The API contracts, from which `go generate ./...` writes the servers' stubs: REST (`openapi.yaml`), GraphQL (`graphql/`) and, with the Back-office, the admin API (`admin.yaml`) |
| `db/migrations/` | PostgreSQL's migrations, SQL and Go, embedded in the binary; `devhub migrate` applies them before the servers roll out |
| `sdk/` | _Planned_: the Go, Python and TypeScript SDKs |
| `chart/devhub/` | The Helm chart that installs DevHub |
| `Dockerfile` | Image `devhub`: the binary, with `git` |
| `docs/devhub/` | The design documents, published to the docs site under `devhub/` |
| `.octomaton.yaml`, `.tekton/` | CI, run by [Octomaton](https://github.com/arikkfir-org/octomaton) |

## Development

Go 1.27, [golangci-lint](https://golangci-lint.run) 2.15 and a PostgreSQL 18 server for the tests that need one (each
makes its own database there; without `DEVHUB_TEST_DATABASE_URL` they are skipped):

```bash
docker run -d --name devhub-postgres -p 5432:5432 -e POSTGRES_HOST_AUTH_METHOD=trust postgres:18.6-alpine
export DEVHUB_TEST_DATABASE_URL='postgres://postgres@localhost:5432/postgres?sslmode=disable'

go generate ./...                      # the REST and GraphQL stubs, from api/; CI fails if they differ
go vet ./... && go test -race ./...    # every test
golangci-lint run ./...                # the house rules' linters (.golangci.yml)
go build -o bin/ ./cmd/...             # bin/devhub
bin/devhub version
```

The workloads read `DEVHUB_` variables. `migrate` brings a database's schema to the build's version and exits; each
server needs a bucket and the database, and turns ready once the schema is at that version:

```bash
export DEVHUB_BLOB_URL='file:///tmp/devhub-blobs?create_dir=true' DEVHUB_DATABASE_URL="$DEVHUB_TEST_DATABASE_URL"
bin/devhub migrate
bin/devhub rest-server                                     # :8080: /healthz, /readyz, /v1/version
DEVHUB_HTTP_ADDR=:8081 bin/devhub graphql-server           # :8081: /healthz, /readyz, GraphQL at /
```

They log text here and JSON in a pod (`DEVHUB_LOG_FORMAT` picks one, `DEVHUB_LOG_LEVEL` the level), and send traces
wherever OpenTelemetry's own variables point, such as `OTEL_EXPORTER_OTLP_ENDPOINT=http://localhost:4318`, or nowhere
([Observability](https://docs.dev.kfirs.com/devhub/designs/architecture.md#observability)).

CI runs the same on every pull request and merge queue group (check `Continuous Integration`), beside the organization's checks `Docs`, `Linear` and the AI review, pipeline `main` publishes both images of `main`'s newest commit, and pipeline `tag` each version's images and chart. How the repository and its CI fit together: [the DevHub repository](docs/devhub/designs/repository.md).

## Installing

Install a version of the chart, which is published beside its images; its guide, [`chart/devhub/README.md`](chart/devhub/README.md),
covers the values and upgrades:

```bash
helm install devhub oci://me-west1-docker.pkg.dev/arikkfir/devhub/charts/devhub --version 1.2.3 \
  --namespace devhub --create-namespace --set blob.url='gs://my-devhub-bucket'
helm test devhub --namespace devhub
```

A version is a [GitHub release](https://github.com/arikkfir-org/devhub/releases): publishing it creates tag `v1.2.3`,
whose pipeline `tag` adds `1.2.3` to the images pipeline `main` published for the commit and publishes the chart at
`1.2.3` ([Releases](docs/devhub/designs/repository.md#releases)). Pipeline `main` publishes images `devhub` and
`devhub-console` of `main`'s newest commit to `me-west1-docker.pkg.dev/arikkfir/devhub`, tagged with the commit's first
seven characters; an older commit may have none, since a push that a newer one overtakes while it waits is never built.
To follow `main`, install the chart from a checkout with its commit's images:

```bash
helm install devhub chart/devhub --namespace devhub --create-namespace \
  --set-string images.devhub.tag=$(git rev-parse HEAD | cut -c1-7) \
  --set-string images.console.tag=$(git rev-parse HEAD | cut -c1-7) --set blob.url='gs://my-devhub-bucket'
```

For a commit of your own, build both images, push them where your cluster pulls from, and install with those
repositories and tag `dev`:

```bash
docker build --build-arg VERSION=dev -t registry.example.com/devhub:dev .
docker build -t registry.example.com/devhub-console:dev web/console
docker push registry.example.com/devhub:dev && docker push registry.example.com/devhub-console:dev
```

Every workload serves as if it were alone, so you choose where each one lives: a host each, or paths on one host
(`https://devhub.example.com/api`), the prefix stripped. Route them from your Gateway, or set `urls.console`,
`urls.rest` and `urls.graphql` with `httpRoute.enabled` for an HTTPRoute each. Logs go to stdout as JSON; to see
traces, set `telemetry.traces.endpoint` to your OpenTelemetry Collector's or a vendor's OTLP endpoint.

## Contributing

DevHub follows the organization's house rules (<https://docs.dev.kfirs.com/CONTRIBUTING.md>) and its own [`CLAUDE.md`](CLAUDE.md): a Linear issue for every change, pull requests through the merge queue, and a design document for every meaningful unit of work.
