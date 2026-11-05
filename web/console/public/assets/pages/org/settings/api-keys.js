import { api } from "../../../lib/api.js";
import { openDialog } from "../../../lib/dialog.js";
import { h } from "../../../lib/dom.js";
import { banner, checkbox, failure, field, input, selectBox } from "../../../lib/forms.js";
import { when } from "../../../lib/format.js";
import { icon } from "../../../lib/icons.js";
import { popover } from "../../../lib/menu.js";
import { roles } from "../../../lib/state.js";

export const tab = { label: "API keys", order: 30 };
export const view = "organization.api-keys";
export default ({ org, main, can, actions }) => showApiKeys(org, main, can, actions);

// expiryPicker chooses when a new secret stops working: in a year (the default), never, or on a date.
function expiryPicker() {
  const date = input({ type: "date", hidden: true, "aria-label": "Expires on" });
  const pick = h("select", { onchange: () => { date.hidden = pick.value !== "date"; date.required = !date.hidden; } },
    h("option", { value: "year" }, "Expires in a year"), h("option", { value: "never" }, "Never expires"), h("option", { value: "date" }, "Expires on a date…"));
  const value = () => pick.value === "never" ? { neverExpires: true } : pick.value === "date" ? { expiresAt: new Date(date.value + "T00:00:00").toISOString() } : {};
  return { el: field("Expiry", h("div", { class: "field" }, selectBox(pick), date)), value };
}

// live is whether a key's secret still works: neither revoked nor expired.
const live = (s) => !s.revokedAt && !(s.expiresAt && new Date(s.expiresAt) <= Date.now());

// secretState is where a secret stands: revoked, expired, or when it expires and when it was last used.
function secretState(s) {
  if (s.revokedAt) return "revoked " + when(s.revokedAt);
  if (!live(s)) return "expired " + when(s.expiresAt);
  return (s.expiresAt ? "expires " + when(s.expiresAt) : "never expires") + (s.lastUsedAt ? ", last used " + when(s.lastUsedAt) : ", never used");
}

