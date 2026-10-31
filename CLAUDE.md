# Cobranch: rules for agents

## What Cobranch is

- Cobranch is a product: an all-in-one, opinionated development platform, AI-first, that anyone installs on their own
  Kubernetes cluster with one Helm chart. It ships identity, organizations, Git hosting, CI, delivery, review, issues
  and conversation as one product with one model and one API.
- Cobranch has two faces: cobranch.dev, the open-source product this repository is, which anyone downloads and runs as
  their own installation, and cobranch.app, our own installation of it, sold as a paid self-serve service like
  github.com or gitlab.com. Nothing of the product assumes cobranch.app, as nothing assumes the hub; running
  cobranch.app is work of its own.
- Cobranch is not the Kfirs hub. The hub (`infra`, `delivery`, `octomaton`, `tooling`) is one installation assembled by
  hand from GKE, Argo CD, Tekton, Keycloak and Octomaton: Cobranch's precursor, and later its first installation.
  Describe Cobranch as the product; name the hub only as context or as a migration target. Nothing of the product (its
  code, chart, defaults, examples or docs about installing it) assumes the hub, Google Cloud or any other cloud. Only
  this repository's own CI, its releases and its docs publishing run on the hub: the images and the chart are published
  to a public Artifact Registry repository, which the chart's image defaults name and every installation pulls from as
  anyone, wherever it runs.
- Cobranch is a Kfirs project, created by Arik Kfir. The credit may appear anywhere (the README, the product's footer,
  the chart's metadata, a license); keep it wherever it already does.
- Kubernetes-native and cloud-agnostic: blob storage only through Go Cloud (`gocloud.dev/blob`), PostgreSQL deployed by
  the chart or supplied externally, and the domain, ingress and autoscaling left to the operator.
- The designs are cross-linked pages, diagram-first: sequence, flow, component and boundary diagrams carry each design,
  and prose keeps what a picture can't say (why, rejected options, risks). The [product
  design](https://claude.ai/artifact/BsCFv8jQ7XunQP2T64Sjoy) links every one: the technical architecture, principals and
  permissions, this repository, the Helm chart and the design system. They are the source of truth while Cobranch's
  shape settles; `docs/cobranch/designs/` holds snapshots of them, synced again once it does. A new design is drafted as
  a page beside them and linked from the product design. A change keeps the design pages it touches true, and corrects a
  snapshot it contradicts in the same pull request.

## Work

- The house rules apply (https://docs.dev.kfirs.com/CONTRIBUTING.md); this file comes on top of them and wins where
  they conflict.
- Work is tracked in Linear's Cobranch project (https://linear.app/arikkfir/project/cobranch-56ee907dd1af). Large tasks
  run as Linear projects (https://docs.dev.kfirs.com/CONTRIBUTING.md, "Projects"): suggest one to your person and create
  it once they agree; one issue per slice, however many pull requests it takes; slices preferably self-contained and
  deployable on their own; keep the project's status current, with an update at each milestone or significant change.
- Tekton files stay short and composed (https://docs.dev.kfirs.com/CONTRIBUTING.md, "Continuous integration"): a
  PipelineRun references its Pipeline (`pipelineRef`), or its inline `pipelineSpec` its Tasks (`taskRef`), by name,
  each in a file beside it (`<name>.pipeline.yaml`, `<name>.task.yaml`), steps that Tasks share are StepActions
  (`<name>.step.yaml`), and a step names its command or runs a script file of this repository rather than an inline
  script of more than a few lines. A change to a long, inlined file splits what it touches.

## Code

- Go 1.27, one module at the root, `cobranch.dev`.
- One binary, `cmd/cobranch`, with a subcommand per workload, so every workload runs the same build at one version.
  `cmd/cobranch` only coordinates startup and shutdown: signals, telemetry, configuration, wiring. Logic goes into an
  `internal/` package.
- Layers: `internal/services` hold the domain, `internal/adapters` implement its ports (transports, storage, outside
  systems) and `internal/system` the plumbing (configuration, telemetry, build information). Dependencies point inward:
  services never import adapters, and the linter's `depguard` fails the build otherwise.
- Configure with environment variables through `github.com/kelseyhightower/envconfig`; no configuration files, and
  flags only for what a one-off command acts on.
- SQL lives in `.sql` files and is generated, never written in Go: the schema in `db/migrations/` (goose), the stores'
  queries in `db/queries/` and the seeds' in `db/seeds/queries.sql`, from which sqlc (`sqlc.yaml`, run by
  `go generate ./...`) writes `internal/adapters/postgres/queries` on pgx and `db/seeds/queries` on database/sql. A
  store converts between the generated rows and the domain's types and writes no SQL of its own; the one exception is
  the schema version's read of goose's table, whose name sqlc doesn't know.
- Every table of data people manage (principals, their emails and identities, sessions, roles, grants, and each such
  table a slice adds) carries trigger `audit()` (`db/migrations/00003_audit.sql`), which writes `audit_log` with the
  principal from session variable `cobranch.principal_id`; a new table gets the trigger in its migration, with the
  columns to keep out of the log (a token's hash) as the trigger's arguments. A change a person makes to an audited
  table runs in a store transaction that names them (`transact`'s actor; the variable is local to the transaction).
  Cobranch's own changes (a migration, a seed, maintenance's expiry sweep, a session's touch) name nobody, and
  transient rows (`sign_in_flows`) aren't audited.
- `golangci-lint` runs the house rules' linters (`.golangci.yml`); fix what it reports rather than silence it.

## Tests

- Every change needs table-driven tests. Before finishing, run `go vet ./... && go test -race ./...` and
  `golangci-lint run ./...`; CI runs the same, then builds `cmd/cobranch` and runs `cobranch version`.

## Docs

- Cobranch's pages are in `docs/cobranch/`, published to the docs site under `cobranch/`: snapshots of the designs in
  `designs/`. A change keeps those it touches true, in the same pull request.
- Pages at `https://docs.dev.kfirs.com/<path>` (every repository's `docs/`) are behind the hub's sign-in. Read one with
  `curl -fsS https://storage.googleapis.com/arikkfir-docs/.layers/<repository>/<path>`, where `<repository>` is the
  path's first segment when it names a repository (`cobranch/designs/repository.md` is `cobranch`'s) and `docs`
  otherwise (`CONTRIBUTING.md`). Don't clone a repository to read its docs.
