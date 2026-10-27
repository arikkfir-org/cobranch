# The DevHub repository

> **Snapshot.** This design lives in its [repository page](https://claude.ai/artifact/7G9yg4LyjYGGJWCkXEoDVu), linked
> with DevHub's other designs; this file is generated from it, with its diagrams, as of 2026-10-27, and synced again
> once DevHub's shape settles.

**Status**: Decided 2026-10-27 · [ENG-192](https://linear.app/arikkfir/issue/ENG-192) · Lays out the [architecture’s
stack](architecture.md#stack-and-layout) · Public, MIT

DevHub lives in one repository, `arikkfir-org/devhub`: one Go module, one binary, the web apps, the Helm chart, the SDKs
and the designs side by side, so a change to DevHub is one pull request however many parts it touches.

## Context

DevHub is a product of its own, not part of the Kfirs hub ([the product](product.md#purpose)). Its source still needs a
home where its work is reviewed, built and documented, and the hub gives one to every repository of the organization:
pull requests through a merge queue, CI through Octomaton, an AI review, Linear's check and a docs site. The goal of
[ENG-192](https://linear.app/arikkfir/issue/ENG-192) is a repository that gets all of that from its first pull request,
with a layout that later slices fill in rather than reshape.

**The organization’s repositories give devhub everything a repository needs**

![The organization’s repositories give devhub everything a repository needs](repository/context.svg)

## Layout

**One repository, one module, one binary; each part arrives with the slice that needs it**

![One repository, one module, one binary; each part arrives with the slice that needs it](repository/layout.svg)

One Go module at the root, `github.com/arikkfir-org/devhub`. The [architecture](architecture.md#stack-and-layout) agreed
the planned paths; the README keeps the full table as the layout grows.

## Continuous integration

Pipeline `ci` reports check `Continuous Integration`. `generated` fails when `go generate` changes the checkout, so the
REST and GraphQL stubs match `api/`. `lint` runs golangci-lint's standard linters, the house rules' (errcheck,
wrapcheck, contextcheck, sloglint, spancheck, depguard, funlen, gocognit, revive with `use-any`) and gofmt. `test` runs
against the PostgreSQL sidecar, and with `CI` set a test fails rather than skip when it can't reach it. Go Cloud's
drivers bring the AWS, Azure and Google Cloud SDKs: on a cold cache lint peaks near 1.5Gi, hence the task's size.

**Pipeline ci runs seven steps in one pod, beside a PostgreSQL sidecar**

![Pipeline ci runs seven steps in one pod, beside a PostgreSQL sidecar](repository/ci.svg)

Every image is pinned to a version, and the steps set `GOMAXPROCS=4`, so their memory doesn’t grow with the node’s CPUs.
The clone is anonymous: the repository is public, so the run holds no token.

> **Coming with [ENG-195](https://linear.app/arikkfir/issue/ENG-195)'s chart.** A second task, `chart`, runs beside
> `ci`: a dockerd sidecar builds the images `devhub` and `devhub-console`, kind creates a cluster, and the
> [chart](https://claude.ai/artifact/LyMHXgSoNQNgHtHNfEjvA8) is installed, tested, upgraded and tested again. Both tasks
> share a clone StepAction.

## Checks

`Docs` composes `docs/devhub/` with the other repositories' layers and fails on a broken link (the [docs site
composition](../../hub/designs/docs-site-composition.md)). `Linear` and the AI review are tooling's: [check
Linear](../../tooling/designs/linear-check.md) and the [pull request reviewer](../../tooling/designs/pr-reviewer.md).
The ruleset is infra's [root github](../../infra/terraform/github.md).

**Three required checks admit a pull request to the merge queue**

![Three required checks admit a pull request to the merge queue](repository/checks.svg)

A missing check blocks the merge queue and never merges unchecked; the admin bypass remains the owner’s.

## Decisions

| Decision | Why | Rejected |
| --- | --- | --- |
| One repository for every part of DevHub | A change across the API, the web apps, the chart and the docs is one reviewed pull request, tested and released at one version | A repository per component, whose versions must be matched |
| One Go module at the root, `github.com/arikkfir-org/devhub` | One `go.mod` and one version of each dependency; no domain is chosen yet for a vanity path | A module per workload; a vanity path before a domain exists |
| One binary, a subcommand per workload | Every workload runs the same build at one version, and the chart picks the subcommand | A binary per workload |
| Created by hand, then imported by `infra` | The owner created it with its license and pushed the first designs to `main`; the import gives it the organization's settings and ruleset, and every later change comes through a pull request | Waiting for `infra` to create it |
| Public, under the MIT License | DevHub is a product anyone may install, and its source is open with it | Internal until a license was chosen |
| An anonymous clone | A public repository needs no token, so the run holds none | Octomaton's installation token |
| golangci-lint from the first commit | The house rules' linters hold from the first line, instead of a cleanup later | Adding them later |
| No persistent Go cache yet | A cold run takes about eight minutes since Go Cloud's SDKs came in, which the foundations' few pull requests can afford; a cache needs a bucket and an identity of its own | Octomaton's bucket-backed caches now, worth it once pull requests come often |

## Security and failure modes

- Any branch's pull request runs its own `.octomaton.yaml` and `.tekton/` as the tenant's ServiceAccount `pipeline`,
  which holds no Google Cloud role, and with no GitHub token. Octomaton ignores forks, so a stranger's pull request runs
  nothing.
- Without CI tenant `ci-devhub`, Octomaton refuses every pipeline on the repository, and its pull requests can't pass
  the required checks; without infra's docs grants, check `Docs` and `docs-publish` fail for want of their roles.
- A missing check blocks the merge queue and never merges unchecked; the admin bypass remains the owner's.

## Rollout

**Rolled out in three steps, each repository doing its part**

![Rolled out in three steps, each repository doing its part](repository/rollout.svg)

Both are merged: before `ci-devhub` existed, Octomaton refused every pipeline on the repository, and before infra’s
grants applied, check Docs failed for want of its roles.

[arikkfir-org/infra#90](https://github.com/arikkfir-org/infra/pull/90) and
[arikkfir-org/delivery#86](https://github.com/arikkfir-org/delivery/pull/86) carry steps 2 and 3. The next push to
`main` after them publishes `docs/devhub/`.

## Open questions

| Question | Status | Where it stands |
| --- | --- | --- |
| Module path | Decided | The GitHub path for now, decided on 2026-10-27; a vanity path, as Octomaton's `octomaton.dev`, waits for a domain |
| CI for the rest | Decided | The web apps, the chart and the SDKs add steps to `ci` or join its steps, decided on 2026-10-27: `lint` runs golangci-lint and the web linters, `test` both suites. Installing the [chart](https://claude.ai/artifact/LyMHXgSoNQNgHtHNfEjvA8) on a throwaway cluster is a task of its own, since its dockerd runs privileged |

DevHub is a Kfirs project, created by Arik Kfir.