// showApiKeys lists the organization's API keys, each with its roles and its secrets; a viewer with
// org.principals.manage makes, rotates, revokes and deletes them. made is a key just made or rotated, whose secret this
// draw shows once, as the API never shows it again.
async function showApiKeys(org, main, can, actions, made = null) {
  const manage = can("org.principals.manage");
  const error = failure();
  const refresh = (created) => showApiKeys(org, main, can, actions, created || null);
  const path = "/orgs/" + org.slug + "/api-keys";
  const keys = (await api("GET", path)).apiKeys;
  actions.replaceChildren(manage ? h("button", { class: "btn btn-primary", type: "button", onclick: () => makeKey(path, refresh) }, icon("plus"), "Make a key") : "");
  const menu = (k) => {
    const working = k.secrets.filter(live).length;
    const button = h("button", { class: "icon-btn icon-btn-ghost icon-btn-sm", type: "button", "aria-haspopup": "menu", "aria-label": "More for " + k.name, title: "More for " + k.name }, icon("ellipsis"));
    button.addEventListener("click", () => popover(button, "end", (close) => [
      h("button", { class: "menu-item", type: "button", role: "menuitem", disabled: working >= 2, title: working >= 2 ? "Two secrets already work; revoke one first." : "A new secret beside the old one",
        onclick: () => { close(); rotate(path + "/" + k.id, k, refresh); } }, icon("key-round"), h("span", {}, "Rotate")),
      k.secrets.filter((s) => !s.revokedAt).map((s) => h("button", { class: "menu-item", type: "button", role: "menuitem",
        onclick: () => { close(); api("DELETE", path + "/" + k.id + "/secrets/" + s.id).then(() => refresh()).catch(error.fail); } }, icon("x"), h("span", {}, "Revoke …" + s.hint))),
      h("hr", { class: "menu-divider" }),
      h("button", { class: "menu-item menu-item-danger", type: "button", role: "menuitem", onclick: () => {
        close();
        openDialog({ title: "Delete " + k.name + "?", action: "Delete", danger: true, body: h("p", {}, "Every script that signs in with its secrets stops working at once."),
          submit: () => api("DELETE", path + "/" + k.id).then(() => refresh()) });
      } }, icon("trash-2"), h("span", {}, "Delete")),
    ]));
    return button;
  };
  const row = (k) => {
    const working = k.secrets.filter(live).length;
    return h("tr", {},
      h("td", {}, h("div", { class: "row-text" }, h("span", { class: "row-title" }, k.name), h("span", { class: "row-meta" }, "Made " + when(k.createdAt) + (working ? "" : " · no working secret: rotate it")))),
      h("td", {}, k.roles.length ? k.roles.map((r) => h("span", { class: "tag tone-neutral" }, r.name)) : h("span", { class: "cell-muted" }, "No role")),
      h("td", {}, h("div", { class: "row-text" }, k.secrets.map((s) => h("span", { class: "row-meta" }, h("code", {}, "…" + s.hint), " " + secretState(s))))),
      h("td", { class: "cell-actions" }, manage ? menu(k) : null));
  };
  main.replaceChildren(error.el, made ? reveal(made) : "",
    h("p", { class: "field-help" }, "A key is a principal of " + org.name + " that a script acts as, with roles of its own; it never borrows a person's access. Rotating a key adds a second secret, so the script changes over without failing; then revoke the old one."),
    keys.length ? h("table", { class: "table" }, h("thead", {}, h("tr", {}, h("th", {}, "Key"), h("th", {}, "Roles"), h("th", {}, "Secrets"), h("th", { "aria-label": "Actions" }))), h("tbody", {}, keys.map(row)))
      : h("div", { class: "card" }, h("div", { class: "empty" }, h("div", { class: "empty-icon" }, icon("key-round")), h("h2", { class: "empty-title" }, "No API keys yet"),
        h("p", { class: "empty-description" }, "Make a key for a script that calls " + org.name + "'s API as itself."))));
}

// reveal shows a secret just made, which the API never shows again, with a button that copies it.
function reveal(made) {
  const code = h("code", {}, made.secret);
  const copy = h("button", { class: "btn btn-sm", type: "button", onclick: () => navigator.clipboard.writeText(made.secret)
    .then(() => { copy.textContent = "Copied"; }).catch(() => getSelection().selectAllChildren(code)) }, icon("copy"), "Copy");
  return banner("success", "The secret of " + made.apiKey.name, h("span", {}, "Put it in the script's own secret store now: it is shown here and never again. Scripts send it as ",
    h("code", {}, "Authorization: Bearer …"), ". ", code), copy);
}

// makeKey asks for a key's name, its roles and when its first secret expires, and makes it.
function makeKey(path, refresh) {
  const name = input({ name: "name", required: true, maxlength: 100, placeholder: "Deploy script" });
  const picks = roles.map((r) => h("input", { type: "checkbox", value: r.id, "aria-label": r.name }));
  const expiry = expiryPicker();
  openDialog({ title: "Make a key", action: "Make the key",
    body: [field("Name", name), h("div", { class: "field" }, h("span", { class: "field-label" }, "Roles"), h("span", { class: "field-help" }, "Only roles whose every permission you hold here yourself."),
      h("div", { class: "picks" }, picks.map((box, i) => checkbox(box, roles[i].name)))), expiry.el],
    submit: () => api("POST", path, { name: name.value, roleIds: picks.filter((c) => c.checked).map((c) => c.value), ...expiry.value() }).then(refresh) });
  name.focus();
}

// rotate asks when a key's new secret expires, and adds it beside the one that works.
function rotate(keyPath, k, refresh) {
  const expiry = expiryPicker();
  openDialog({ title: "Rotate " + k.name, action: "Rotate", body: [h("p", {}, "A second secret works beside the first until you revoke the old one."), expiry.el],
    submit: () => api("POST", keyPath + "/secrets", expiry.value()).then(refresh) });
}
