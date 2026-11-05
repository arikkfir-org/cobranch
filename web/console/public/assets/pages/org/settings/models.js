import { api } from "../../../lib/api.js";
import { h } from "../../../lib/dom.js";
import { when } from "../../../lib/format.js";
import { icon } from "../../../lib/icons.js";
import { base } from "../../../lib/state.js";

// Models, until slice E draws this tab to its screen: the model providers, as Settings' General listed them before.
export const tab = { label: "Models", order: 60 };
export const allowed = (can) => can("org.manage");
export const view = "organization.settings";
export default async function show({ org, main, can }) {
  const error = h("p", { class: "alert", hidden: true });
  const fail = (err) => { error.textContent = err.message; error.hidden = false; };
  main.replaceChildren(error, await providersSection(org, can, fail, () => show({ org, main, can })));
}

// providersSection lists the model providers the organization added, each with the secret its API key is in, and the
// forms that add one the installation knows, name another key, and remove one no agent runs on. A key is one of the
// organization's own secrets, which a viewer who manages secrets picks; render draws the page again.
async function providersSection(org, can, fail, render) {
  const path = "/orgs/" + org.slug + "/model-providers";
  const [{ modelProviders: list, available }, keys] = await Promise.all([api("GET", path),
    can("secrets.manage") ? api("GET", "/orgs/" + org.slug + "/secrets").then((d) => d.secrets) : []]);
  const item = (name) => path + "/" + encodeURIComponent(name);
  const keySelect = (selected) => h("select", { required: true }, keys.map((s) => h("option", { value: s.id, selected: s.id === selected }, s.name)));
  const row = (p) => {
    const tr = h("tr", {}, h("td", {}, h("code", {}, p.name)),
      h("td", {}, p.broken ? h("span", { class: "badge tone-danger" }, icon("circle-x"), "Its key is gone") : p.secretName ? h("code", {}, p.secretName) : h("span", { class: "muted" }, "A secret you don't manage")),
      h("td", {}, p.createdBy ? p.createdBy.displayName : h("span", { class: "muted" }, "Someone since gone"), h("p", {}, when(p.updatedAt))),
      h("td", { class: "actions" },
        keys.length ? h("button", { class: "button small", type: "button", onclick: () => {
          const key = keySelect(p.secretId);
          const editor = h("tr", {}, h("td", { colspan: 4 }, h("form", { class: "inline with-role", onsubmit: (e) => {
            e.preventDefault();
            api("PUT", item(p.name), { secretId: key.value }, { "If-Match": `"${p.version}"` }).then(render).catch(fail);
          } }, h("label", {}, "Key", key), h("button", { class: "button primary", type: "submit" }, "Save"), " ",
            h("button", { class: "button", type: "button", onclick: () => editor.replaceWith(tr) }, "Cancel"))));
          tr.replaceWith(editor);
        } }, "Change key") : null, " ",
        h("button", { class: "button small danger", type: "button", title: "Remove " + p.name,
          onclick: () => confirm("Remove " + p.name + "? Agents on it must move to another provider first.") && api("DELETE", item(p.name), undefined, { "If-Match": `"${p.version}"` }).then(render).catch(fail) }, "Remove")));
    return tr;
  };
  const left = available.filter((name) => !list.some((p) => p.name === name));
  const section = h("section", { class: "panel" }, h("h3", {}, "Model providers"),
    h("p", {}, "The providers this organization's agents run on, each with its API key, kept as one of the organization's secrets. An agent's tasks run only while the agent may read its provider's key."),
    list.length ? h("table", {}, h("thead", {}, h("tr", {}, h("th", {}, "Provider"), h("th", {}, "Key"), h("th", {}, "Set by"), h("th", {}))), h("tbody", {}, list.map(row)))
      : h("p", {}, "No providers yet."));
  if (!left.length) return section;
  if (!keys.length) {
    section.append(h("p", { class: "muted" }, can("secrets.manage") ? ["Add a provider's API key as one of the organization's ", h("a", { href: base + org.slug + "/settings/secrets", "data-nav": "" }, "secrets"), " to add the provider."]
      : "Adding a provider takes picking its key among the organization's secrets, which only those who manage them see."));
    return section;
  }
  const provider = h("select", { required: true }, left.map((name) => h("option", { value: name }, name)));
  const key = keySelect();
  section.append(h("form", { class: "inline with-role", onsubmit: (e) => {
    e.preventDefault();
    api("PUT", item(provider.value), { secretId: key.value }, { "If-None-Match": "*" }).then(render).catch(fail);
  } }, h("label", {}, "Provider", provider), h("label", {}, "Key", key), h("button", { class: "button primary", type: "submit" }, "Add")));
  return section;
}
