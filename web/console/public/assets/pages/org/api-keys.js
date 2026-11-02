import { api } from "../../lib/api.js";
import { h } from "../../lib/dom.js";
import { when } from "../../lib/format.js";
import { roles } from "../../lib/state.js";

export const nav = { label: "API keys", order: 40 };
export default ({ org, main, can }) => showApiKeys(org, main, can);

// expiryPicker chooses when a new secret stops working: in a year (the default), never, or on a date.
function expiryPicker() {
  const date = h("input", { type: "date", hidden: true });
  const pick = h("select", { onchange: () => { date.hidden = pick.value !== "date"; if (!date.hidden) date.required = true; } },
    h("option", { value: "year" }, "Expires in a year"), h("option", { value: "never" }, "Never expires"), h("option", { value: "date" }, "Expires on a date…"));
  const el = h("label", {}, "Expiry", pick, date);
  const value = () => pick.value === "never" ? { neverExpires: true } : pick.value === "date" ? { expiresAt: new Date(date.value + "T00:00:00").toISOString() } : {};
  return { el, value };
}

// showApiKeys renders the organization's API keys; shown is a secret just made, which this render shows once.
async function showApiKeys(org, main, can, shown = null) {
  const manage = can("org.principals.manage");
  const error = h("p", { class: "alert", hidden: true });
  const fail = (err) => { error.textContent = err.message; error.hidden = false; };
  const refresh = (created) => showApiKeys(org, main, can, created || null).catch(fail);
  // reveal shows a new secret, which the API never shows again.
  const reveal = (created) => {
    const code = h("code", {}, created.secret);
    const copy = h("button", { class: "button small", type: "button", onclick: () => {
      navigator.clipboard.writeText(created.secret).then(() => { copy.textContent = "Copied"; }).catch(() => { getSelection().selectAllChildren(code); });
    } }, "Copy");
    return h("section", { class: "panel notice" }, h("h3", {}, "The secret of ", created.apiKey.name),
      h("p", {}, "Put it in the script's own secret store now: it is shown here and never again. Scripts send it as ", h("code", {}, "Authorization: Bearer …"), "."),
      h("div", { class: "row" }, code, copy));
  };
  const state = (s) => {
    const now = Date.now();
    if (s.revokedAt) return "revoked " + when(s.revokedAt);
    if (s.expiresAt && new Date(s.expiresAt) <= now) return "expired " + when(s.expiresAt);
    return (s.expiresAt ? "expires " + when(s.expiresAt) : "never expires") + (s.lastUsedAt ? ", last used " + when(s.lastUsedAt) : ", never used");
  };
  const keys = (await api("GET", "/orgs/" + org.slug + "/api-keys")).apiKeys;
  const secretRow = (k, s) => h("div", { class: "row" }, h("span", {}, h("code", {}, "…" + s.hint), " ", h("span", { class: "muted" }, state(s))),
    manage && !s.revokedAt ? h("button", { class: "button small danger", type: "button", onclick: () => api("DELETE", "/orgs/" + org.slug + "/api-keys/" + k.id + "/secrets/" + s.id).then(() => refresh()).catch(fail) }, "Revoke") : null);
  const keyRow = (k) => {
    const live = k.secrets.filter((s) => !s.revokedAt && !(s.expiresAt && new Date(s.expiresAt) <= Date.now())).length;
    const row = h("tr", {}, h("td", {}, k.name, h("p", {}, "made " + when(k.createdAt), live ? "" : " · no live secret: rotate it")),
      h("td", {}, k.roles.map((r) => h("span", { class: "chip" }, r.name))),
      h("td", {}, k.secrets.map((s) => secretRow(k, s))),
      h("td", { class: "actions" }, manage ? [
        h("button", { class: "button small", type: "button", disabled: live >= 2, title: live >= 2 ? "Two secrets already work; revoke one first." : "A new secret beside the old one", onclick: () => {
          const picker = expiryPicker();
          const editor = h("tr", {}, h("td", { colspan: 4 }, h("form", { class: "inline", onsubmit: (e) => { e.preventDefault(); api("POST", "/orgs/" + org.slug + "/api-keys/" + k.id + "/secrets", picker.value()).then(refresh).catch(fail); } },
            picker.el, h("div", {}, h("button", { class: "button primary", type: "submit" }, "Rotate"), " ", h("button", { class: "button", type: "button", onclick: () => editor.replaceWith(row) }, "Cancel")))));
          row.replaceWith(editor);
        } }, "Rotate"), " ",
        h("button", { class: "button small danger", type: "button", onclick: () => api("DELETE", "/orgs/" + org.slug + "/api-keys/" + k.id).then(() => refresh()).catch(fail) }, "Delete"),
      ] : null));
    return row;
  };
  const sections = [h("h1", {}, "API keys"), error];
  if (shown) sections.push(reveal(shown));
  sections.push(h("section", { class: "panel" }, h("p", {}, "A key is a principal of this organization that a script acts as, with roles of its own; a key never borrows a person's access. Rotating a key adds a second secret, so the script changes over without failing; then revoke the old one."),
    keys.length ? h("table", {}, h("thead", {}, h("tr", {}, h("th", {}, "Key"), h("th", {}, "Roles"), h("th", {}, "Secrets"), h("th", {}))), h("tbody", {}, keys.map(keyRow))) : h("p", {}, "No API keys yet.")));
  if (manage) {
    const name = h("input", { name: "name", required: true, maxlength: 100, placeholder: "Deploy script" });
    const picks = roles.map((r) => h("label", {}, h("input", { type: "checkbox", value: r.id }), r.name));
    const picker = expiryPicker();
    sections.push(h("section", { class: "panel" }, h("h3", {}, "Make a key"), h("p", {}, "Only roles whose every permission you hold here yourself."),
      h("form", { onsubmit: (e) => {
        e.preventDefault();
        const roleIds = picks.map((l) => l.firstChild).filter((c) => c.checked).map((c) => c.value);
        api("POST", "/orgs/" + org.slug + "/api-keys", { name: name.value, roleIds, ...picker.value() }).then(refresh).catch(fail);
      } }, h("label", {}, "Name", name), h("label", {}, "Roles", h("div", { class: "picks" }, picks)), picker.el,
        h("div", {}, h("button", { class: "button primary", type: "submit" }, "Make the key")))));
  }
  main.replaceChildren(...sections);
}
