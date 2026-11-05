import { api } from "../../../lib/api.js";
import { openDialog } from "../../../lib/dialog.js";
import { h } from "../../../lib/dom.js";
import { checkbox, copyField, failure, field, input, selectBox } from "../../../lib/forms.js";
import { ago, plural, when } from "../../../lib/format.js";
import { icon } from "../../../lib/icons.js";
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

// newest is the latest of the times at picks from a key's secrets, or null when none has one.
const newest = (k, at) => k.secrets.map(at).filter(Boolean).sort().pop() || null;

// showApiKeys lists the organization's API keys, each with its roles, when it was made and last used, and, for a viewer
// with org.principals.manage, Rotate and Revoke, or Delete once no secret of it works. made is a key just made or
// rotated, whose secret this draw shows once, as the API never shows it again.
async function showApiKeys(org, main, can, actions, made = null) {
  const manage = can("org.principals.manage");
  const error = failure();
  const refresh = (created) => showApiKeys(org, main, can, actions, created || null);
  const path = "/orgs/" + org.slug + "/api-keys";
  const keys = (await api("GET", path)).apiKeys;
  actions.replaceChildren(manage ? h("button", { class: "btn btn-primary", type: "button", onclick: () => makeKey(path, refresh) }, icon("plus"), "Make a key") : "");
  const ghost = (label, onclick, attrs = {}) => h("button", { class: "btn btn-ghost btn-sm", type: "button", onclick, ...attrs }, label);
  const row = (k) => {
    const working = k.secrets.filter(live);
    const hint = (working.at(-1) || k.secrets.at(-1) || {}).hint;
    const revoked = newest(k, (s) => s.revokedAt);
    const used = newest(k, (s) => s.lastUsedAt);
    const gone = !working.length;
    return h("tr", { class: gone ? "row-muted" : "", "data-filter": k.name.toLowerCase() },
      h("td", { class: "cell-two-lines" }, h("span", {}, k.name), hint ? h("code", {}, "cbk_…" + hint) : null),
      h("td", {}, gone ? h("span", { class: "tag" }, revoked ? "Revoked" : "Expired")
        : k.roles.length ? k.roles.map((r) => h("span", { class: "tag tone-info" }, r.name)) : h("span", { class: "cell-muted" }, "No role")),
      h("td", { class: "cell-muted" }, when(k.createdAt)),
      h("td", { class: "cell-muted" }, gone ? (revoked ? "revoked " + ago(revoked) : "expired " + ago(newest(k, (s) => s.expiresAt))) : used ? ago(used) : "never"),
      h("td", { class: "cell-actions" }, !manage ? null : gone
        ? ghost("Delete", () => openDialog({ title: "Delete " + k.name + "?", action: "Delete", danger: true, body: h("p", {}, "No secret of it works any more; deleting it takes it off this list for good."),
          submit: () => api("DELETE", path + "/" + k.id).then(() => refresh()) }))
        : [ghost("Rotate", () => rotate(path + "/" + k.id, k, refresh), { disabled: working.length >= 2, title: working.length >= 2 ? "Two secrets already work; revoke the old one first." : "A new secret beside the one that works" }),
          ghost("Revoke", () => revoke(path + "/" + k.id, k, working[0], working.length, refresh))]));
  };
  const rows = keys.map(row);
  const gone = keys.filter((k) => !k.secrets.some(live)).length;
  const count = plural(keys.length, "key") + (gone ? " · " + gone + " revoked" : "");
  const counted = h("span", { class: "filters-count" }, count);
  const none = h("p", { class: "cell-muted", hidden: true }, "No key matches.");
  const table = h("table", { class: "table" }, h("thead", {}, h("tr", {}, h("th", {}, "Name"), h("th", {}, "Role"), h("th", {}, "Created"), h("th", {}, "Last used"), h("th", { "aria-label": "Actions" }))),
    h("tbody", {}, rows));
  const filter = h("input", { type: "search", placeholder: "Filter keys", "aria-label": "Filter keys", oninput: () => {
    const q = filter.value.trim().toLowerCase();
    const shown = rows.filter((r) => { r.hidden = !!q && !r.dataset.filter.includes(q); return !r.hidden; }).length;
    table.hidden = !shown;
    none.hidden = !!shown;
  } });
  main.replaceChildren(error.el, made ? reveal(made, () => refresh()) : "",
    h("header", { class: "section-header" }, h("div", { class: "section-header-text" }, h("h2", { class: "section-header-title" }, "API keys"),
      h("p", { class: "section-header-description" }, "A key acts as the organization with the permissions of its roles. Its secret is shown once, when it is made or rotated."))),
    ...(keys.length ? [h("div", { class: "filters" }, h("div", { class: "input" }, icon("search"), filter), counted), table, none]
      : [h("div", { class: "card" }, h("div", { class: "empty" }, h("div", { class: "empty-icon" }, icon("key-round")), h("h2", { class: "empty-title" }, "No API keys yet"),
        h("p", { class: "empty-description" }, "Make a key for a script that calls " + org.name + "'s API as itself.")))]));
}

