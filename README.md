# DevHub

DevHub is an all-in-one, opinionated development platform for individuals and teams, with AI central to the development process from every angle. It hosts the code, runs the jobs, delivers the result and holds the conversation around each change, so a developer or a team needs nothing else to ship software.

It is a product, not an installation: anyone installs it on their own Kubernetes cluster with one Helm chart and gets a working hub, with sign-in, organizations, Git hosting, CI, GitOps delivery, APIs and SDKs.

DevHub is a Kfirs project, created by Arik Kfir, and is released under the [MIT License](LICENSE).

## Status

Foundations. Nothing installs yet: the first installable slice is the Helm chart's skeleton ([ENG-195](https://linear.app/arikkfir/issue/ENG-195)). The work is tracked in Linear's [DevHub project](https://linear.app/arikkfir/project/devhub-56ee907dd1af), and the designs are cross-linked pages that start from the [product design](https://claude.ai/artifact/BsCFv8jQ7XunQP2T64Sjoy); [`docs/devhub/designs/`](docs/devhub/designs/) holds snapshots of them, published to the hub's docs site under `devhub/designs/`, behind its sign-in.

## Layout

One repository holds every part of DevHub, laid out as the [technical architecture](docs/devhub/designs/architecture.md) agreed. Paths marked _planned_ arrive with the slices that need them.

| Path | What it holds |
| --- | --- |
| `cmd/devhub/` | The one binary: a subcommand per workload (the API servers `auth-server`, `rest-server`, `graphql-server` and `admin-server`, the `migrate` Job, Git server, push worker, controller, maintenance, agent), so every workload runs the same build at one version; today `rest-server`, `graphql-server`, `migrate` and `version` |
| `internal/` | The Go packages: `services/` (the domain), `adapters/` (transports, storage, outside systems) and `system/` (configuration, telemetry, build information) |
| `web/console/`, `web/back-office/`, `web/ui/` | _Planned_: the Console, the Back-office and their shared components, built to static files that Caddy serves |
| `api/` | The API contracts, from which `go generate ./...` writes the servers' stubs: REST (`openapi.yaml`), GraphQL (`graphql/`) and, with the Back-office, the admin API (`admin.yaml`) |
| `db/migrations/` | PostgreSQL's migrations, SQL and Go, embedded in the binary; `devhub migrate` applies them before the servers roll out |
| `sdk/` | _Planned_: the Go, Python and TypeScript SDKs |
| `chart/devhub/` | _Planned_: the Helm chart that installs DevHub |
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

CI runs the same on every pull request and merge queue group (check `Continuous Integration`), beside the organization's checks `Docs`, `Linear` and the AI review. How the repository and its CI fit together: [the DevHub repository](docs/devhub/designs/repository.md).

## Contributing

DevHub follows the organization's house rules (<https://docs.dev.kfirs.com/CONTRIBUTING.md>) and its own [`CLAUDE.md`](CLAUDE.md): a Linear issue for every change, pull requests through the merge queue, and a design document for every meaningful unit of work.
