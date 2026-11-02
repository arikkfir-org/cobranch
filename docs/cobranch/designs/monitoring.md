# Cobranch monitoring

> **Snapshot.** This design lives in its [monitoring page](https://claude.ai/artifact/1merS4FjzGF2DNSwr4uSUU), linked
> with Cobranch's other designs; this file is generated from it, with its diagrams, as of 2026-11-02, and synced again
> once Cobranch's shape settles.

**Status**: Proposed 2026-11-02 · [ENG-235](https://linear.app/arikkfir/issue/ENG-235) · Measures [code browsing’s
risks](code-browsing.md#risks) · Adds `telemetry.metrics` to the [Helm
chart](https://claude.ai/artifact/LyMHXgSoNQNgHtHNfEjvA8) · Built by
[ENG-235](https://linear.app/arikkfir/issue/ENG-235), in one pull request

Cobranch measures what its pages cost: how long each Console page takes to show and how many throwaway repositories it
builds, how long each REST route and GraphQL field takes, what each throwaway repository costs in time and disk, and how
often the Git server’s pack cache hits. Every workload exports these metrics through OpenTelemetry to whatever the
operator runs, pushed over OTLP or scraped from Prometheus’s format, and an operator who sets nothing sees no change.

## Scope

What Cobranch measures about its own speed and cost, and how an operator receives it
([ENG-235](https://linear.app/arikkfir/issue/ENG-235)). [Code browsing](code-browsing.md#risks) builds a throwaway
repository for every read and leans on the Git server's pack cache; its follow-ups, a cache per snapshot and a
commit-graph per snapshot, wait for numbers. This design gives every installation those numbers: how long each Console
page takes to show, how long each REST route and GraphQL field takes to answer, what each throwaway repository costs,
and how often the pack cache hits.

**The browser times pages; the servers time routes, fields and throwaway repositories; the SDK exports them all**

![The browser times pages; the servers time routes, fields and throwaway repositories; the SDK exports them all](monitoring/where.svg)

The REST and GraphQL servers read code through the Git server’s internal call, and each of their answers names those
calls in `Server-Timing`. The Auth and Admin servers time their routes the same way.

- **Metrics through OpenTelemetry, as traces already go.** Every workload gets a meter provider beside its tracer
  provider, configured by OpenTelemetry's own variables: `OTEL_METRICS_EXPORTER` picks OTLP, which pushes to the
  operator's collector or vendor, or Prometheus, which serves `/metrics` for the operator's scraper. Nothing in Cobranch
  names a backend.
- **The browser measures pages; the servers measure the rest.** Only the browser knows when a page has shown, so the
  Console times each page view and reports it to the REST server, which records it like any other measurement.
- **Few attributes, all bounded.** Each attribute is something an operator filters by and takes one of a fixed set of
  values: a page from the Console's list, a route from the API's, a Git operation. Nothing names a user, an
  organization, a repository or a path.

Choices beyond the issue are collected under [Decisions](#decisions).

## The metrics

| Metric | Kind, unit | Attributes | Recorded by | Answers |
| --- | --- | --- | --- | --- |
| `cobranch.console.page.duration` | Histogram, s | `cobranch.console.page`, `cobranch.console.navigation` | REST server, from the Console's reports | How long each Console page takes to show |
| `cobranch.console.page.git.reads` | Histogram, `{read}` | `cobranch.console.page`, `cobranch.console.navigation` | REST server, from the Console's reports | How many throwaway repositories one view of a page builds |
| `http.server.request.duration` | Histogram, s | `http.request.method`, `http.route`, `http.response.status_code` | Every HTTP server (OpenTelemetry's semantic conventions) | How long each REST route, and every other server's routes, take to answer, and how often they fail |
| `cobranch.graphql.field.duration` | Histogram, s | `graphql.operation.type`, `cobranch.graphql.field` | GraphQL server | How long each root field of a query or mutation takes, with everything beneath it |
| `cobranch.git.workspace.duration` | Histogram, s | `cobranch.git.operation` | Git server | How long a throwaway repository takes to build: the snapshot, its layout and its packs |
| `cobranch.git.workspace.size` | Histogram, By | `cobranch.git.operation` | Git server | How much disk a throwaway repository takes beyond the pack cache |
| `cobranch.git.pack_cache.lookups` | Counter, `{lookup}` | `cobranch.git.pack_cache.result` | Git server | How often the pack cache holds the pack a repository links: its hit rate |
| `cobranch.git.pack_cache.usage` | Gauge, By | | Git server | How full each pod's pack cache is |
| `cobranch.git.pack_cache.limit` | Gauge, By | | Git server | What the cache keeps before it evicts (`gitServer.cacheSize`) |

Every metric carries the workload's resource, as traces do: `service.name` the workload, `service.namespace`,
`service.version` and whatever `OTEL_RESOURCE_ATTRIBUTES` adds, such as `deployment.environment.name`.

| Attribute | Values |
| --- | --- |
| `cobranch.console.page` | `sign-in`, `welcome`, `settings`, `organization`, `organization.members`, `organization.roles`, `organization.api-keys`, `organization.settings`, `organization.new-repository`, `repository.code`, `repository.file`, `repository.commits`, `repository.commit`, `repository.branches`, `repository.branch`, `repository.access`, `repository.settings` |
| `cobranch.console.navigation` | `load`, the browser loading the Console, from its navigation's start; `route`, a move inside it, from the click or Back |
| `http.route` | The route's pattern, such as `/v1/orgs/{org}/repos/{repo}/tree`; `/` for a path no route serves |
| `graphql.operation.type` | `query` or `mutation`; a subscription's stream isn't timed |
| `cobranch.graphql.field` | A root field of `Query` or `Mutation`, as the schema names it |
| `cobranch.git.pack_cache.result` | `hit`, the cache held the pack; `miss`, it downloaded the pack or waited on another request's download of it |
| `cobranch.git.operation` | `tree`, `blob` and `raw`, the internal call's reads, and `merge`, its merges; `info-refs`, the refs an HTTPS client asks for first; `upload-pack`, a clone's or a fetch's pack, and `receive-pack`, a push, over HTTPS or SSH |

## A page view

**A page view: timed in the browser, its Git reads counted from the answers, reported in a batch**

![A page view: timed in the browser, its Git reads counted from the answers, reported in a batch](monitoring/pageview.svg)

The report goes with `keepalive`, so the last batch reaches the server after the tab closes. A page that redirects
counts as the page it lands on, from the first wait’s start.

- **From the wait's start to the page drawn.** A first load counts from the browser's navigation start, so it includes
  the Console's own files, `config.json` and the viewer's first calls; a move inside the Console counts from the click
  or Back. A page that redirects, as the home page does to the viewer's organization, counts as the page it lands on,
  from the first wait's start. A page that fails to draw reports nothing.
- **Reads come back on each answer.** Every server answers a `Server-Timing` header with one `git` entry per internal
  call its request made, and the call's time: `Server-Timing: git;dur=12.4, git;dur=8.1`. The Console counts them on its
  own API calls per view, and the browser's developer tools show them beside the request. An image a README shows loads
  through `<img>`, whose answer the Console can't read, so its read isn't counted.
- **Reports go in batches.** The Console keeps the views it measured and sends them, at most 20 at once, every 30
  seconds and when the tab is hidden or closed, as `POST /v1/page-views`, with `keepalive` so the last batch survives
  the page's end, and without the session's cookie. The REST server answers `204` and records each view.
- **Anyone may report, within bounds.** The sign-in page is measured before anyone signs in, so the route takes no
  credential. It records only pages from the fixed list, at most 20 views a request and 10 minutes a view, and refuses
  anything else with `400`; the worst a false report does is skew a number, never add an attribute value.

## A throwaway repository

**A throwaway repository’s time runs to its last pack linked; its disk is counted as it goes**

![A throwaway repository’s time runs to its last pack linked; its disk is counted as it goes](monitoring/workspace.svg)

A pack the cache still holds has a second link, so it costs the repository nothing; one evicted while the repository
linked it, a push’s new pack and the layout’s files are counted.

- **Its time** runs from the snapshot's read to the last pack linked: one statement for the refs and the pack list, the
  layout's few files, and a hard link per pack, with a download from storage for each pack the cache lacks. It is
  measured for every throwaway repository, the internal call's, a fetch's, a push's and a merge's alike.
- **Its disk** is measured when it is removed: the bytes of every file in it that nothing else links. A pack the cache
  still holds is shared and costs nothing more; one the cache evicted while the repository linked it, a push's new pack
  and the layout's files are the repository's own.
- **The cache's hit rate** counts each pack a repository links: a hit when the cache holds it, a miss when it downloads
  it, or waits on another request's download of it. Its usage and limit come from each Git server pod.

## Exporting

**Where metrics go: OpenTelemetry’s variables decide; set nothing and nothing leaves**

![Where metrics go: OpenTelemetry’s variables decide; set nothing and nothing leaves](monitoring/export.svg)

Every other OTLP setting is OpenTelemetry’s own: endpoint, headers, certificate, timeout, interval and temporality.

| Variable | Effect |
| --- | --- |
| `OTEL_METRICS_EXPORTER` | `otlp`: push over OTLP/HTTP. `prometheus`: serve `/metrics` for a scraper. `none`: nothing. Unset: `otlp` when an OTLP endpoint is set, else nothing, as traces do |
| `OTEL_EXPORTER_OTLP_ENDPOINT`, `OTEL_EXPORTER_OTLP_METRICS_ENDPOINT` | Where OTLP goes: the collector's or vendor's address, shared with traces, or the metrics' own URL |
| `OTEL_EXPORTER_OTLP_HEADERS`, `…_METRICS_HEADERS` | A vendor's API key, say |
| `OTEL_METRIC_EXPORT_INTERVAL` | How often OTLP pushes, in milliseconds: 60000 by default |
| `OTEL_EXPORTER_PROMETHEUS_HOST`, `OTEL_EXPORTER_PROMETHEUS_PORT` | Where `/metrics` listens: `localhost:9464` by default, which no scraper reaches; the chart sets `0.0.0.0` |
| `OTEL_SDK_DISABLED` | `true` turns metrics off with traces |

- **The Helm chart** carries them as `telemetry.metrics`: `exporter` (empty, `otlp` or `prometheus`), `endpoint`, the
  metrics' own OTLP URL when it isn't traces', and `port`, which every workload with metrics then declares as container
  port `metrics`, for a scraper's pod selector. The operator's scrape configuration, its network policy and its
  dashboards stay the operator's.
- **Temporality stays the SDK's default,** cumulative, which Prometheus and most backends read;
  `OTEL_EXPORTER_OTLP_METRICS_TEMPORALITY_PREFERENCE` changes it for one that wants deltas.
- **Histogram buckets** follow OpenTelemetry's HTTP conventions for durations, 5 ms to 10 s, with 20, 30 and 60 s more
  for page views; powers of four for sizes, 4 KiB to 1 GiB; and 0 to 32 for reads.

## Using the numbers

| Question | From |
| --- | --- |
| Which pages are slow? | `cobranch.console.page.duration`'s 95th percentile by page, `route` views apart from `load`s |
| Is a slow page slow on the server? | `http.server.request.duration` by route, against the page's time |
| Is a cache per snapshot worth building? | `cobranch.console.page.git.reads` above one per view on code pages, times `cobranch.git.workspace.duration` for `tree`, `blob` and `raw` |
| Is a commit-graph per snapshot worth building? | `http.server.request.duration` of `/v1/orgs/{org}/repos/{repo}/commits` growing with history pages, and `repository.commits` page times |
| Is the pack cache big enough? | `cobranch.git.pack_cache.lookups`' hit rate, with `usage` against `limit` |
| What do throwaway repositories cost? | `cobranch.git.workspace.duration` and `cobranch.git.workspace.size` by operation, and their count |

## Decisions

| Decision | Why | Rejected | Status |
| --- | --- | --- | --- |
| OpenTelemetry's SDK and variables for metrics, OTLP or Prometheus | Any backend, through the operator's collector or scraper; the variables are the ones operators already set for traces | A metrics backend of Cobranch's own; a vendor's SDK | Proposed |
| The browser times pages; the REST server records them | Only the browser knows when a page has shown, and the Console already calls the REST API | The browser exporting OTLP itself, which would make every operator expose a collector to the internet and allow it in CORS | Proposed |
| Reads per page from `Server-Timing`, counted by the Console | Exact per view, with no new request; the standard header shows in the browser's tools | Baggage from the browser to the Git server, which makes every cross-origin `GET` preflighted and trusts a browser's attribute; dividing reads by page views, which API clients skew | Proposed |
| GraphQL timed per root field | The schema bounds the fields, where operation names are the client's to choose | Operation names as attributes, unbounded | Proposed |
| A throwaway repository's disk is what nothing else links | That is what it adds to the disk; packs the cache holds are shared | Its apparent size, which counts the cache's packs again for every repository | Proposed |
| Page-view reports take no credential, within bounds | The sign-in page is measured before anyone signs in; bounded attributes keep a false report harmless | Requiring a session, which loses the sign-in page | Proposed |

## Risks

- **Reports from anyone can skew the page numbers.** They can't add attribute values or grow storage, and a skew shows
  as a jump in one page's count; a per-address limit can come if it happens.
- **A tab in the background times slowly.** Browsers throttle hidden tabs, so a view that waits behind one is long;
  views over ten minutes are dropped, and percentiles hold up against the rest.
- **The Prometheus endpoint answers anyone who reaches it.** It holds only these metrics, nothing about users, and
  listens on its own port, which the operator's network policy admits only from its scraper.

## Rollout

**One pull request in Cobranch; the rest is each operator’s**

![One pull request in Cobranch; the rest is each operator’s](monitoring/rollout.svg)

Nothing of an installation is in the product: an operator who sets nothing sees no change.

An installation that sets nothing new sees no change: without `OTEL_METRICS_EXPORTER` or an OTLP endpoint, metrics stay
in the process, as unexported spans do. Each operator then chooses where they go, and builds their dashboards and alerts
on the questions above.

Cobranch is a Kfirs project, created by Arik Kfir.
