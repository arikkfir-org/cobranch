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

Pipeline `ci` runs two tasks side by side, each in a pod of its own, and reports check `Continuous Integration`, which
fails when either does. Task `ci` tests the Go module. `generated` fails when `go generate` changes the checkout, so the
REST and GraphQL stubs match `api/`. `lint` runs golangci-lint's standard linters, the house rules' (errcheck,
wrapcheck, contextcheck, sloglint, spancheck, depguard, funlen, gocognit, revive with `use-any`) and gofmt. `test` runs
against the PostgreSQL sidecar, and with `CI` set a test fails rather than skip when it can't reach it. Go Cloud's
drivers bring the AWS, Azure and Google Cloud SDKs: on a cold cache lint peaks near 1.5Gi, hence the task's size.

**Task ci runs seven steps in one pod, beside a PostgreSQL sidecar**

![Task ci runs seven steps in one pod, beside a PostgreSQL sidecar](repository/ci.svg)

Every image is pinned to a version, and the steps set `GOMAXPROCS=4`, so their memory doesn’t grow with the node’s CPUs.
The clone is anonymous: the repository is public, so the run holds no token.

Task `chart` installs the [chart](https://claude.ai/artifact/LyMHXgSoNQNgHtHNfEjvA8) as an operator would, on a
throwaway kind cluster. A privileged Docker daemon runs beside its steps and listens only on a socket they share. `lint`
lints the chart and validates what it renders, both with a host per workload and with paths on one host. `images` builds
`devhub` and `devhub-console` in the daemon, tagged with the commit, and `cluster` creates the cluster there and loads
them, with the bundled PostgreSQL's image, so its pods never pull. `install` waits for the migrate Job and every pod,
and `test` runs the chart's tests: both servers answer with the build's version, and the Console serves its page and the
REST API's URL. `upgrade` rolls every pod, and fails if the generated database password changed or its migrate Job
applied anything; `retest` tests again. Both tasks clone the commit with one StepAction.

**Task chart builds the images and installs the chart on a throwaway kind cluster, in a Docker daemon beside its steps**

![Task chart builds the images and installs the chart on a throwaway kind cluster, in a Docker daemon beside its steps](repository/chart.svg)

Every image is pinned to a version. The cluster’s pods count against the daemon’s resources, not the steps’, and nothing
outlives the run: the clone is anonymous, and the task holds no token and no cloud role.

Pipeline `main` runs on pushes to `main`. It builds both images in a Docker daemon, as task `chart` does, and pushes
them to DevHub's public repository on Artifact Registry, tagged with the commit's first seven characters, which the
binary reports as its version too. An installation that follows `main` runs the images of the commit it is at, so only
the newest waiting push is built: one that a newer push overtakes while it waits publishes nothing, and `main`'s newest
commit always gets its images.

**Pipeline main publishes both images of main’s newest commit, tagged with its short SHA**

![Pipeline main publishes both images of main’s newest commit, tagged with its short SHA](repository/main.svg)

The repository, its address and its grants are infra’s ([Artifact
Registry](../../infra/terraform/gcp.md#artifact-registry)); ServiceAccount `ci-devhub-release` and the branches it may
run from are delivery’s ([CI tenants](../../delivery/platform.md#ci-tenants)).

## Releases

A version is a [GitHub release](https://github.com/arikkfir-org/devhub/releases), as Octomaton's are: tag `v1.2.3` on a
commit of `main`, title `1.2.3`, GitHub's generated notes and a link to the chart's guide at the tag,
`https://github.com/arikkfir-org/devhub/blob/v1.2.3/chart/devhub/README.md`. Publishing the release creates the tag,
which runs pipeline `tag`; a draft has no tag and does nothing. `tag` refuses a tag that isn't `vX.Y.Z`, a commit that
isn't on `main` and one whose chart doesn't default to the images published here, waits up to 15 minutes for `main`'s
images of the commit, adds `1.2.3` to both, and only then packages the commit's chart at `1.2.3`, its app version the
same, and pushes it beside them as `charts/devhub`. So one version names the chart and every image it runs, and an
operator installs it with nothing but a bucket:

```bash
helm install devhub oci://me-west1-docker.pkg.dev/arikkfir/devhub/charts/devhub --version 1.2.3 \
  --namespace devhub --create-namespace --set blob.url='gs://my-devhub-bucket'
```

**A version retags main’s images of a commit and publishes the chart at that version**

![A version retags main’s images of a commit and publishes the chart at that version](repository/tag.svg)

The tag’s rights are delivery’s: `ci-devhub-release` admits `main` and `v*` tags ([CI
tenants](../../delivery/platform.md#ci-tenants)). The repository keeps every tagged version ([Artifact
Registry](../../infra/terraform/gcp.md#artifact-registry)).

The images are `main`'s, retagged, so the binary reports the commit's short SHA, and `helm test` checks that both
servers answer with the same version rather than with the chart's. A tag runs `main`'s `.octomaton.yaml` and `.tekton/`,
and its steps run `main`'s scripts against the tagged commit's chart, so no tag can carry a pipeline without the check.

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
| The chart installed on kind in every run | Each pull request proves the install and the upgrade end to end, on a cluster nobody else uses, and a failure shows the pods' events and logs | A namespace on a shared cluster, which would need its credentials and outlive a failed run |
| A version retags `main`'s images of the commit | A version is the very image `main` published, with no second build to diverge, in seconds | Rebuilding at the tag, so the binary reports the version |
| The chart beside the images, in repository `devhub`, as OCI | One public repository, one grant and one cleanup for all of DevHub's releases; Helm installs OCI charts directly | GitHub's container registry; a chart repository's `index.yaml` in a bucket |
| One version, `1.2.3`, for the chart, its app version and the image tags; `v1.2.3` for the Git tag | A chart's version is SemVer without the `v`, and the chart's images default to its app version, so the images carry the same string | Octomaton's `v1.2.3` image tags, a second spelling beside the chart's |
| `tag` runs `main`'s scripts on the tagged commit's chart | A tag can name any commit; only `main`'s files are reviewed | Running the tagged commit's checkout |
| No persistent Go cache yet | A cold run takes about eight minutes since Go Cloud's SDKs came in, which the foundations' few pull requests can afford; a cache needs a bucket and an identity of its own | Octomaton's bucket-backed caches now, worth it once pull requests come often |

## Security and failure modes

- Any branch's pull request runs its own `.octomaton.yaml` and `.tekton/` as the tenant's ServiceAccount `pipeline`,
  which holds no Google Cloud role, and with no GitHub token. Octomaton ignores forks, so a stranger's pull request runs
  nothing.
- Task `chart`'s Docker daemon runs privileged, which `ci-devhub` admits; it listens only on a socket its steps share,
  and its run holds no token either.
- Without CI tenant `ci-devhub`, Octomaton refuses every pipeline on the repository, and its pull requests can't pass
  the required checks; without infra's docs grants, check `Docs` and `docs-publish` fail for want of their roles.
- A missing check blocks the merge queue and never merges unchecked; the admin bypass remains the owner's.
- Anyone with write access can push a `v*` tag, which releases, but only a commit of `main`: `tag` refuses any other,
  and `ci-devhub-release` refuses any ref but `main` and `v*`.
- `main` builds only its newest waiting push, so a tag on a commit it skipped finds no images and fails after 15
  minutes: release `main`'s newest commit, or one it built. A tag on a commit whose build failed fails the same way;
  re-run `main`, then `tag`.
- A commit from before releases is refused at once: its chart's images default to Docker Hub's names, so its version
  would install images nobody tagged.
- A tag moved to another commit moves the version's image tags and overwrites its chart. Don't; cut the next version.

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
| CI for the rest | Decided | The web apps, the chart and the SDKs add steps to `ci` or join its steps, decided on 2026-10-27: `lint` runs golangci-lint and the web linters, `test` both suites. Installing the [chart](https://claude.ai/artifact/LyMHXgSoNQNgHtHNfEjvA8) on a throwaway cluster is task `chart`, of its own since its daemon runs privileged |

DevHub is a Kfirs project, created by Arik Kfir.
