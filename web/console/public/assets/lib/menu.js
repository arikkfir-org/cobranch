// Menus the shell opens from a button: the avatar menu, the organization switcher, the conversation's mode. One is
// open at a time; it closes on a choice, a click outside, Escape, or a move to another page.

import { h } from "./dom.js";

let open = null; // the menu open now: { el, anchor, close }

// closeMenu closes the menu open now, if any.
export function closeMenu() {
  if (open) open.close();
}

// popover opens a menu (B's .menu) under anchor, its start or end edge aligned with the anchor's (align "start" or
// "end"), or above it, aligned with its start (align "above"), holding what build(close) returns, with the shell's
// class for it, if any, and answers it; the anchor's button opens it again once closed, and a second click on it
// closes it (answering null). It reports itself to assistive technology through the anchor's aria-expanded.
export function popover(anchor, align, build, kind = "") {
  const again = open && open.anchor === anchor;
  closeMenu();
  if (again) return null;
  const el = h("div", { class: ("menu app-popover " + kind).trim(), role: "menu" });
  const close = () => {
    if (!open || open.el !== el) return;
    open = null;
    el.remove();
    anchor.setAttribute("aria-expanded", "false");
    document.removeEventListener("pointerdown", outside, true);
    document.removeEventListener("keydown", keys, true);
  };
  const outside = (e) => { if (!el.contains(e.target) && !anchor.contains(e.target)) close(); };
  const items = () => [...el.querySelectorAll(".menu-item:not([disabled]), .segmented button")];
  const keys = (e) => {
    if (e.key === "Escape") {
      close();
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
