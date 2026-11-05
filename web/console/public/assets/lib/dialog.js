// Dialogs a page opens over the scrim (B's Dialog): a title with a close button, the fields or the question, and the
// actions in the footer, the one that does it last. Escape, the close button, Cancel and a click on the scrim close
// it; what failed shows in a banner at the top of its body, and the dialog stays open to try again.

import { h } from "./dom.js";
import { failure } from "./forms.js";
import { icon } from "./icons.js";

let made = 0; // numbers the dialogs, for the ids their labels and forms refer to

// openDialog opens a dialog titled title over the page, holding body (its fields, or a sentence for a question), whose
// action button, labelled action and in the danger style when danger is set, calls submit once the fields are valid;
// once submit's promise resolves the dialog closes, and when it fails the dialog shows why. It answers the dialog,
// which fires close when it goes, done or cancelled, and then leaves the page.
export function openDialog({ title, body, action, danger, submit }) {
  const id = "dialog-" + ++made;
  const error = failure();
  const close = () => el.close();
  const button = h("button", { class: "btn " + (danger ? "btn-danger" : "btn-primary"), type: "submit", form: id + "-form" }, action);
  const form = h("form", { id: id + "-form", class: "dialog-body", onsubmit: async (e) => {
    e.preventDefault();
    button.disabled = true;
    error.el.hidden = true;
    try {
      await submit();
      close();
    } catch (err) {
      error.fail(err);
      button.disabled = false;
    }
  } }, error.el, body);
  const el = h("dialog", { class: "dialog", "aria-labelledby": id + "-title", onclose: () => el.remove(),
    onclick: (e) => { if (e.target === el) close(); } },
  h("header", { class: "dialog-header" }, h("h2", { class: "dialog-title", id: id + "-title" }, title),
    h("button", { class: "icon-btn icon-btn-ghost icon-btn-sm", type: "button", "aria-label": "Close", title: "Close", onclick: close }, icon("x"))),
  form,
  h("footer", { class: "dialog-footer" }, h("button", { class: "btn", type: "button", onclick: close }, "Cancel"), button));
  document.body.append(el);
  el.showModal();
  return el;
}

// dialog opens a dialog of body whose action, label in the button's tone (btn-primary or btn-danger), runs act.
export const dialog = (title, body, label, tone, act) => openDialog({ title, body, action: label, danger: tone === "btn-danger", submit: act });

// ask asks before a step that can't be undone, with a Danger button that takes it (act), and resolves once the dialog
// closes, done or cancelled.
export const ask = (title, text, label, act) => new Promise((resolve) => dialog(title, [h("p", {}, text)], label, "btn-danger", act).addEventListener("close", resolve));
