// The app shell every signed-in page sits in: the sidebar, the top bar with the breadcrumbs, the conversation toggle
// and the avatar menu, the content area, which alone scrolls, and the conversation pane; and the card a page outside
// it draws (signing in, the welcome, a page that couldn't load).

import { logo } from "./brand.js";
import { app, h } from "./dom.js";
import { avatar, icon } from "./icons.js";
import { closeMenu, popover } from "./menu.js";
import { converse, leave, paneElement, paneToggle } from "./pane.js";
import { bareSidebar, sidebar } from "./sidebar.js";
import { base, config, me } from "./state.js";
import { setTheme, theme, themes } from "./theme.js";

export function showCard(...children) {
  leave();
  document.body.className = "centered";
  app.replaceChildren(h("main", { class: "card" }, h("h1", { class: "brand" }, logo()), ...children));
}

export function signOut() {
  fetch(config.authUrl + "/sign-out", { method: "POST", credentials: "include" }).finally(() => location.replace(base));
}

export function notFound(slug) {
  showCard(h("h2", {}, "No such organization"), h("p", {}, "There is no organization ", h("code", {}, slug), " that you may see."), h("a", { class: "button", href: base, "data-nav": "" }, "Back"));
}

// themeNames name the themes lib/theme.js knows.
export const themeNames = { light: "Light", dark: "Dark", system: "System" };

// themeSwitch is the theme as a segmented control (Light, Dark, System), which changes it at once and saves it with
// the person; saved hears how each save ended, null or its error, which the avatar menu's switch leaves to Preferences
// to report.
export function themeSwitch(saved = () => {}) {
  const buttons = themes.map((value) => h("button", { type: "button", "aria-pressed": String(theme() === value), onclick: () => {
    setTheme(value).then(() => saved(null), saved);
    buttons.forEach((b, i) => b.setAttribute("aria-pressed", String(themes[i] === value)));
  } }, themeNames[value]));
  return h("div", { class: "segmented", role: "group", "aria-label": "Theme" }, buttons);
}

// userMenu opens the avatar menu (App/User menu): who is signed in, their Account, the theme, their Preferences and
// signing out.
function userMenu(anchor) {
  const email = me.emails && me.emails.length ? me.emails[0].email : "";
  popover(anchor, "end", (close) => [
    h("div", { class: "app-user-menu-who" }, h("strong", {}, me.displayName), email ? h("span", {}, email) : null),
    h("a", { class: "menu-item", href: base + "settings", "data-nav": "", role: "menuitem", onclick: close }, icon("user"), h("span", {}, "Account")),
    h("div", { class: "app-user-menu-theme" }, h("span", {}, "Theme"), themeSwitch()),
    h("a", { class: "menu-item", href: base + "settings/preferences", "data-nav": "", role: "menuitem", onclick: close }, icon("cog"), h("span", {}, "Preferences")),
    h("hr", { class: "menu-divider" }),
    h("button", { class: "menu-item menu-item-danger", type: "button", role: "menuitem", onclick: () => { close(); signOut(); } }, icon("log-out"), h("span", {}, "Log out")),
  ], "app-user-menu");
}

// pageHeading is the heading a group of pages shares above their tabs, as a repository's and Settings' do: B's Page
// header, its title with a tag beside it, what it is and the actions at its end, then its tabs (B's Tab), each a link
// to its page.
export const pageHeading = ({ title, tag, description, actions, tabs }) => h("div", { class: "app-page-heading" },
  pageHeader({ title, tag, description, actions }), tabs && tabs.length ? h("nav", { class: "tab-bar", "aria-label": title }, tabs) : null);

// pageHeader is B's Page header, the top of a page's content: its title, with a tag beside it, a line on what the page
// holds, and its actions on the right, primary last.
export const pageHeader = ({ title, tag, description, actions }) => h("header", { class: "page-header" },
  h("div", { class: "page-header-text" },
    tag ? h("div", { class: "app-page-heading-title" }, h("h1", { class: "page-header-title" }, title), tag) : h("h1", { class: "page-header-title" }, title),
    description ? h("p", { class: "page-header-description" }, description) : null),
  actions ? h("div", { class: "page-header-actions" }, actions) : null);

// crumb is one of the breadcrumbs: a link to its page, or the page shown, the last, in code type, as App/Top bar draws
// it.
const crumb = (c, last) => last ? h("li", { "aria-current": "page" }, h("code", {}, c.label))
  : h("li", {}, c.href ? h("a", { href: c.href, "data-nav": "" }, c.label) : h("span", {}, c.label));

// The shell's elements, which every page shown in it keeps, the pane above all: it keeps following its conversation
// while the viewer moves between pages.
let shell = null;

function build() {
  const menu = h("button", { class: "icon-btn icon-btn-ghost app-sidebar-toggle", type: "button", "aria-label": "Menu", title: "Menu",
    onclick: () => shell.root.classList.toggle("app-sidebar-open") }, icon("menu"));
  const viewer = h("button", { class: "app-avatar-button", type: "button", "aria-haspopup": "menu", "aria-label": "Your account, " + me.displayName, title: me.displayName,
    onclick: () => userMenu(viewer) }, avatar(me.displayName, false, 24));
  const crumbs = h("ol");
  const content = h("div", { class: "app-content" });
  const root = h("div", { class: "app" }, h("div", { class: "app-sidebar-slot" }),
    h("div", { class: "app-main" }, h("header", { class: "app-topbar" }, menu, h("nav", { class: "app-crumbs", "aria-label": "Breadcrumb" }, crumbs), paneToggle(), viewer),
      h("div", { class: "app-body" }, content, paneElement())),
    h("div", { class: "app-scrim", onclick: () => root.classList.remove("app-sidebar-open") }));
  shell = { root, crumbs, content };
}

// recrumb draws the breadcrumbs, each { label, href }, the last the page shown, and names the browser's tab after it,
// as frame does, and as a page does again once it has read what it shows.
export function recrumb(crumbs) {
  shell.crumbs.replaceChildren(...crumbs.map((c, i) => crumb(c, i === crumbs.length - 1)));
  document.title = (crumbs.length ? crumbs[crumbs.length - 1].label + " · " : "") + "Cobranch";
}

// frame draws the shell for a page and answers the element the page draws into: the sidebar of org (where the viewer
// acts with can, and the page shown is the organization's part), or of no organization when org is null; the
// breadcrumbs, each { label, href }, the last the page shown; the heading above the page, when its group of
// pages has one (pageHeading); and the conversation pane's subject, as a page's module names it (lib/pane.js).
export function frame({ org, can, part, crumbs, subject, head }) {
  if (!shell) build();
  closeMenu();
  document.body.className = "app-body-shell";
  if (!shell.root.isConnected) app.replaceChildren(shell.root);
  shell.root.classList.remove("app-sidebar-open");
  shell.root.querySelector(".app-sidebar-slot").replaceChildren(org ? sidebar(org, can, part) : bareSidebar());
  recrumb(crumbs);
  const main = h("main", { class: "page" });
  shell.content.replaceChildren(...[head, main].filter(Boolean));
  shell.content.scrollTop = 0;
  converse(subject, can("agents.assign"));
  return main;
}