// reveal announces a key just made or rotated in a Card (Tone=Success) with its secret in a Copy field, as the API
// never shows it again; done hides it.
function reveal(made, done) {
  const roleNames = made.apiKey.roles.map((r) => r.name);
  return h("section", { class: "card card-success" },
    h("header", { class: "card-header" }, icon("circle-check"), h("h2", { class: "card-title" }, "Key \"" + made.apiKey.name + "\" made. Copy its secret now: it is shown this once.")),
    h("div", { class: "card-body" }, copyField("Secret", made.secret, "Shown once. Copy it now; Cobranch keeps only its hash."),
      h("p", { class: "card-meta" }, (roleNames.length ? (roleNames.length > 1 ? "Roles " : "Role ") + roleNames.join(", ") : "No role") +
        " · scripts send it as a Bearer token; a lost secret can't be recovered, so rotate the key instead.")),
    h("footer", { class: "card-footer" }, h("button", { class: "btn btn-primary", type: "button", onclick: done }, "I copied it")));
}

// makeKey asks for a key's name, its roles and when its first secret expires, and makes it.
function makeKey(path, refresh) {
  const name = input({ name: "name", required: true, maxlength: 100, placeholder: "deploy-bot" });
  const picks = roles.map((r) => h("input", { type: "checkbox", value: r.id, "aria-label": r.name }));
  const expiry = expiryPicker();
  openDialog({ title: "Make a key", action: "Make the key",
    body: [field("Name", name), h("div", { class: "field" }, h("span", { class: "field-label" }, "Roles"), h("span", { class: "field-help" }, "Only roles whose every permission you hold here yourself."),
      picks.map((box, i) => checkbox(box, roles[i].name))), expiry.el],
    submit: () => api("POST", path, { name: name.value, roleIds: picks.filter((c) => c.checked).map((c) => c.value), ...expiry.value() }).then(refresh) });
  name.focus();
}

// rotate asks when a key's new secret expires, and adds it beside the one that works.
function rotate(keyPath, k, refresh) {
  const expiry = expiryPicker();
  openDialog({ title: "Rotate " + k.name, action: "Rotate", body: [h("p", {}, "A second secret works beside the first until you revoke the old one."), expiry.el],
    submit: () => api("POST", keyPath + "/secrets", expiry.value()).then(refresh) });
}

// revoke asks before it revokes the oldest working secret of a key: the old one after a rotation, or the key's last.
function revoke(keyPath, k, s, working, refresh) {
  openDialog({ title: "Revoke …" + s.hint + "?", action: "Revoke", danger: true,
    body: h("p", {}, working > 1 ? "Scripts still on the old secret stop working; those on the new one carry on."
      : "It is " + k.name + "'s only working secret: every script that signs in with the key stops working at once."),
    submit: () => api("DELETE", keyPath + "/secrets/" + s.id).then(() => refresh()) });
}
