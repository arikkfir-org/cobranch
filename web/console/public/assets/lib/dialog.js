// Dialogs a page opens over the scrim (B's Dialog): a form, or a question before a step that can't be undone.

import { h } from "./dom.js";
import { icon } from "./icons.js";

// dialog opens B's Dialog: its title and close button, its body, and its footer's Cancel and action (label, in the
// button's tone), which runs act and closes it once act resolves, or shows in the dialog why it failed. Escape, Cancel,
// the close button and a click on the scrim close it, and it leaves the page once closed.
export function dialog(title, body, label, tone, act) {
  const error = h("div", { class: "banner banner-danger", role: "alert", hidden: true }, icon("circle-x"), h("div", { class: "banner-text" }, h("p", {})));
  const el = h("dialog", { class: "dialog", "aria-label": title });
  const close = () => el.close();
  const go = h("button", { class: "btn " + tone, type: "button", onclick: () => {
    go.disabled = true;
    Promise.resolve().then(act).then(close, (err) => { error.querySelector("p").textContent = err.message; error.hidden = false; }).finally(() => { go.disabled = false; });
  } }, label);
  el.append(h("div", { class: "dialog-header" }, h("h2", { class: "dialog-title" }, title),
    h("button", { class: "icon-btn icon-btn-ghost icon-btn-sm", type: "button", "aria-label": "Close", title: "Close", onclick: close }, icon("x"))),
  h("div", { class: "dialog-body" }, error, ...body), h("div", { class: "dialog-footer" }, h("button", { class: "btn", type: "button", onclick: close }, "Cancel"), go));
  el.addEventListener("click", (e) => { if (e.target === el) close(); });
  el.addEventListener("close", () => el.remove());
  document.body.append(el);
  el.showModal();
  return el;
}

// ask asks before a step that can't be undone, with a Danger button that takes it (act), and resolves once the dialog
// closes, done or cancelled.
export const ask = (title, text, label, act) => new Promise((resolve) => dialog(title, [h("p", {}, text)], label, "btn-danger", act).addEventListener("close", resolve));
