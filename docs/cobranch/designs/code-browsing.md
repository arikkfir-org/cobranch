# Cobranch code browsing

> **Snapshot.** This design lives in its [code browsing page](https://claude.ai/artifact/8VJGfvZ8WDMyvu6iSC88qB), linked
> with Cobranch's other designs; this file is generated from it, with its diagrams, as of 2026-11-01, and synced again
> once Cobranch's shape settles.

**Status**: Agreed 2026-11-01 · [ENG-228](https://linear.app/arikkfir/issue/ENG-228) · Builds on [Git hosting’s
Console](git-hosting.md#the-console) and the [architecture’s internal calls](architecture.md#components) · Adds the Git
server’s internal port to the chart · Built by [ENG-228](https://linear.app/arikkfir/issue/ENG-228), in three pull
requests

People read a repository in the Console the way its authors wrote it: its directories and files at any branch, tag or
commit, Markdown rendered as on GitHub but unable to run anything or load anything from outside but images, a README
beneath every directory, and history through every parent. The REST and GraphQL servers ask the Git server, the only
workload that reads Git objects, over an internal call this slice builds.

## Scope

How the Console shows a repository's code ([ENG-228](https://linear.app/arikkfir/issue/ENG-228)): its directories and
files at any branch, tag or commit, Markdown rendered the way GitHub renders it, a directory's README beneath its
listing, and history through every parent. [Git hosting](git-hosting.md#the-console) gave the Console branches,
first-parent history and commits from PostgreSQL, and left the rest here, since only the Git server reads Git objects
([architecture](architecture.md#components)).

**This slice reads a repository’s files through the Git server; colouring, blame and diffs come later**

![This slice reads a repository’s files through the Git server; colouring, blame and diffs come later](code-browsing/scope.svg)

Git hosting ([ENG-198](https://linear.app/arikkfir/issue/ENG-198)) recorded every push’s commits and branches; this
slice reads the files those commits hold.

Choices beyond the agreed designs are collected under [Decisions](#decisions).

## Components

**The API servers ask the Git server over an internal call; only the Git server reads Git objects**

![The API servers ask the Git server over an internal call; only the Git server reads Git objects](code-browsing/components.svg)

The internal port has a Service of its own, which no route names; the chart gives the REST and GraphQL servers its URL
whenever it runs the Git server.

- **The internal call arrives.** The [architecture](architecture.md#components) planned it: the REST and GraphQL servers
  ask the Git server over plain HTTP with JSON, on a port of its own that no route names. This slice builds it, for
  trees, blobs and history walks; merges through the API ([ENG-199](https://linear.app/arikkfir/issue/ENG-199)) join it
  later.
- **The Git server reads; the API servers decide.** The internal call takes a repository's ID, a ref and a path, and
  answers what Git holds there. The REST and GraphQL servers check `repo.read` first, pick a directory's README, render
  Markdown and add PostgreSQL's commits and principals. The Git server checks the viewer too: its callers send on the
  viewer's own token, and it serves only what that viewer may read.
- **Each call reads one snapshot.** As a fetch does ([a fetch](git-hosting.md#a-fetch)): the refs and the pack list in
  one statement, a throwaway repository that hard-links the snapshot's packs from the pod's cache, and `git` on it. A
  ref resolves against the same snapshot whose packs hold its objects.

### The internal call

| Call | Answers |
| --- | --- |
| `GET /internal/v1/repos/{id}/tree?ref=&path=` | The commit the ref names, the tree at the path, and its entries: name, mode, kind, object and size |
| `GET /internal/v1/repos/{id}/blob?ref=&path=&limit=` | The commit, the blob at the path, its mode and size, and up to `limit` bytes of it |
| `GET /internal/v1/repos/{id}/raw?ref=&path=` | The blob's bytes, streamed, with its commit and object in headers |
| `GET /internal/v1/repos/{id}/log?ref=&skip=&limit=` | The commits reachable from the ref, every parent followed, newest first: their names only |

- **Its own port and Service.** The Git server listens on `8081` beside Git's `8080`, behind a Service of its own,
  `<release>-git-server-internal`; routes name only `<release>-git-server`, so nothing outside reaches it. The REST and
  GraphQL servers find it at the Service's URL, which the chart gives them whenever `urls.git` is set; without a Git
  server, a code read answers `503`.
- **The viewer's own token.** **Changed (owner, 2026-11-01)** from the architecture's "secret the chart generates": the
  REST and GraphQL servers send on the token their viewer's request carried (a session's, a personal token or an API
  key's secret) as a bearer token, and none for Anonymous. The Git server resolves it as every server does and checks
  `repo.read` itself, as Git's own port does, so it holds up whoever calls it: no secret is shared, and agents or an MCP
  server may later call it directly with their own tokens. A session's token works on this port, unlike on Git's, since
  only Cobranch's own workloads reach it.
- **Errors carry codes.** `401` for a credential that acts as nobody; `404` with `repo.not_found` (a repository the
  viewer can't read, as one that isn't there), `ref.not_found`, `path.not_found`, `path.directory` (a blob asked of a
  tree), `path.file` (a tree asked of a blob) or `path.submodule`; `400` for a ref or a path Cobranch refuses.

## Refs and paths

**A Console URL splits at the longest branch or tag; the Git server resolves the ref in the same snapshot**

![A Console URL splits at the longest branch or tag; the Git server resolves the ref in the same snapshot](code-browsing/refs.svg)

A ref resolves against the refs read in the same statement as the packs that hold its objects, so a branch that moves
meanwhile changes nothing of the answer.

- **A ref is a branch, a tag or a full commit name.** The Git server tries a full name first, then `refs/heads/<ref>`,
  then `refs/tags/<ref>`, against the snapshot's refs, and peels an annotated tag to its commit. An abbreviated name,
  `HEAD~2` and the like are refused: nothing but a ref or a name reaches `git`'s revision parser.
- **A path is relative to the repository's root,** as `/`-separated names: no empty name, no `.` or `..`, no NUL. The
  root is the empty path.
- **The Console's URLs look like GitHub's,** `/<org>/<repo>/tree/<ref>/<path>` and `…/blob/<ref>/<path>`, and a branch's
  name may hold `/`. The Console splits the URL by the longest branch or tag name its segments start with, or a full
  commit name, from the branches and tags it lists for its ref picker.
- **Pinned answers.** Every answer names the commit its ref resolved to, so the Console links a file's permanent URL,
  and images load at the commit.

## Trees and files

**A directory is one request: its listing from the Git server, its README rendered by the REST server**

![A directory is one request: its listing from the Git server, its README rendered by the REST server](code-browsing/read.svg)

The README’s blob comes from a second internal call, in the same request. A conditional request whose ETag still matches
answers 304 without a body.

- **A tree** lists directories first and then files, each by name, with its mode, its kind (directory, file, symlink,
  submodule), its object and a file's size. A submodule names the commit it pins; the Console shows it, and browsing
  into it waits for a later slice.
- **A file** answers its size, mode and object, whether it is binary (a NUL in its first 8000 bytes, as Git judges, or
  bytes that aren't UTF-8), and its text up to 1 MiB; a larger text says it is truncated, and the Console offers its raw
  bytes. A symlink's text is its target.
- **Raw bytes** come from the REST API's `…/raw`, which the Console's images and its Raw and Download links use: see
  [Raw files](#raw-files).
- **GraphQL** answers the same: `Repository.tags`, `tree(ref, path)` and `blob(ref, path)`, a tree's README included.

## Markdown

A file whose name ends in `.md`, `.markdown`, `.mdown`, `.mkd` or `.mkdn` opens rendered, with a switch to its source.
The REST and GraphQL servers render it, so every client gets the same HTML, and the Console only shows it.

**Markdown renders on the server and shows in a frame that runs nothing and loads only the installation’s images**

![Markdown renders on the server and shows in a frame that runs nothing and loads only the installation’s images](code-browsing/markdown.svg)

The sanitizer is the guard; the Console’s frame is a second one, which holds even if the sanitizer lets something
through.

- **GitHub-flavoured.** goldmark with GFM's tables, task lists, strikethrough and autolinks, and footnotes. Code blocks
  keep their language as a `language-…` class; colouring them waits for a later slice.
- **Raw HTML is kept, then sanitized.** An allow-list in the spirit of GitHub's: headings, paragraphs, lists, tables
  with `align`, `details` and `summary`, `img` with `width` and `height`, `kbd`, `sup`, `sub` and the like. No `script`,
  `style`, `iframe`, `object`, `form` or `input` but a task list's disabled checkbox; no event handlers, no `style`
  attributes, and only `http`, `https`, `mailto` and in-repository URLs.
- **Links resolve inside the repository.** A relative link, or one from the root (`/docs/setup.md`), points at the
  Console's page of that path at the same ref; a link to a directory lands on its tree, which the Console tells from a
  file by asking. A relative image loads from the REST API's `…/raw` at the commit. A heading gets GitHub's anchor, its
  `id` prefixed with `user-content-` so no document clobbers the Console's own ids, and `#anchor` links scroll to it.
- **Images from elsewhere load in the browser.** An image from another host, Markdown or HTML, stays at its URL, at
  https when it names a host alone, and the viewer's browser loads it as any page's, cached as that host says; Cobranch
  neither fetches nor proxies it. `data:` images of PNG, JPEG, GIF and WebP stay too. Nothing else loads from outside; a
  setting with which a user turns images from elsewhere off may come later.
- **A sandboxed frame in the Console.** The Console draws the HTML in an `iframe` with `sandbox` but no `allow-scripts`,
  so nothing in it runs, and a Content Security Policy that lets it load images alone, from any host and `data:`. The
  Console sizes the frame to its content and follows its links itself.

## READMEs

A directory's README shows rendered beneath its listing, at the repository's root and in every directory.

- **Which file.** The first, case-insensitively, of `README.md`, `README.markdown`, `README.mdown`, `README.mkd`,
  `README.mkdn`, `README.txt` and `README`; Markdown renders, the rest shows as text.
- **The tree's answer holds it:** its path, its object and its rendered HTML or text, so the Console draws a directory
  with one request.

## Raw files

`GET /v1/orgs/{org}/repos/{repo}/raw?ref=&path=` answers a file's bytes from the REST server's own origin, which holds
the session cookie, so it serves them as nothing that could run there.

| Header | Value |
| --- | --- |
| `Content-Type` | An image's type by its extension (PNG, JPEG, GIF, WebP, AVIF, BMP, ICO, SVG); text as `text/plain; charset=utf-8`; anything else as `application/octet-stream` |
| `Content-Disposition` | `attachment` for anything but an image and text, or with `download=1` |
| `Content-Security-Policy` | `default-src 'none'; img-src 'self' data:; style-src 'unsafe-inline'; sandbox` |
| `X-Content-Type-Options` | `nosniff` |
| `Cross-Origin-Resource-Policy` | `same-site`: only the installation's own pages embed it |
| `ETag` | The blob's object name: its content's hash |

An HTML file shows as text, and an SVG opened on its own runs nothing, under `sandbox`. Images in the Console's frame
carry the session cookie because the REST API and the Console share the site the cookie names
([routes](architecture.md#routes)).

## History

**Every parent comes from the Git server’s walk, the first parent from PostgreSQL; both read their commits from
PostgreSQL**

![Every parent comes from the Git server’s walk, the first parent from PostgreSQL; both read their commits from PostgreSQL](code-browsing/history.svg)

A cursor for every parent keeps the commit the first page started from, so later pages hold still while the branch moves
on.

- **Every parent, in the graph's order.** A history page lists the commits reachable from its ref, every parent
  followed, newest first, and never a commit before its children: the commit graph orders them, and dates only order
  separate lines. The Git server walks them (`git rev-list --date-order --parents --skip --max-count` on the snapshot),
  and the API servers read those commits, with their principals, from PostgreSQL in one query, leaving out any commit no
  push recorded.
- **First parent stays.** `parents=first` keeps today's walk along first parents in PostgreSQL, which needs no Git
  server: a branch's line of merges. It stays the API's default, so today's clients read what they read; the Console
  asks for every parent, and falls back to the first on an installation without a Git server.
- **A graph.** The Console draws a lane per line of history beside the list, from each commit's parents, a page at a
  time; a merge's lines join it.
- **Any ref.** History takes `ref` (a branch, a tag or a commit) beside today's `branch`, and its cursor is opaque: for
  every parent, the commit it started from and how many it has listed, so pages hold still while the branch moves.

## Caching

- **ETags from the commit.** A tree's and a file's answer is fixed by the commit its ref resolved to, its path and the
  renderer's version, so its ETag is their hash. A history page's hashes the page itself, since the push worker maps a
  commit's people to users after the push. A matching `If-None-Match` answers `304` without a body. Every answer sends
  `Cache-Control: private, no-cache`, as the house rules ask.
- **Raw bytes at a commit are immutable.** **Agreed (owner, 2026-11-01):** `…/raw` asked at the full name of the commit
  it resolves to, as the Console's images always are, answers `Cache-Control: private, max-age=31536000, immutable`, its
  `304` too, so a README's images aren't asked again on every view; asked at a branch or a tag, even one named like a
  commit, it stays `no-cache`.
- **The Git server's pack cache** serves every internal call as it does fetches; nothing else is cached in this slice.

## The Console

**The Code tab opens on main’s root; every directory, file, history page and commit links the others**

![The Code tab opens on main’s root; every directory, file, history page and commit links the others](code-browsing/console.svg)

The ref picker lists branches and tags; a file links its permanent URL at the commit its ref resolved to.

- **The Code tab** opens on `main`'s root: the ref picker with branches and tags, the clone URLs behind a Clone button,
  the listing, the latest commit with a link to the history, and the README.
- **A directory** shows its breadcrumb, its listing and its README; **a file** its size, Raw and Download, and its
  content: Markdown rendered with a switch to its source, text with line numbers, an image, or for anything else its
  size and a download.
- **History** gains the graph and a switch between every parent and the first; a commit gains a link to browse its
  files.
- **Empty repositories** keep today's page: how to push the first commit.

## Configuration

Nothing for the operator: the chart wires the internal call whenever it runs the Git server.

| The chart adds | When |
| --- | --- |
| The Git server's container port `internal` (`8081`) and Service `<release>-git-server-internal` | `urls.git` is set |
| `COBRANCH_GIT_INTERNAL_URL` on the REST and GraphQL servers, that Service's URL | `urls.git` is set |
| Job `<release>-seed`, after each install and upgrade, where the migrate Job runs: a demo owner's push of a history to the demo organization's website, unless it has one | `seed.enabled`, with `urls.auth` and `urls.git` |

An operator whose NetworkPolicies select the Git server admits the REST and GraphQL servers to port `8081`.

## Tests

- **The renderer** in table-driven tests: every GFM feature, relative links and images at the root and in directories,
  anchors, images from elsewhere, and the sanitizer against script, event handlers, `javascript:` URLs, `style`, SVG,
  `iframe`, forms and DOM-clobbering ids.
- **The internal call end to end with the real `git`,** as Git hosting's tests do: push a repository with directories, a
  symlink, a submodule, binary and large files, an annotated tag and a merge, then read trees, blobs, raw bytes and
  every-parent history through the REST API, by branch, tag and commit, and the codes for missing refs and paths.
- **The chart in CI** finds the pushed file through the REST API's tree in kind.

## Rollout

**Three pull requests in Cobranch, each one usable on its own**

![Three pull requests in Cobranch, each one usable on its own](code-browsing/rollout.svg)

Nothing of an installation is in the product: the chart wires the internal call whenever it runs the Git server.

- **Each pull request keeps `main` working,** with its tests and its Console pages, and is Part of
  [ENG-228](https://linear.app/arikkfir/issue/ENG-228); the last closes it.
- **An installation adds nothing,** unless its NetworkPolicies select the Git server: then they admit the REST and
  GraphQL servers to port `8081` before the first pull request.
- **The shared token goes a release later.** The release that sends the viewer's token stops reading the token
  PostgreSQL held, and the next drops its table, so the release before keeps working through a rolling upgrade. While
  old and new servers run side by side, a code read between an old one and a new one fails until both have rolled.

## Risks

- **Every view builds a throwaway repository.** Cheap over a warm cache (hard links and `packed-refs`), and two calls
  for a directory with a README; a cache per snapshot can come if pages turn slow.
- **History by skipping.** A deep page walks every commit before it, and the graph's order reads the whole history
  before the first page; a commit-graph per snapshot can come once
  [ENG-235](https://linear.app/arikkfir/issue/ENG-235)'s measurements show a need.
- **Clock skew.** A commit shows the date its committer's clock wrote, as in Git; pushes aren't refused for it, since
  imported histories and rebases carry skew. It moves a commit only among separate lines, never above its children.
- **Raw files on the API's origin.** Guarded by their headers above; a separate origin for user content is the
  operator's choice later, if one is ever needed.
- **The sanitizer is a dependency.** bluemonday's allow-list is the guard, with the Console's frame behind it, and its
  tests run in CI.

## Decisions

| Proposal | Why | Decision |
| --- | --- | --- |
| The REST and GraphQL servers call the Git server internally for trees, blobs, raw bytes and history walks | Only the Git server reads Git objects; the API servers keep permissions, Markdown and PostgreSQL's data | Agreed (owner, 2026-11-01) |
| The internal call carries the viewer's own token, and the Git server checks `repo.read` itself, instead of a secret both ends share | Nothing to share or rotate; the Git server holds up whoever calls it, agents and an MCP server included | Changed (owner, 2026-11-01) |
| History is ordered by the commit graph (`git rev-list --date-order`); no push is refused for its dates | A commit never lists before its children, whatever the clocks said | Agreed (owner, 2026-11-01) |
| A ref is a full commit name, a branch or a tag, in that order, peeled to a commit; nothing else reaches `git` | One meaning per ref; no revision syntax from a URL | Agreed (owner, 2026-11-01) |
| The API servers render Markdown, with goldmark (GFM) and an allow-list sanitizer, and resolve links and images inside the repository | One renderer for every client, tested in Go; later issues and reviews reuse it | Agreed (owner, 2026-11-01) |
| Images from other hosts load in the viewer's browser, cached as their hosts say; nothing else loads from outside, and nothing fetches or proxies them | A README's badges and screenshots show as their authors meant; a setting that turns them off may come later | Changed (owner, 2026-11-01) |
| The Console shows rendered HTML in a sandboxed frame with its own Content Security Policy | A second guard that holds whatever the sanitizer misses, and no document reaches the Console's own page | Agreed (owner, 2026-11-01) |
| A directory's README is the first of `README.md` … `README`, rendered in the tree's answer | One request per directory | Agreed (owner, 2026-11-01) |
| History follows every parent by default in the Console, with a graph; `parents=first` keeps today's walk | A merge's second line shows; first parent stays one switch away | Agreed (owner, 2026-11-01) |
| A demo installation's Job pushes a history to the demo organization's website: directories, Markdown, an image, a symlink, merges, a tag and an open branch | A preview shows every page of this slice at once, through the push path anyone takes | Agreed (owner, 2026-11-01) |
| Raw bytes at the full name of their commit are cached as immutable; everything else is `private, no-cache` | A README's images load once; the house rules' default stays for whatever can change | Agreed (owner, 2026-11-01) |
| Syntax colouring, each entry's last commit, blame, a file's history, diffs, a setting that turns images from elsewhere off and code search come later | Keeps this slice to its issue | Agreed (owner, 2026-11-01) |

## Open questions

None: the owner answered all three on 2026-11-01 (images from elsewhere, raw bytes at a commit, the internal call's
token), as Decisions records.

Cobranch is a Kfirs project, created by Arik Kfir.
