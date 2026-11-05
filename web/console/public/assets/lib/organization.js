import { api } from "./api.js";
import { consolePages, orgPages } from "./assets.js";
import { h } from "./dom.js";
import { frame, notFound, pageHeading } from "./layout.js";
import { showRepository } from "./repository.js";
import { go } from "./router.js";
import { base, orgs, roles, setRoles } from "./state.js";

export const roleSelect = (selected) => h("select", { name: "roleId" }, roles.map((r) => h("option", { value: r.id, selected: r.id === selected }, r.name)));

// What a repository's visibility means, as the API names it.
export const visibilities = {
  private: { name: "Private", title: "Only those granted a role on it, and the organization's Owners, read it." },
  internal: { name: "Internal", title: "Others of the organization read it too, through a grant on it or an organization role." },
};

// crumbsOf is the breadcrumbs a page's module names after its organization's (and its repository's): its crumbs
// export, a list or a function of what the page shows that returns one.
export const crumbsOf = (m, ctx) => (typeof m.crumbs === "function" ? m.crumbs(ctx) : m.crumbs) || [];

// subjectOf is the conversation a page's module names for the pane (lib/pane.js), from its subject export, which
// may answer a promise of it, or else fallback: its organization's, or its repository's.
export function subjectOf(m, ctx, fallback) {
  const s = m.subject ? m.subject(ctx) : null;
  return s && typeof s.then === "function" ? s.then((r) => r || fallback, () => fallback) : s || fallback;
}

// orgSubject is an organization's conversation, which every page of the organization that names no other has: each
// viewer's own, private to them, which no page has yet.
const orgSubject = (org) => ({ key: "org:" + org.slug, title: org.name, scope: "Nothing to follow on this page", chat: null });

// tabsOf is the tabs of an organization page that has pages under it, or that is under another (its nav's under, as
// Members is under Settings): the parent's, labelled by its nav's tab or its label, then those under it, in order of
// their nav; null for a page with none.
function tabsOf(org, can, part, m) {
  const parent = m.nav && m.nav.under ? m.nav.under : part;
  const allowed = (p) => !p.allowed || p.allowed(can);
  const under = [...orgPages].filter(([, p]) => p.nav && p.nav.under === parent && allowed(p)).sort(([, a], [, b]) => a.nav.order - b.nav.order);
  const top = orgPages.get(parent);
  if (!under.length || !top || !top.nav) return null;
  const tab = ([p, t]) => h("a", { class: "tab", href: base + org.slug + (p ? "/" + p : ""), "data-nav": "", "aria-current": p === part ? "page" : null }, t.nav.tab || t.nav.label);
  return { parent, title: top.nav.label, description: top.nav.description, tabs: [tab([parent, top]), ...under.map(tab)] };
}

// An organization's pages, each a module of pages/org/ (its home index.js), and its repositories' pages, in the app
// shell (lib/layout.js). A page's module draws it into main from its default export, given the organization, main,
// can, the route's part after its own (sub) and the parts after that (rest); its nav places it in the sidebar
// ({ label, order, icon }, with items for a section and place "foot" for the sidebar's foot) or under another page,
// as one of its tabs ({ label, order, under }); its allowed(can), when it has one, says whether the viewer may open
// it, which sends anyone else to the organization's home; its crumbs name it in the breadcrumbs after the
// organization, and its subject the conversation the pane shows (lib/pane.js). Every other part of the route is a
// repository's slug: no repository takes a page's (repos.ReservedSlugs).
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
  if (shown && shown.allowed && !shown.allowed(can)) return go("/" + org.slug);
  const crumb = { label: org.name, href: base + org.slug };
  if (!shown) return showRepository(org, can, crumb, orgSubject(org), page, sub, rest);
  const ctx = { org, can, sub, rest, root: base + org.slug };
  const tabs = tabsOf(org, can, page, shown);
  const part = tabs ? tabs.parent : page;
  const main = frame({ org, can, part, crumbs: [crumb, ...crumbsOf(shown, ctx)], subject: subjectOf(shown, ctx, orgSubject(org)),
    head: tabs ? pageHeading({ title: tabs.title, description: tabs.description, tabs: tabs.tabs }) : null });
  return shown.default({ org, main, can, sub, rest });
}

// showConsolePage draws one of the Console's own pages (consolePages, pages/<part>.js) in the app shell, with the
// sidebar of the organization this browser opened last, or its first, or of none. Its module draws it into main from its
// default export, given main, the route's parts after its own (rest) and that organization, and names it in the
// breadcrumbs with its crumbs; the pane shows its subject, or says there is no conversation here.
export async function showConsolePage(part, rest) {
  const m = consolePages.get(part);
  const last = (() => { try { return localStorage.getItem("cobranch.org"); } catch { return null; } })();
  const known = orgs.find((o) => o.slug === last) || orgs[0];
  const org = known ? await api("GET", "/orgs/" + encodeURIComponent(known.slug)).catch(() => null) : null;
  const can = (p) => !!org && org.viewerPermissions.includes(p);
  const ctx = { org, rest, root: base + part };
  const none = { key: "console:" + part, title: "", scope: "Nothing to follow on this page", chat: null };
  const main = frame({ org, can, part: null, crumbs: crumbsOf(m, ctx), subject: subjectOf(m, ctx, none) });
  return m.default({ main, rest, org });
}
