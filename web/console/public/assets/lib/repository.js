import { api } from "./api.js";
import { repoPages } from "./assets.js";
import { cloneMenu } from "./code.js";
import { h } from "./dom.js";
import { icon } from "./icons.js";
import { frame, pageHeading } from "./layout.js";
import { crumbsOf, subjectOf, visibilities } from "./organization.js";
import { go } from "./router.js";
import { remember } from "./sidebar.js";
import { base, carried, config, setCarried } from "./state.js";

// A repository's slug in a form: 1 to 100 lowercase letters, digits, dots, underscores and hyphens, from a letter
// or digit. The API refuses the rest: a slug ending in .git, and the organization's own pages.
export const repoSlugInput = (value) => h("input", { name: "slug", required: true, value, placeholder: "website", pattern: "[a-z0-9][a-z0-9._\\-]*", maxlength: 100 });

// repoSubject is a repository's conversation, which every page of the repository that names no other has: each
// viewer's own, private to them, which no page has yet.
const repoSubject = (org, repo) => ({ key: "repo:" + org.slug + "/" + repo.slug, title: repo.slug, code: true, scope: "Nothing to follow on this page", chat: null });

// A repository's pages, each a module of pages/repo/ (its branches index.js), in the app shell, under the
// repository's heading and its tabs. A page's module answers the sections it shows from its default export, given the
// organization, the repository, its API path, its links, the route's parts after the page's own (rest), can, orgCan,
// the repository's root and fail; its nav ({ label, order }) makes it a tab, its under names the tab it shows under
// instead ("" for Branches), its allowed(can), when it has one, says whether the viewer may open it, and its accepts(rest), when it has one,
// which parts it shows. Its heading, true or a function of rest, says whether the repository's heading and tabs show
// above it (a page about one branch, commit or run shows its own instead); its crumbs name it in the breadcrumbs after
// the repository, and its subject the conversation the pane shows (lib/pane.js); its count(ctx), when it has one,
// answers how many its tab holds, which the tab shows once it arrives. Any other route opens the repository's
// branches, its first tab. Opening one of its pages puts the repository first in the sidebar's Repositories.
export async function showRepository(org, orgCan, orgCrumb, orgSubject, slug, sub, rest) {
  let repo;
  try {
    repo = await api("GET", "/orgs/" + org.slug + "/repos/" + encodeURIComponent(slug));
  } catch (err) {
    if (err.status !== 404) throw err;
    const main = frame({ org, can: orgCan, part: "repos", crumbs: [orgCrumb, { label: slug }], subject: orgSubject });
    return main.replaceChildren(h("h1", {}, "No such repository"), h("p", {}, "There is no repository ", h("code", {}, org.slug + "/" + slug), " that you may read."),
      h("div", {}, h("a", { class: "btn", href: base + org.slug, "data-nav": "" }, "Back to " + org.name)));
  }
  if (repo.slug !== slug) return go("/" + [org.slug, repo.slug, sub, ...rest].filter(Boolean).map(encodeURIComponent).join("/"), true);
  const can = (p) => repo.viewerPermissions.includes(p);
  const shown = repoPages.get(sub);
  const allowed = (m) => !m.allowed || m.allowed(can);
  const tabs = [...repoPages].filter(([, m]) => m.nav && allowed(m)).sort(([, a], [, b]) => a.nav.order - b.nav.order);
  // A page shown under another's tab lights that tab, as a branch lights Branches and a file, a history and a commit
  // Files.
  const tabOf = shown && shown.under !== undefined ? shown.under : sub;
  if (!tabs.some(([t]) => t === tabOf) || (shown.accepts && !shown.accepts(rest))) return go("/" + org.slug + "/" + repo.slug, true);
  const root = base + org.slug + "/" + repo.slug;
  remember(org, "repos", { key: repo.slug, label: repo.slug, href: root, code: true, icon: "folder-git-2" });
  const path = "/orgs/" + org.slug + "/repos/" + repo.slug;
  const at = repoLinks(org, repo);
  const ctx = { org, repo, path, at, rest, can, orgCan, root };
  const tab = ([t, m]) => {
    const el = h("a", { class: "tab", href: root + (t ? "/" + t : ""), "data-nav": "", "aria-current": t === tabOf ? "page" : null }, m.nav.label);
    // A count that fails to arrive leaves its tab without one.
    if (m.count) Promise.resolve(m.count(ctx)).then((n) => { if (n) el.append(h("span", { class: "tab-count" }, String(n))); }, () => {});
    return el;
  };
  const visibility = visibilities[repo.visibility];
  const headed = shown.heading === undefined || (typeof shown.heading === "function" ? shown.heading(rest) : shown.heading);
  const clone = h("button", { class: "btn", type: "button", "aria-haspopup": "dialog", onclick: () => cloneMenu(clone, org, repo, can("repo.push")) },
    "Clone", icon("chevron-down"));
  const head = headed ? pageHeading({ title: repo.name, tag: h("span", { class: "tag tone-neutral", title: visibility.title }, visibility.name),
    description: repo.description, actions: clone, tabs: tabs.map(tab) }) : null;
  const main = frame({ org, can: orgCan, part: "repos", crumbs: [orgCrumb, { label: repo.slug, href: root }, ...crumbsOf(shown, ctx)],
    subject: subjectOf(shown, ctx, repoSubject(org, repo)), head });
  const error = h("p", { class: "alert", hidden: true });
  const fail = (err) => { error.textContent = err.message; error.hidden = false; };
  if (carried) {
    fail(carried);
    setCarried(null);
  }
  const sections = await shown.default({ ...ctx, fail });
  main.replaceChildren(error, ...sections);
}

// repoLinks makes the links of a repository's code: to the history of a ref, through every parent or along first
// parents, to a branch's page and to one of its tabs (lib/branches.js branchTabs), to a commit, to a run, to an
// environment, to a directory or a file at a ref, and to a file's bytes, which the REST API serves.
export function repoLinks(org, repo) {
  const root = base + org.slug + "/" + repo.slug;
  const parts = (...names) => names.filter(Boolean).join("/").split("/").map(encodeURIComponent).join("/");
  return {
    history: (ref, first) => root + "/commits/" + parts(ref) + (first ? "?parents=first" : ""),
    branchPage: (name, tab) => root + "/branches/" + parts(name) + (tab ? "?tab=" + tab : ""),
    changes: (name) => root + "/branches/" + parts(name) + "?tab=changes",
    commit: (sha) => root + "/commit/" + sha,
    run: (number) => root + "/runs/" + number,
    environment: (id) => root + "/environments/" + id,
    tree: (ref, path) => root + "/tree/" + parts(ref, path),
    blob: (ref, path) => root + "/blob/" + parts(ref, path),
    raw: (commit, path, download) => config.restUrl + "/v1/orgs/" + org.slug + "/repos/" + repo.slug + "/raw?" +
      new URLSearchParams(download ? { ref: commit, path, download: "true" } : { ref: commit, path }),
  };
}
