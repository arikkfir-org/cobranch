// The conversation pane (App/Conversation pane): one pane on the right of every page in the shell, opened and closed
// from the top bar, whose subject follows the page, or stays while the viewer pins it. A page's module names its
// subject with its subject export (lib/organization.js, lib/repository.js), and without one the frame gives it its
// organization's or its repository's. The pane draws a subject's conversation (lib/chat.js) only while it is open,
// and keeps following it, live, while the viewer moves between pages of the same subject.
//
// A subject is { key, title, code, scope, chat, post, moderate, ask, asks, placeholder, gone, at }: two pages whose
// subjects have the same key share the conversation the pane shows; title names it in the header, in code type when
// code is set, and scope says under it who sees it. chat is the REST API's path of its conversation, or null for a
// page that has none; post and moderate are what the viewer may do there, and ask whether they may ask the
// organization's agents there (the page's agents.assign, unless the subject says); asks is the agent of an agent's
// conversation, whose name heads the pane and which every thread asks; placeholder is the composer's text; gone shows
// the conversation to be read, as it was; at holds the repository's links, for threads on lines of its files; none
// says, under the pane's empty state, what would start the subject's conversation.

import { conversation, emptyState } from "./chat.js";
import { h } from "./dom.js";
import { icon } from "./icons.js";
import { popover } from "./menu.js";

const openKey = "cobranch.pane";
const opened = () => { try { return localStorage.getItem(openKey) === "open"; } catch { return false; } };

let shown = null; // the subject the pane shows, or will once it opens
let page = null; // the subject of the page on screen, which Following shows
let pinned = false; // whether it stays on its subject while the viewer moves to another page's
let drawn = null; // { key, stop } of the conversation drawn, while the pane is open
const pane = h("aside", { class: "app-pane", "aria-label": "Conversation", hidden: !opened() });
const toggle = h("button", { class: "icon-btn app-pane-toggle", type: "button", "aria-label": "Conversation", title: "Conversation", "aria-pressed": String(opened()),
  onclick: () => setOpen(pane.hidden) }, icon("message-square"));

// paneElement and paneToggle are the pane and the top bar's button that opens and closes it, which the shell places.
export const paneElement = () => pane;
export const paneToggle = () => toggle;

// setOpen opens or closes the pane, which this browser remembers for the next page it loads.
export function setOpen(open) {
  pane.hidden = !open;
  toggle.setAttribute("aria-pressed", String(open));
  try { localStorage.setItem(openKey, open ? "open" : "closed"); } catch { /* a convenience */ }
  if (open) draw();
  else stop();
}

const stop = () => {
  if (drawn) drawn.stop.abort();
  drawn = null;
};

// leave stops following the conversation the pane shows, as a card that replaces the shell does (showCard); the pane
// draws it again once a page in the shell gives it its subject.
export const leave = () => stop();

let given = 0; // numbers the subjects pages give, so that one a page works out late never replaces a later page's

// converse gives the pane the page's subject, or a promise of it, and whether the viewer may ask the organization's
// agents on the page (ask): it shows it, unless it is pinned to another, and a link to one of the subject's threads
// opens it.
export function converse(subject, ask) {
  const n = ++given;
  Promise.resolve(subject).catch(() => null).then((p) => (p && p.ask === undefined ? { ...p, ask: !!ask } : p)).then((s) => {
    if (n !== given || !s) return;
    page = s;
    if (!pinned || !shown) shown = s;
    if (pane.hidden && shown.chat && location.hash.startsWith("#thread-")) return setOpen(true);
    if (!pane.hidden) draw();
  });
}

// modes are what the header's mode menu switches between.
const modes = { following: ["Following", "The conversation follows the page."], pinned: ["Pinned", "The conversation stays while you move to other pages."] };

// header is the pane's header: Conversation, its subject's title, the mode it is in and, while it lies over the
// content, the button that closes it; then who sees it.
function header(s) {
  const mode = h("button", { class: "app-pane-mode", type: "button", "aria-haspopup": "menu", title: modes[pinned ? "pinned" : "following"][1] },
    pinned ? icon("pin") : null, h("span", {}, modes[pinned ? "pinned" : "following"][0]), icon("chevron-down"));
  mode.addEventListener("click", () => popover(mode, "end", (close) => Object.entries(modes).map(([m, [word, title]]) =>
    h("button", { class: "menu-item", type: "button", role: "menuitemradio", "aria-checked": String(pinned === (m === "pinned")), title, onclick: () => {
      close();
      pinned = m === "pinned";
      if (!pinned && page) shown = page;
      pane.querySelector(".app-pane-header").replaceWith(header(shown));
      draw();
    } }, icon(m === "pinned" ? "pin" : "message-square"), h("span", {}, word)))));
  return h("header", { class: "app-pane-header" },
    h("div", { class: "app-pane-title" }, h("strong", {}, s.asks ? s.title : "Conversation"),
      s.title && !s.asks ? h(s.code ? "code" : "span", { class: "app-pane-subject" }, s.title) : null, mode,
      h("button", { class: "icon-btn icon-btn-ghost icon-btn-sm app-pane-close", type: "button", "aria-label": "Close the conversation", title: "Close", onclick: () => setOpen(false) }, icon("x"))),
    h("p", { class: "app-pane-scope" }, s.scope || ""));
}

// draw draws the subject's conversation into the open pane, unless it shows it already.
function draw() {
  const s = shown;
  if (!s) return void pane.replaceChildren();
  if (drawn && drawn.key === s.key) return;
  stop();
  const signal = new AbortController();
  drawn = { key: s.key, stop: signal };
  const body = h("div", { class: "app-pane-body" });
  pane.replaceChildren(header(s), body);
  if (!s.chat) return void body.append(h("div", { class: "app-pane-messages" }, emptyState(s)));
  conversation(s, signal.signal).then((el) => { if (!signal.signal.aborted) body.replaceWith(el); },
    (err) => { if (!signal.signal.aborted) body.replaceChildren(h("p", { class: "alert" }, err.message)); });
}
