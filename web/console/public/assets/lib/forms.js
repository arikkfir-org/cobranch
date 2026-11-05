// The pieces of a form and its notices, as B's components draw them (components.css): a field's label, control and
// help (Input, Select, Textarea, Copy field), a checkbox beside its label, and a banner that says what happened.

import { h } from "./dom.js";
import { icon } from "./icons.js";

// field is a control with its label above it and its help under it.
export const field = (label, control, help) => h("label", { class: "field" }, h("span", { class: "field-label" }, label), control,
  help ? h("span", { class: "field-help" }, help) : null);

// input is an Input; selectBox puts a select in a Select, with its chevron; textarea is a Textarea.
export const input = (attrs) => h("input", { class: "input", ...attrs });
export const selectBox = (select) => h("div", { class: "select" }, select);
export const textarea = (attrs) => h("textarea", { class: "textarea", ...attrs });

// copyField is a Copy field: value, read-only, with a button that copies it, under its label and over its help; where
// the clipboard refuses, the button selects the value instead, for the viewer to copy.
export function copyField(label, value, help) {
  const text = h("code", {}, value);
  const copy = h("button", { class: "icon-btn icon-btn-ghost icon-btn-sm", type: "button", "aria-label": "Copy", title: "Copy",
    onclick: () => navigator.clipboard.writeText(value).then(() => { copy.replaceChildren(icon("check")); copy.title = "Copied"; })
      .catch(() => getSelection().selectAllChildren(text)) }, icon("copy"));
  return h("div", { class: "field" }, label ? h("span", { class: "field-label" }, label) : null, h("div", { class: "copy-field" }, text, copy),
    help ? h("span", { class: "field-help" }, help) : null);
}

// checkbox is a Checkbox, the box before its label.
export const checkbox = (box, label) => h("label", { class: "checkbox" }, box, label);

// The icon each banner's tone shows.
const glyphs = { info: "info", success: "circle-check", warning: "triangle-alert", danger: "circle-x" };

// banner is a Banner in tone (info, success, warning or danger): its icon, an optional title over the text, and
// actions at its end.
export const banner = (tone, title, text, ...actions) => h("div", { class: "banner banner-" + tone, role: tone === "danger" ? "alert" : "status" },
  icon(glyphs[tone]), h("div", { class: "banner-text" }, title ? h("span", { class: "banner-title" }, title) : null, text ? h("p", {}, text) : null), actions);

// failure is a danger banner, hidden until fail(err) shows what went wrong, which it then does until the page draws again.
export function failure() {
  const text = h("p", {});
  const el = h("div", { class: "banner banner-danger", role: "alert", hidden: true }, icon("circle-x"), h("div", { class: "banner-text" }, text));
  return { el, fail: (err) => { text.textContent = err.message; el.hidden = false; } };
}
