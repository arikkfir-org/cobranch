// The app shell's sidebar (App/Sidebar): the logo, which opens the organization's Overview, the organization's
// places, the switcher between the viewer's organizations, and Settings at its foot. No list here names a page: each
// place is an organization page whose module's nav puts it there (lib/assets.js), and a section's five sub items are
// those the viewer opened last in this browser, topped up from the section's own items.

import { orgPages } from "./assets.js";
import { logo } from "./brand.js";
import { h } from "./dom.js";
import { avatar, icon } from "./icons.js";
import { popover } from "./menu.js";
import { go } from "./router.js";
import { base, orgs } from "./state.js";

// shown is how many sub items a section shows.
const shown = 5;
// fresh is how long a section's items stay as the sidebar last read them, in milliseconds, before a page that draws it
// reads them again.
const fresh = 60000;
// read holds each section's items as last read, by the organization's slug and the section's part.
const read = new Map();

// recentKey is where this browser keeps the sub items of an organization's section the viewer opened last.
const recentKey = (org, part) => "cobranch.recent." + org.id + "." + part;

// recent is the items of an organization's section the viewer opened last in this browser, the last first.
function recent(org, part) {
  try {
    const list = JSON.parse(localStorage.getItem(recentKey(org, part)) || "[]");
    return Array.isArray(list) ? list.filter((i) => i && typeof i.key === "string" && typeof i.href === "string") : [];
  } catch {
    return [];
  }
}

// remember puts item, a sub item of an organization's section (the page part that opens the section: "repos",
// "issues", "agents"), first among those its viewer opened last, as a page that shows the item does: a repository's
// pages, an issue's. An item is { key, label, href, code, icon }, with avatar "agent" for an agent's, and the sidebar
// draws it again at once when it shows the section.
export function remember(org, part, item) {
  const list = [item, ...recent(org, part).filter((i) => i.key !== item.key)].slice(0, shown * 2);
  try {
    localStorage.setItem(recentKey(org, part), JSON.stringify(list));
  } catch { /* a convenience: the section shows its own items instead */ }
  const section = document.querySelector('.app-sidebar [data-section="' + part + '"]');
  if (section && section.dataset.org === org.slug) drawItems(org, part, section);
}

// subItems are the five sub items a section shows: those the viewer opened last, as the section's items have them
// now, then the section's items. A section whose items are all of them (whole) drops what the viewer opened that is
// gone; one that reads only its newest keeps them.
function subItems(org, part) {
  const got = read.get(org.slug + "/" + part);
  const items = got ? got.items : [];
  const byKey = new Map(items.map((i) => [i.key, i]));
  const mine = recent(org, part).filter((i) => !got || !got.whole || byKey.has(i.key)).map((i) => byKey.get(i.key) || i);
  const keys = new Set(mine.map((i) => i.key));
  return [...mine, ...items.filter((i) => !keys.has(i.key))].slice(0, shown);
}

// current is whether a link's path is the page shown, or a page under it.
const current = (href) => location.pathname === href || location.pathname.startsWith(href + "/");

// subItem is a section's row (Nav/Sub item): its leading icon or an agent's avatar, its label, and its status dot.
function subItem(i) {
  const leading = i.avatar === "agent" ? avatar(i.label, true) : icon(i.icon || "circle");
  return h("a", { class: "nav-sub-item", href: i.href, "data-nav": "", "aria-current": current(i.href) ? "page" : null, title: i.title || i.label },
    leading, h(i.code ? "code" : "span", { class: "nav-sub-item-label" }, i.label),
    i.dot ? h("span", { class: "nav-sub-item-dot tone-" + i.dot, role: "img", "aria-label": i.dotLabel || null }) : null);
}

// drawItems draws a section's sub items, and its count when its items know it.
function drawItems(org, part, section) {
  const got = read.get(org.slug + "/" + part);
  const count = section.querySelector(".nav-item-count");
  if (count) count.textContent = got && got.count !== undefined && got.count !== null ? String(got.count) : "";
  section.querySelector(".nav-sub-items").replaceChildren(...subItems(org, part).map(subItem));
}

// readItems reads a section's items from its module's nav.items, unless the sidebar read them less than a minute ago,
// and draws them again once they arrive; a section that fails to read keeps those it showed.
function readItems(org, can, part, m, section) {
  const key = org.slug + "/" + part;
  const got = read.get(key);
  if (got && Date.now() - got.at < fresh) return;
  read.set(key, { ...(got || { items: [] }), at: Date.now() });
  Promise.resolve(m.nav.items({ org, can })).then((r) => {
    read.set(key, { items: r.items, count: r.count, whole: !!r.whole, at: Date.now() });
    if (section.isConnected) drawItems(org, part, section);
  }).catch(() => { /* the section keeps what it showed; the next page tries again */ });
}

