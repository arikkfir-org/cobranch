# The Cobranch Console’s files

> **Snapshot.** This design lives in its [Console page](https://claude.ai/artifact/YDmfu6vfypwS1HfsVi1CSh), linked with
> Cobranch's other designs; this file is generated from it, with its diagrams, as of 2026-11-02, and synced again once
> Cobranch's shape settles.

**Status**: Proposed 2026-11-02 · [ENG-246](https://linear.app/arikkfir/issue/ENG-246) · Restructures the Console of
[the architecture](architecture.md#components), [code browsing](code-browsing.md) and every page since · Served as
before: Caddy, no build step · Built by [ENG-246](https://linear.app/arikkfir/issue/ENG-246), in one pull request

The Console was one 3,700-line index.html: its styles, router and every page in one file, which every slice that touched
a page changed, so their pull requests conflicted and fell out of the merge queue. It becomes a shell and native ES
modules, one per page, which the Console finds by their files’ names rather than through a list, each with its
stylesheet beside it. A slice that adds a page adds files; two such slices merge in either order. Nothing the Console
does changes.

## Scope

How the Console's code is laid out in files, so slices that build pages in parallel stop conflicting
([ENG-246](https://linear.app/arikkfir/issue/ENG-246)). Since 2026-11-02 01:40Z five approved, green pull requests fell
out of the merge queue on conflicts in `web/console/public/index.html`, the one file that held the Console's styles,
router and every page. This design splits that file without changing what the Console does: same routes, same pages,
same requests, same look, same page views.

**A shell, shared modules, and a module per page that the Console finds by its file’s name**

![A shell, shared modules, and a module per page that the Console finds by its file’s name](console/files.svg)

Native ES modules, as browsers load them: no bundler, no build step, and the image still copies `public/` as it is. A
new page is a new file in `pages/org/` or `pages/repo/`, with its stylesheet beside it when it has styles of its own.

| Path under `web/console/public/` | Holds |
| --- | --- |
| `index.html` | The shell: a `<div id="app">` and a bootstrap that finds the Console's root and release, imports that release's `console.js`, and loads again when it fails |
| `assets/console.js` | `start`: the configuration, the viewer, their organizations, every page's module and stylesheet, then the first route |
| `assets/console.css` | What every page shares: the design system's tokens, elements, layout and status language |
| `assets/lib/` | The router, the frames of an organization and a repository, the shared state, the API client, and the parts several pages draw (code, diffs, chats, reviews, runs, issues' badges), each with its stylesheet when it has one |
| `assets/pages/` | The pages the router names: `sign-in.js`, `welcome.js`, `settings.js` |
| `assets/pages/org/` | An organization's pages, `<part>.js` for `/acme/<part>/…`; `index.js` its home |
| `assets/pages/repo/` | A repository's pages, `<part>.js` for `/acme/website/<part>/…`; `index.js` its code |

## A load

**A load: the shell, its root and release from config.json, every page’s module and stylesheet of that release, then the
route**

![A load: the shell, its root and release from config.json, every page’s module and stylesheet of that release, then the route](console/boot.svg)

A load takes every file from one release: a server serves only its own release’s, so a load that reaches another fails
whole and starts again, instead of running two releases’ modules together. One that still fails shows “The Console
couldn’t load” instead of half a Console. Navigating within the Console loads no more modules: every page’s is already
in.

- **The root comes from `config.json`.** An operator may mount the Console at a prefix, which its route strips:
  `consoleUrl`'s path is then the root every module and link starts from, as the pages' links already did.
- **So does the release.** `config.json` names the release of the server that answered (`COBRANCH_RELEASE`, which the
  chart sets to the image's tag), and the shell imports `assets/@<release>/console.js`; every module and stylesheet
  resolves from there, so one load takes them all under that path. A server serves only its own release's: while a
  rollout runs two releases behind one Service, a request that reaches the other release's server answers 404 instead of
  a file that doesn't fit the rest. The shell then loads the Console again, after 1 s, 3 s and 9 s, each time under a
  try's path (`@<release>~1/`) that no browser cached, and after the third says the Console couldn't load. A rollout
  surges one server at a time, so a load rarely meets both releases, and one that does starts over whole.
- **Every page's module loads at the start,** not when first opened. The frames need every page's `nav` and `allowed` to
  draw the nav and the tabs, and a session then runs one release's modules throughout: a release can't land between two
  pages of one visit. The cost is about fifty small requests on a first load, over one connection, which later loads
  answer with 304s.
- **Caching: `Cache-Control: no-cache` on every file,** the shell included. A browser checks each file again before it
  uses it, so a release reaches it on its next load, every module at once. The release's path keeps a load to one
  release; it doesn't let browsers keep files longer, which would need content-hashed names and a build step to rewrite
  every import.
- **Under `/assets/`,** which `orgs.ReservedSlugs` already keeps from every organization. A file missing there answers
  404, never the shell, so a missing module fails loudly instead of loading HTML as JavaScript.

## Finding pages

**The router: a path’s parts pick its page, from the modules the directories hold**

![The router: a path’s parts pick its page, from the modules the directories hold](console/routing.svg)

A third part that names no module draws nothing new: the repository’s root, as an unknown tab always did. The Console’s
own paths are the organization slugs no organization may take.

- **No list of pages.** `lib/assets.js` asks Caddy for the files of `pages/org/` and `pages/repo/`, which
  `file_server browse` lists as JSON when asked, and imports each `.js` there; a file's name is the route's part that
  opens it. Caddy lists only the directories the Console reads, those two and `lib/` and `pages/` for their stylesheets.
  A new page is a new file, and nothing else in the Console changes.
- **Rejected: a registry module,** one import a line: two slices that add pages insert at the same place, the end, and
  conflict, the failure this design removes. **Rejected: importing `pages/org/<part>.js` on demand:** most second parts
  are repositories, each of which would cost a 404 first, and the nav must know every page before any is opened.
  **Rejected: a manifest written when the image builds:** a build step the Console doesn't have.
- **The three pages outside an organization stay named in the router:** their paths are the Console's own, decided once,
  as their slugs are reserved for organizations.

## A page's module

**What a page module exports: its page, and what its frame needs before drawing it**

![What a page module exports: its page, and what its frame needs before drawing it](console/contract.svg)

An organization’s page exports the same, but `under` and `accepts`: its route is `/acme/<part>/…`, and its module reads
the rest of the path itself. Every export but `default` is optional.

| Export | Of | Means | Without it |
| --- | --- | --- | --- |
| `default(ctx)` | Both | Draws the page: an organization's into the frame's main element, a repository's as the sections under its tabs | Required |
| `nav` | Both | `{ label, order }`: its link in the organization's nav or the repository's tabs, sorted by `order` | No link; opened from other pages |
| `allowed(can)` | Both | Whether the viewer may open it, from the permissions they hold there | Anyone who reads the organization or repository |
| `view` | Both | The name its page views report, or a function of the rest of the path that returns one | `organization.<part>` or `repository.<part>` |
| `under` | Repository | The tab it lights, as a file and a commit light Code (`""`) | Its own tab |
| `accepts(rest)` | Repository | Which rests of the path it shows; any other sends the viewer to the repository's root | Every rest |

## Styles

**One cascade on every load: the stylesheets are linked in a fixed order**

![One cascade on every load: the stylesheets are linked in a fixed order](console/styles.svg)

Every stylesheet styles the whole Console, as the one `<style>` did: a page’s rules name its own classes, so they don’t
reach another page’s elements.

- **A stylesheet beside the module it styles,** of the same name, linked by `lib/assets.js` before the first page draws,
  so no page shows unstyled. `console.css` keeps what more than one page uses.
- **One cascade:** the fixed order, and rules that name their own classes, keep every load the same. The split was
  checked against the single file by comparing every element's computed style on 99 pages, as five demo users, in light
  and dark and at a phone's width: all the same.

## Adding a page

**What a slice that adds a page writes: new files, and one line where a list must name it**

![What a slice that adds a page writes: new files, and one line where a list must name it](console/adding.svg)

Two slices that add pages touch no file in common, but those lists; one entry a line, they conflict only when both
insert between the same two lines.

- **The reserved slugs stay in Go,** where the REST API refuses a repository named like a page, one a line; a test reads
  `pages/org/` and fails until the two agree. A new slug still needs its migration, as the pages `secrets` and `agents`
  did, to move the repositories that had it.
- **Page views' names are the API's to accept:** the REST API counts only the names `api/openapi.yaml` lists
  ([monitoring](monitoring.md)), so a page that reports its own name adds it there.

## Security and failure modes

| Risk | What happens |
| --- | --- |
| A module, a stylesheet or a listing fails to load | The Console says it couldn't load its pages and to try again, instead of drawing half a page |
| A release while a tab is open | The tab keeps the release it loaded until it reloads; the next load gets every new file at once |
| A rollout, two releases behind one Service | A load asks for its files under the release `config.json` named; a server of the other release answers 404, and the shell loads again, at most three times over 13 s, then says to try again. It never runs two releases' modules together. The shell of one release may start the `console.js` of another, so `start`'s arguments stay as they are or both accept the old ones |
| The first rollout of this design | The servers before it answer `/assets/@<release>/…` with the old single page, which fails as a module, and the shell loads again; those answers carry no `Cache-Control`, which is why each try asks under a path of its own |
| An image retagged in place | Two builds under one tag share a release's path; the chart's tags are versions or commits, never reused |
| Directory listings | Public, and harmless: only the four directories the Console reads list their files (`lib/`, `pages/`, `pages/org/`, `pages/repo/` under `/assets/`), files anyone may fetch anyway; every other directory answers 404 |
| A Content Security Policy | None today, and none added. Modules and stylesheets are files of the Console's own origin, so a policy of `script-src 'self'` would need only the shell's bootstrap allowed by its hash |

## Rollout

One pull request, Part of and closing [ENG-246](https://linear.app/arikkfir/issue/ENG-246), moves the code without
changing behaviour: no migration, no chart value, no API change. The chart gives the Console's server one more variable,
`COBRANCH_RELEASE`, from its image's tag. Its preview is walked route by route as the demo users, deep links and reloads
on nested routes included, and an installation of main once it merges.

## Open questions

- **Loading pages lazily** once the Console holds many more modules: measure a first load first.
- **Page views the API refuses:** the agents, tasks and secrets pages report names the API's list lacks, as they did
  before this change, and a report holding one is refused whole. Monitoring's thread decides the names.

Cobranch is a Kfirs project, created by Arik Kfir.
