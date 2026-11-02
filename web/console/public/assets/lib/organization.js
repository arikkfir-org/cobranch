import { api } from "./api.js";
import { orgPages } from "./assets.js";
import { app, h } from "./dom.js";
import { notFound, viewerLinks } from "./layout.js";
import { showRepository } from "./repository.js";
import { go } from "./router.js";
import { base, orgs, roles, setRoles } from "./state.js";

export const roleSelect = (selected) => h("select", { name: "roleId" }, roles.map((r) => h("option", { value: r.id, selected: r.id === selected }, r.name)));

// What a repository's visibility means, as the API names it.
export const visibilities = {
  private: { name: "Private", title: "Only those granted a role on it, and the organization's Owners, read it." },
  internal: { name: "Internal", title: "Others of the organization read it too, through a grant on it or an organization role." },
};

// An organization's pages, each a module of pages/org/ (its home index.js), and its repositories' pages, under a bar
// with the switcher and the navigation. A page's module draws it into main from its default export, given the
// organization, main, can, and the route's parts after its own; its nav ({ label, order }) puts it in the navigation,
// and its allowed(can), when it has one, says whether the viewer may open it, which sends anyone else to the
// organization's home. Every other part of the route is a repository's slug: no repository takes a page's
// (repos.ReservedSlugs).
export async function showOrganization(slug, page, sub, rest) {
  let org;
  try {
    org = await api("GET", "/orgs/" + encodeURIComponent(slug));
  } catch (err) {
    if (err.status === 404) return notFound(slug);
    throw err;
  }
  if (org.slug !== slug) return go("/" + [org.slug, page, sub].filter(Boolean).join("/"), true);
  try { localStorage.setItem("cobranch.org", org.slug); } catch { /* a convenience */ }
  setRoles((await api("GET", "/orgs/" + org.slug + "/roles")).roles);
  const can = (p) => org.viewerPermissions.includes(p);
  const shown = orgPages.get(page);
  const allowed = (m) => !m.allowed || m.allowed(can);
  const pages = [...orgPages].filter(([, m]) => m.nav && allowed(m)).sort(([, a], [, b]) => a.nav.order - b.nav.order);
  const current = shown ? page : "";
  const link = ([p, m]) => h("a", { href: base + org.slug + (p ? "/" + p : ""), "data-nav": "", class: p === current ? "current" : "" }, m.nav.label);
  const switcher = h("select", { onchange: (e) => (e.target.value === "+" ? go("/new") : go("/" + e.target.value)) },
    orgs.map((o) => h("option", { value: o.slug, selected: o.slug === org.slug }, o.name)), h("option", { value: "+" }, "Create or join…"));
  const bar = h("header", { class: "bar" }, h("a", { class: "brand", href: base, "data-nav": "" }, "Cobranch"), switcher, h("nav", {}, pages.map(link)),
    h("span", { class: "spacer" }), ...viewerLinks());
  const main = h("main", { class: "page" });
  document.body.className = "";
  app.replaceChildren(bar, main);
  if (shown && allowed(shown)) return shown.default({ org, main, can, sub, rest });
  if (shown) return go("/" + org.slug);
  return showRepository(org, main, can, page, sub, rest);
}
