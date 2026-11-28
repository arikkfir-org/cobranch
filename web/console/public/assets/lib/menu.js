// Menus the shell opens from a button: the avatar menu, the organization switcher, the conversation's mode. One is
// open at a time; it closes on a choice, a click outside, Escape, or a move to another page. A check menu (checkMenu,
// opened by checkButton) stays open through its choices and saves them as it closes, unless Escape closed it.

import { h } from "./dom.js";
import { icon } from "./icons.js";

let open = null; // the menu open now: { el, anchor, close }

// closeMenu closes the menu open now, if any.
export function closeMenu() {
  if (open) open.close();
}

// popover opens a menu (B's .menu) under anchor, its start or end edge aligned with the anchor's (align "start" or
// "end"), or above it, aligned with its start (align "above"), holding what build(close) returns, with the shell's
// class for it, if any, and answers it; the anchor's button opens it again once closed, and a second click on it
// closes it (answering null). It reports itself to assistive technology through the anchor's aria-expanded. closed,
// when given, runs once the menu closes, however it does, told whether Escape closed it.
export function popover(anchor, align, build, kind = "", closed = null) {
  const again = open && open.anchor === anchor;
  closeMenu();
  if (again) return null;
  const el = h("div", { class: ("menu app-popover " + kind).trim(), role: "menu" });
  const close = (escaped = false) => {
    if (!open || open.el !== el) return;
    open = null;
    el.remove();
    anchor.setAttribute("aria-expanded", "false");
    document.removeEventListener("pointerdown", outside, true);
    document.removeEventListener("keydown", keys, true);
    if (closed) closed(escaped === true);
  };
  const outside = (e) => { if (!el.contains(e.target) && !anchor.contains(e.target)) close(); };
  const items = () => [...el.querySelectorAll(".menu-item:not([disabled]), .segmented button")];
  const keys = (e) => {
    if (e.key === "Escape") {
      close(true);
      anchor.focus();
    } else if (e.key === "ArrowDown" || e.key === "ArrowUp") {
      e.preventDefault();
      const all = items();
      const at = all.indexOf(document.activeElement);
      all[(at + (e.key === "ArrowDown" ? 1 : all.length - 1)) % all.length]?.focus();
    }
  };
  el.append(...build(close).flat(Infinity).filter(Boolean));
  document.body.append(el);
  const r = anchor.getBoundingClientRect();
  el.style.top = Math.round(r.bottom + 4) + "px";
  if (align === "end") el.style.right = Math.round(document.documentElement.clientWidth - r.right) + "px";
  else el.style.left = Math.round(r.left) + "px";
  // A menu that would leave the window opens above its button instead.
  if (align === "above" || el.getBoundingClientRect().bottom > window.innerHeight) el.style.top = Math.max(4, Math.round(r.top - 4 - el.offsetHeight)) + "px";
  open = { el, anchor, close };
  anchor.setAttribute("aria-expanded", "true");
  document.addEventListener("pointerdown", outside, true);
  document.addEventListener("keydown", keys, true);
  items()[0]?.focus();
  return el;
}

// checkMenu opens a menu of choices (each { id, name, title, lead, checked }, lead an element before the name) under
// anchor, as popover does, that stays open while they are checked and unchecked, and on closing (a click outside, the
// anchor again, or focus moving out of it) calls save(added, removed) with the choices whose check changed, if any;
// Escape closes it without saving. With keepOne, the last choice still checked can't be unchecked, and says why in why.
export function checkMenu(anchor, align, choices, save, { keepOne = false, why = "" } = {}) {
  const checked = new Set(choices.filter((c) => c.checked).map((c) => c.id));
  const items = choices.map((c) => h("button", { class: "menu-item", type: "button", role: "menuitemcheckbox", title: c.title || null,
    onclick: () => { checked.has(c.id) ? checked.delete(c.id) : checked.add(c.id); mark(); } }, c.lead || null, h("span", {}, c.name)));
  // mark shows each choice as it stands, and locks the last one checked when one must stay.
  const mark = () => items.forEach((item, i) => {
    const on = checked.has(choices[i].id);
    const locked = keepOne && on && checked.size === 1;
    item.setAttribute("aria-checked", String(on));
    item.disabled = locked;
    item.title = locked ? why : choices[i].title || "";
  });
  mark();
  const el = popover(anchor, align, () => items, "", (escaped) => {
    const added = choices.filter((c) => !c.checked && checked.has(c.id));
    const removed = choices.filter((c) => c.checked && !checked.has(c.id));
    if (!escaped && (added.length || removed.length)) save(added, removed, choices.filter((c) => checked.has(c.id)));
  });
  // Focus leaving it closes it too. Tab hands focus back to the anchor first, so the browser moves on from there, as
  // from the button that opened it; focus that moves elsewhere on the page closes it as it goes, while a click inside
  // that takes no focus moves it nowhere.
  el?.addEventListener("keydown", (e) => { if (e.key === "Tab") { closeMenu(); anchor.focus(); } });
  el?.addEventListener("focusout", (e) => { if (e.relatedTarget && !el.contains(e.relatedTarget) && e.relatedTarget !== anchor) closeMenu(); });
  return el;
}

// checkButton is a value of several choices (each as checkMenu takes it) shown as a small ghost button, the names of
// those checked or empty when none is, that opens them as a check menu. Once the menu closes with a change, the button
// shows the choices now checked with a spinning loader-circle in place of its chevron, busy and not opening, until
// save(added, removed) settles; a page that draws itself again then replaces it, and one that doesn't gets it back as
// it was. It is the Console's control for any value that holds several choices: roles, labels.
export function checkButton({ choices, save, empty, align = "start", keepOne = false, why = "", ...attrs }) {
  const names = (picked) => picked.map((c) => c.name).join(", ") || empty;
  const button = h("button", { class: "btn btn-ghost btn-sm", type: "button", "aria-haspopup": "menu", ...attrs },
    names(choices.filter((c) => c.checked)), icon("chevron-down"));
  button.addEventListener("click", () => button.ariaBusy !== "true" && checkMenu(button, align, choices, (added, removed, picked) => {
    const shown = [...button.childNodes];
    button.replaceChildren(names(picked), icon("loader-circle", "Saving"));
    button.ariaBusy = "true";
    Promise.resolve(save(added, removed)).catch(() => null).finally(() => {
      if (!button.isConnected) return;
      button.replaceChildren(...shown);
      button.ariaBusy = null;
    });
  }, { keepOne, why }));
  return button;
}