// navItem is one of the sidebar's places (Nav item): its icon, its label and, for a section, its count.
const navItem = (org, part, m, selected) => h("a", { class: "nav-item", href: base + org.slug + (part ? "/" + part : ""), "data-nav": "", "aria-current": selected ? "page" : null },
  icon(m.nav.icon || "circle"), h("span", { class: "nav-item-label" }, m.nav.label), m.nav.items ? h("span", { class: "nav-item-count" }) : null);

// orgMark is an organization's mark: the first letter of its name, on ink.
export const orgMark = (org) => h("span", { class: "app-org-mark", "aria-hidden": "true" }, (org.name.trim()[0] || "?").toUpperCase());

// switcher opens a menu of the viewer's organizations, the one shown checked, and the page that creates or joins one.
function switcher(org) {
  const button = h("button", { class: "app-org-switcher", type: "button", "aria-haspopup": "menu", "aria-label": "Switch organization" },
    orgMark(org), h("span", { class: "app-org-name" }, org.name), icon("chevron-down"));
  button.addEventListener("click", () => popover(button, "start", (close) => [
    ...orgs.map((o) => h("button", { class: "menu-item", type: "button", role: "menuitemradio", "aria-checked": String(o.slug === org.slug),
      onclick: () => { close(); if (o.slug !== org.slug) go("/" + o.slug); } }, orgMark(o), h("span", {}, o.name))),
    h("hr", { class: "menu-divider" }),
    h("button", { class: "menu-item", type: "button", onclick: () => { close(); go("/new"); } }, icon("plus"), h("span", {}, "Create or join an organization")),
  ]));
  return button;
}

// sidebar draws the sidebar of org, which the viewer may act in with can, where the page shown is the organization's
// part (its page part, or the part of the page it shows under; "repos" for a repository's): the places its pages'
// modules name, in their order, each section with a divider before the first and its sub items, then the switcher and
// the places at the foot.
export function sidebar(org, can, part) {
  const places = [...orgPages].filter(([, m]) => m.nav && !m.nav.under && (!m.allowed || m.allowed(can))).sort(([, a], [, b]) => a.nav.order - b.nav.order);
  const nav = h("nav", { class: "app-sidebar-nav", "aria-label": org.name });
  let divided = false;
  for (const [p, m] of places.filter(([, m]) => m.nav.place !== "foot")) {
    if (m.nav.items && !divided) {
      nav.append(h("hr", { class: "app-sidebar-divider" }));
      divided = true;
    }
    if (!m.nav.items) {
      nav.append(navItem(org, p, m, p === part));
      continue;
    }
    const section = h("div", { class: "app-sidebar-section", "data-section": p, "data-org": org.slug }, navItem(org, p, m, p === part), h("div", { class: "nav-sub-items" }));
    nav.append(section);
    drawItems(org, p, section);
    readItems(org, can, p, m, section);
  }
  const foot = places.filter(([, m]) => m.nav.place === "foot").map(([p, m]) => navItem(org, p, m, p === part));
  return h("aside", { class: "app-sidebar" },
    h("a", { class: "app-sidebar-logo", href: base + org.slug, "data-nav": "", "aria-label": "Cobranch: " + org.name + "'s overview" }, logo()),
    h("hr", { class: "app-sidebar-divider" }), nav, h("div", { class: "app-sidebar-flex" }),
    h("hr", { class: "app-sidebar-divider" }), switcher(org), h("hr", { class: "app-sidebar-divider" }), ...foot,
    h("p", { class: "app-sidebar-credit" }, "Cobranch is a Kfirs project"));
}

// bareSidebar is the sidebar of a viewer in no organization yet: the logo, and the page that creates or joins one.
export const bareSidebar = () => h("aside", { class: "app-sidebar" },
  h("a", { class: "app-sidebar-logo", href: base, "data-nav": "", "aria-label": "Cobranch" }, logo()), h("hr", { class: "app-sidebar-divider" }),
  h("nav", { class: "app-sidebar-nav", "aria-label": "Cobranch" }, h("a", { class: "nav-item", href: base + "new", "data-nav": "" }, icon("plus"), h("span", { class: "nav-item-label" }, "Create or join an organization"))),
  h("div", { class: "app-sidebar-flex" }), h("p", { class: "app-sidebar-credit" }, "Cobranch is a Kfirs project"));
