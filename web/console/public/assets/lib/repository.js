import { api } from "./api.js";
import { repoPages } from "./assets.js";
import { h } from "./dom.js";
import { visibilities } from "./organization.js";
import { go } from "./router.js";
import { base, carried, config, setCarried } from "./state.js";

// A repository's slug in a form: 1 to 100 lowercase letters, digits, dots, underscores and hyphens, from a letter
// or digit. The API refuses the rest: a slug ending in .git, and the organization's own pages.
export const repoSlugInput = (value) => h("input", { name: "slug", required: true, value, placeholder: "website", pattern: "[a-z0-9][a-z0-9._\\-]*", maxlength: 100 });

// A repository's pages, each a module of pages/repo/ (its code index.js), under its heading and its tabs. A page's
// module answers the sections it shows from its default export, given the organization, the repository, its API path,
// its links, the route's parts after the page's own (rest), can, orgCan and fail; its nav ({ label, order }) makes it
// a tab, its under names the tab it shows under instead ("" for Code), its allowed(can), when it has one, says
// whether the viewer may open it, and its accepts(rest), when it has one, which parts it shows. Any other route opens
// the repository's code.
export async function showRepository(org, main, orgCan, slug, sub, rest) {
  let repo;
  try {
    repo = await api("GET", "/orgs/" + org.slug + "/repos/" + encodeURIComponent(slug));
  } catch (err) {
    if (err.status !== 404) throw err;
    return main.replaceChildren(h("h1", {}, "No such repository"), h("p", {}, "There is no repository ", h("code", {}, org.slug + "/" + slug), " that you may read."),
      h("div", {}, h("a", { class: "button", href: base + org.slug, "data-nav": "" }, "Back to " + org.name)));
  }
  if (repo.slug !== slug) return go("/" + [org.slug, repo.slug, sub, ...rest].filter(Boolean).map(encodeURIComponent).join("/"), true);
  const can = (p) => repo.viewerPermissions.includes(p);
  const shown = repoPages.get(sub);
  const allowed = (m) => !m.allowed || m.allowed(can);
  const tabs = [...repoPages].filter(([, m]) => m.nav && allowed(m)).sort(([, a], [, b]) => a.nav.order - b.nav.order);
  // A page shown under another's tab lights that tab, as a directory, a file, a history and a commit light Code.
  const tabOf = shown && shown.under !== undefined ? shown.under : sub;
  if (!tabs.some(([t]) => t === tabOf) || (shown.accepts && !shown.accepts(rest))) return go("/" + org.slug + "/" + repo.slug, true);
  const path = "/orgs/" + org.slug + "/repos/" + repo.slug;
  const tab = ([t, m]) => h("a", { href: base + org.slug + "/" + repo.slug + (t ? "/" + t : ""), "data-nav": "", class: t === tabOf ? "current" : "" }, m.nav.label);
  const visibility = visibilities[repo.visibility];
  const heading = h("div", { class: "heading" }, h("p", {}, h("a", { href: base + org.slug, "data-nav": "" }, org.name), " / ", h("code", {}, repo.slug)),
    h("div", { class: "row" }, h("h1", {}, repo.name), h("span", { class: "chip", title: visibility.title }, visibility.name)),
    repo.description ? h("p", {}, repo.description) : null, h("nav", { class: "tabs" }, tabs.map(tab)));
  const error = h("p", { class: "alert", hidden: true });
  const fail = (err) => { error.textContent = err.message; error.hidden = false; };
  if (carried) {
    fail(carried);
    setCarried(null);
  }
  const at = repoLinks(org, repo);
  const sections = await shown.default({ org, repo, path, at, rest, can, orgCan, fail });
  main.replaceChildren(heading, error, ...sections);
}

// repoLinks makes the links of a repository's code: to the history of a ref, through every parent or along first
// parents, to a branch's page and its changes, to a commit, to a run, to an environment, to a directory or a file
// at a ref, and to a file's bytes, which the REST API serves.
export function repoLinks(org, repo) {
  const root = base + org.slug + "/" + repo.slug;
  const parts = (...names) => names.filter(Boolean).join("/").split("/").map(encodeURIComponent).join("/");
  return {
    history: (ref, first) => root + "/commits/" + parts(ref) + (first ? "?parents=first" : ""),
    branchPage: (name) => root + "/branches/" + parts(name),
    changes: (name) => root + "/changes/" + parts(name),
    commit: (sha) => root + "/commit/" + sha,
    run: (number) => root + "/runs/" + number,
    environment: (id) => root + "/environments/" + id,
    tree: (ref, path) => root + "/tree/" + parts(ref, path),
    blob: (ref, path) => root + "/blob/" + parts(ref, path),
    raw: (commit, path, download) => config.restUrl + "/v1/orgs/" + org.slug + "/repos/" + repo.slug + "/raw?" +
      new URLSearchParams(download ? { ref: commit, path, download: "true" } : { ref: commit, path }),
  };
}
