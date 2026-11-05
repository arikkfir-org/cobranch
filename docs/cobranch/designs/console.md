# The Cobranch Console’s files

> **Snapshot.** This design lives in its [Console page](https://claude.ai/artifact/YDmfu6vfypwS1HfsVi1CSh), linked with
> Cobranch's other designs; this file is generated from it, with its diagrams, as of 2026-11-05, and synced again once
> Cobranch's shape settles.

**Status**: Proposed 2026-11-05 · [ENG-246](https://linear.app/arikkfir/issue/ENG-246) ·
[ENG-252](https://linear.app/arikkfir/issue/ENG-252) · [ENG-255](https://linear.app/arikkfir/issue/ENG-255) ·
Restructures the Console of [the architecture](architecture.md#components), [code browsing](code-browsing.md) and every
page since · Served as before: Caddy, no build step · Built by [ENG-246](https://linear.app/arikkfir/issue/ENG-246), its
first load sped up by [ENG-252](https://linear.app/arikkfir/issue/ENG-252), its app shell drawn by
[ENG-255](https://linear.app/arikkfir/issue/ENG-255)

The Console was one 3,700-line index.html: its styles, router and every page in one file, which every slice that touched
a page changed, so their pull requests conflicted and fell out of the merge queue. It becomes a shell and native ES
modules, one per page, which the Console finds by their files’ names rather than through a list, each with its
stylesheet beside it. A slice that adds a page adds files; two such slices merge in either order. Nothing the Console
does changes. Every page then draws in one app shell, the design system’s frame, from what its module exports: its place
in the sidebar, its breadcrumbs and its conversation.

## Scope

How the Console's code is laid out in files, so slices that build pages in parallel stop conflicting
([ENG-246](https://linear.app/arikkfir/issue/ENG-246)), and how a load asks for them all at once
([ENG-252](https://linear.app/arikkfir/issue/ENG-252)). Since 2026-11-02 01:40Z five approved, green pull requests fell
out of the merge queue on conflicts in `web/console/public/index.html`, the one file that held the Console's styles,
router and every page. This design splits that file without changing what the Console does: same routes, same pages,
same look, same page views. Only how a load asks for its files changes: the page names them all, so they are asked for
at once.

It also sets out the app shell every page draws in ([ENG-255](https://linear.app/arikkfir/issue/ENG-255)), the frame of
the design system's Figma file: a sidebar of the organization's places, a top bar with the breadcrumbs, the conversation
toggle and the user menu, a content area that scrolls on its own, and one conversation pane whose subject follows the
page. Each page module says what the shell shows for it, so the shell holds no list of pages either.

**A shell, shared modules, and a module per page that the Console finds by its file’s name**

![A shell, shared modules, and a module per page that the Console finds by its file’s name](console/files.svg)

Native ES modules, as browsers load them: no bundler, no build step, and the image still copies `public/` as it is. A
new page is a new file in `pages/org/` or `pages/repo/`, with its stylesheet beside it when it has styles of its own.

| Path under `web/console/public/` | Holds |
| --- | --- |
| `index.html` | The shell: a `<div id="app">`, the configuration and every file's name, which Caddy writes in, and a bootstrap that asks for every file and the viewer at once, imports its release's `console.js`, and loads again when it fails |
| `assets/console.js` | `start`: the configuration, the viewer, their organizations, every page's module and stylesheet, then the first route |
| `assets/console.css` | What every page shares: its elements and layout, on the design system's tokens (`tokens.css`, generated from its Figma file), in its typefaces (`fonts.css`, the files in `fonts/`) |
| `assets/components.css` | The design system's components, one class for each, which pages compose |
| `assets/lib/` | The router, the app shell (`layout.js`, `sidebar.js`, `pane.js`, `menu.js`, `shell.css`), the frames of an organization and a repository, the shared state, the API client, and the parts several pages draw (code, diffs, chats, reviews, runs, issues' badges, dialogs), each with its stylesheet when it has one |
| `assets/pages/` | The Console's own pages: `sign-in.js` and `welcome.js`, which the router names, and every other module with a default export, which draws in the app shell at `/<part>` (`settings.js`: Account and Preferences; `help.js`) |
| `assets/pages/org/` | An organization's pages, `<part>.js` for `/acme/<part>/…`; `index.js` its home |
| `assets/pages/repo/` | A repository's pages, `<part>.js` for `/acme/website/<part>/…`; `index.js` its code |

## A load

**A load: the shell with its configuration and files’ names, every file of its release at once with the viewer, then the
route**

![A load: the shell with its configuration and files’ names, every file of its release at once with the viewer, then the route](console/boot.svg)

Before a signed-in viewer’s first page draws, three requests wait on each other: the shell, then every file with the
viewer, then the viewer’s organizations. The single page waited for four (itself, config.json, the viewer, their
organizations), and the modules before [ENG-252](https://linear.app/arikkfir/issue/ENG-252) for about ten, each round of
imports found only once the round before arrived. A load takes every file from one release: a server serves only its own
release’s, so a load that reaches another fails whole and starts again, instead of running two releases’ modules
together. One that still fails shows “The Console couldn’t load” instead of half a Console. Navigating within the
Console loads no more modules: every page’s is already in.

- **The shell comes with what the Console would ask for first.** Caddy writes into it, as JSON, what `config.json` holds
  and the names of the files in `assets/`, `lib/`, `pages/`, `pages/org/`, `pages/repo/`, each tabbed page's directory
  (`pages/org/settings/`) and `fonts/` (its `templates`,
  at request time: no build step). The shell then asks for every module (`modulepreload`), stylesheet (`preload`) and
  the Latin part of each typeface (`preload` as a font, the part every page draws with), and for the viewer from the
  REST API, all at once, before it imports `console.js`: no import waits for the module that names it to
  arrive. Measured at 200 ms of added latency over HTTP/2, a signed-out load drew in 0.76 s, against 2.2 s for the
  modules before and 0.89 s for the single page; a repository's page as its owner in 1.19 s, against 2.41 s and 1.29 s.
  At 500 ms, 1.7 s against 5.2 s and 2.1 s.
- **The root comes from the configuration.** An operator may mount the Console at a prefix, which its route strips:
  `consoleUrl`'s path is then the root every module and link starts from, as the pages' links already did.
- **So does the release.** The configuration names the release of the server that answered (`COBRANCH_RELEASE`, which
  the chart sets to the image's tag), and the shell imports `assets/@<release>/console.js`; every module and stylesheet
  resolves from there, so one load takes them all under that path. A server serves only its own release's: while a
  rollout runs two releases behind one Service, a request that reaches the other release's server answers 404 instead of
  a file that doesn't fit the rest. The shell then loads the Console again, after 1 s, 3 s and 9 s, and after the third
  says the Console couldn't load. A failed try moves the browser to a path it never asked for (`@<release>~1/`, then
  `~2/`), which its later loads keep, so a path that failed, and whatever a browser cached for it, is never asked for
  again. A rollout surges one server at a time, so a load rarely meets both releases, and one that does starts over
  whole.
- **Every page's module loads at the start,** not when first opened. The frames need every page's `nav` and `allowed` to
  draw the nav and the tabs, and a session then runs one release's modules throughout: a release can't land between two
  pages of one visit. The cost is about sixty small requests on every load, over one connection, all asked for at once,
  which later loads answer with 304s.
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

- **No list of pages.** Caddy writes the names of the files in `pages/org/` and `pages/repo/` into the shell, and
  `lib/assets.js` imports each `.js` there; a file's name is the route's part that opens it. A new page is a new file,
  and nothing else in the Console changes. No directory is listed over HTTP.
- **Rejected: a registry module,** one import a line: two slices that add pages insert at the same place, the end, and
  conflict, the failure this design removes. **Rejected: importing `pages/org/<part>.js` on demand:** most second parts
  are repositories, each of which would cost a 404 first, and the nav must know every page before any is opened.
  **Rejected: a manifest written when the image builds:** a build step the Console doesn't have. **Rejected: asking
  Caddy for each directory's listing** (`file_server browse`, as [ENG-246](https://linear.app/arikkfir/issue/ENG-246)
  first did): the listings waited for `console.js` and its imports, and the pages for the listings, about ten requests
  in a row before the first page drew.
- **The Console's own pages are files too.** A module of `pages/` with a default export draws in the app shell at
  `/<part>`, as an organization's page does at `/acme/<part>`: Settings and Help so far. Only sign-in and welcome stay
  named in the router, since they draw outside the shell. Their paths are among the slugs no organization may take,
  which a test holds.
- **A page's tabs are files too.** A page whose views are tabs keeps them in a directory of its own name:
  `pages/org/settings/` holds Settings' General (`index.js`, at `/acme/settings`), Members, Roles, API keys,
  Secrets, Models and Usage, each at `/acme/settings/<file>`. The shell names each such directory, one
  entry in `index.html` (`pages/org/settings/` so far, so a new tabbed page adds its own), and Caddy writes its files'
  names in beside the others; `lib/tabs.js` draws the tab bar from the modules' `tab` (`{ label, order }`),
  `allowed` and `count`. A slice that adds a tab adds a file there, and no organization slug: the old addresses of
  Members, Roles, API keys and Secrets (`/acme/members`) stay files of `pages/org/` that open their tab.
  **Rejected: each tab a page `under` Settings** (`pages/org/members.js`): every new tab would take an organization
  slug, and a tab named like a page of the organization, such as Agents, would clash with it (`/acme/agents`).

## The app shell

**The app shell: each part of the frame drawn from what the page’s module exports**

![The app shell: each part of the frame drawn from what the page’s module exports](console/shell.svg)

The shell names no page: a new page that exports `nav` takes its place in the sidebar, or a tab of the page it is
`under`, and one that exports `subject` gives the pane its conversation. Below 1100px the pane opens over the content;
at a phone’s width the sidebar goes off-canvas.

- **Everything in the shell comes from the page modules.** The sidebar lists the organization's pages whose `nav` places
  them there, in `nav.order`; a page whose `nav` names another page it is `under` is a tab of that page (Tasks under
  Agents), and Settings draws its tabs from its own directory; `place: "foot"` puts Settings at the sidebar's foot. A page
  with `nav.items` is a section, which shows five sub items: those the viewer opened last in this browser, as the
  section's items have them now, then its own newest. A page that shows one of them (a repository, an issue, an agent's
  tasks) puts it first.
- **The top bar** draws the page's `crumbs` after the organization's (and the repository's), the last one in mono; then
  the pane's toggle and the avatar menu: Account, Theme (Light, Dark, System), Preferences and Log out.
- **The pane** shows the page's `subject`: the branch's chat on a branch and its commits, files, changes, runs and
  environments; an issue's comments on the issue; otherwise the organization's, which has nothing to follow yet. A
  thread on lines of a file shows them above its messages, with Resolved or Outdated beside them; a resolved thread
  folds into one row until Show opens it; a deleted branch's conversation stays, read-only; and while the pane can't
  follow, a banner says it is reconnecting. A subject is an object or a promise of one, so a file's page can ask which
  branch its ref is first. Following, the pane changes subject as the viewer moves; Pinned, it keeps the one it shows.
  Slice G ([ENG-234](https://linear.app/arikkfir/issue/ENG-234)) gives every other page a conversation.
- **Narrow screens:** below 1100px the pane opens over the content instead of beside it, with a close button of its own;
  at a phone's width the sidebar goes off-canvas behind a menu button and opens over a scrim, and a page's header stacks
  its actions under its title.
- **Rejected: a page draws its own heading and chat,** as before: every page repeated the frame, and the chat moved with
  every page's layout. **Rejected: the shell names the sections** (Issues, Repositories, Agents): the list every new
  section would edit.

## A page's module

**What a page module exports: its page, and what its frame needs before drawing it**

![What a page module exports: its page, and what its frame needs before drawing it](console/contract.svg)

An organization’s page exports the same, but `under` and `accepts`: its route is `/acme/<part>/…`, and its module reads
the rest of the path itself. Every export but `default` is optional. A tab's module (`pages/org/settings/<tab>.js`)
exports `default`, `tab`, `allowed`, `count` and `view`; its `default` draws into the page's main element and fills
`actions`, the heading's slot for the tab's own buttons.

| Export | Of | Means | Without it |
| --- | --- | --- | --- |
| `default(ctx)` | Both | Draws the page: an organization's into the frame's main element, a repository's as the sections under its tabs | Required |
| `nav` | Both | `{ label, order }`: its link in the sidebar or the repository's tabs, sorted by `order`. An organization's page adds `icon`; `items({ org, can })`, its section's sub items (`{ items, count, whole }`); `under`, the page whose tab it is, with `tab` the parent's own tab's label and `description` the heading's line; and `place: "foot"` | No link; opened from other pages |
| `crumbs` | Both | Its breadcrumbs after the organization's and the repository's: a list of `{ label, href }`, or a function of what the page shows that returns one | Only the organization's and the repository's |
| `subject(ctx)` | Both | The conversation the pane shows: `{ key, title, scope, chat, … }`, or a promise of one | The organization's, or the repository's |
| `heading` | Both | A repository's: whether the repository's heading and tabs draw above it, or a function of the rest of the path. An organization's: a function of `{ org, can, sub, rest, root }` that returns the page's heading, its tab bar included, whose actions slot the page's tab fills | A repository's draw; an organization's page has the heading of its tabs, or none |
| `tab` | Tab | `{ label, order }`: its tab in its page's tab bar | Not a tab |
| `count(ctx)` | Tab | A promise of the number its tab shows | No number |
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
- **A Console page of its own,** a module of `pages/` with a default export, needs its path among `orgs.ReservedSlugs`;
  a test fails until it is.
- **Page views' names are the API's to accept:** the REST API counts only the names `api/openapi.yaml` lists
  ([monitoring](monitoring.md)), so a page that reports its own name adds it there.

## Security and failure modes

| Risk | What happens |
| --- | --- |
| A module or a stylesheet fails to load | The Console says it couldn't load its pages and to try again, instead of drawing half a page |
| A release while a tab is open | The tab keeps the release it loaded until it reloads; the next load gets every new file at once |
| A rollout, two releases behind one Service | A load asks for its files under the release its shell names; a server of the other release answers 404, and the shell loads again, at most three times over 13 s, then says to try again. It never runs two releases' modules together. The shell and its configuration now come in one response, so a shell starts only its own release's `console.js` |
| A shell of a release before [ENG-252](https://linear.app/arikkfir/issue/ENG-252) | It still asks for `config.json`, which every directory answers, and starts this release's `console.js`, which finds no files' names and loads the Console again, now with this release's shell |
| The first rollout of this design | The servers before it answer `/assets/@<release>/…` with the old single page, which fails as a module, and the shell loads again; those answers carry no `Cache-Control`, so a browser may keep one for a while, which is why a browser never asks again for a path that failed |
| An image retagged in place | Two builds under one tag share a release's path; the chart's tags are versions or commits, never reused |
| Files' names in the shell | Public, and harmless: the names of files anyone may fetch anyway. No directory is listed over HTTP |
| A failed load's console | Every preload of a release that isn't there logs its 404, about sixty lines, before the shell loads again |
| A page's subject fails to arrive | The pane shows the organization's or the repository's conversation instead |
| A browser without storage | The sections show their own newest items instead of those the viewer opened last; the pane starts closed |
| A Content Security Policy | None today, and none added. Modules and stylesheets are files of the Console's own origin, so a policy of `script-src 'self'` would need only the shell's bootstrap allowed by its hash; the JSON Caddy writes in is data (`application/json`), not a script, and its values are escaped as JSON escapes `<`, `>` and `&` |

## Rollout

One pull request, Part of and closing [ENG-246](https://linear.app/arikkfir/issue/ENG-246), moves the code without
changing behaviour: no migration, no chart value, no API change. The chart gives the Console's server one more variable,
`COBRANCH_RELEASE`, from its image's tag. Its preview is walked route by route as the demo users, deep links and reloads
on nested routes included, and an installation of main once it merges.

[ENG-252](https://linear.app/arikkfir/issue/ENG-252), one pull request, writes the configuration and the files' names
into the shell and asks for every file at once: no chart value, no migration. `config.json` stays for shells of earlier
releases.

[ENG-255](https://linear.app/arikkfir/issue/ENG-255), one pull request first, draws every page in the app shell and
gives every page module its crumbs and subject: no chart value, no migration, no API change. Its preview is checked
against the Figma file's screens at 1440 wide, light and dark. The page groups' contents move onto the design system's
components after it ([ENG-256](https://linear.app/arikkfir/issue/ENG-256),
[ENG-258](https://linear.app/arikkfir/issue/ENG-258)).

## Open questions

- **Account's facts and Preferences' row** are Table rows until the file's Pattern/Fact row lands; the pages move to it
  then.
- **Loading pages lazily** once the Console holds many more modules: every load asks for every file, so the count grows
  with the pages. Measure again when it doubles.

Cobranch is a Kfirs project, created by Arik Kfir.
