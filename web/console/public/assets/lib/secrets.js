import { api } from "./api.js";
import { openDialog } from "./dialog.js";
import { h } from "./dom.js";
import { field, input, selectBox, textarea } from "./forms.js";
import { whenExactly } from "./format.js";
import { icon } from "./icons.js";
import { popover } from "./menu.js";
import { render } from "./router.js";

// secretsSections lists the secrets at path, an organization's or a repository's, and who receives each, with what
// changes them: Add a secret, in actions when the page gives its heading's actions, otherwise above the table, and each
// row's menu, which changes, grants, revokes and deletes it. The values are never shown: the API keeps them sealed for
// the jobs they are granted to. A viewer who manages the organization's grants grants them to its members, its API
// keys, its agents and the jobs of repos.
export async function secretsSections(org, path, grants, fail, repos, actions) {
  const item = (s) => "/orgs/" + org.slug + "/secrets/" + s.id;
  const list = (await api("GET", path)).secrets;
  const [access, members, keys, agents, jobs] = await Promise.all([
    Promise.all(list.map((s) => api("GET", item(s) + "/access").then((d) => d.access))),
    grants ? api("GET", "/orgs/" + org.slug + "/members").then((d) => d.members) : [],
    grants ? api("GET", "/orgs/" + org.slug + "/api-keys").then((d) => d.apiKeys) : [],
    grants ? api("GET", "/orgs/" + org.slug + "/agents").then((d) => d.agents) : [],
    grants ? Promise.all(repos.map((r) => api("GET", "/orgs/" + org.slug + "/repos/" + r.slug + "/jobs").then((d) => d.jobs.map((j) => ({ ...j, repo: r }))))).then((all) => all.flat()) : [],
  ]);
  const ifMatch = (s) => ({ "If-Match": '"' + s.version + '"' });
  const kinds = { api_key: "API key", job: "Job", agent: "Agent" };
  const add = h("button", { class: "btn btn-primary", type: "button", onclick: () => addSecret(path) }, icon("plus"), "Add a secret");
  if (actions) actions.replaceChildren(add);
  const menu = (s, i) => {
    const button = h("button", { class: "icon-btn icon-btn-ghost icon-btn-sm", type: "button", "aria-haspopup": "menu", "aria-label": "More for " + s.name, title: "More for " + s.name }, icon("ellipsis"));
    const entry = (word, glyph, run, danger) => h("button", { class: "menu-item" + (danger ? " menu-item-danger" : ""), type: "button", role: "menuitem", onclick: run }, icon(glyph), h("span", {}, word));
    button.addEventListener("click", () => popover(button, "end", (close) => [
      entry("Change", "pencil", () => { close(); changeSecret(item(s), s, ifMatch(s)); }),
      grants ? entry("Grant to…", "key-round", () => { close(); grantSecret(item(s), s, { members, keys, agents, jobs }); }) : null,
      grants ? access[i].map((a) => entry("Revoke " + a.displayName, "x", () => { close(); api("DELETE", item(s) + "/access/" + a.principalId).then(render).catch(fail); })) : null,
      h("hr", { class: "menu-divider" }),
      entry("Delete", "trash-2", () => {
        close();
        openDialog({ title: "Delete " + s.name + "?", action: "Delete", danger: true, body: h("p", {}, "Jobs that name it get no value from their next run on."),
          submit: () => api("DELETE", item(s), undefined, ifMatch(s)).then(render) });
      }, true),
    ]));
    return button;
  };
  const row = (s, i) => h("tr", {},
    h("td", {}, h("div", { class: "row-text" }, h("code", { class: "row-title" }, s.name), h("span", { class: "row-meta" }, "ID ", h("code", {}, s.id)))),
    h("td", {}, access[i].length ? access[i].map((a) => h("span", { class: "tag tone-neutral", title: kinds[a.kind] || "Member" }, a.displayName)) : h("span", { class: "cell-muted" }, "Nobody granted on it alone")),
    h("td", { class: "cell-muted" }, whenExactly(s.updatedAt)),
    h("td", { class: "cell-actions" }, menu(s, i)));
  return [actions ? null : h("div", { class: "filters" }, h("span", { class: "filters-space" }), add),
    h("p", { class: "field-help" }, "Values are sealed with this installation's secrets key as they arrive and never shown again. A job receives a secret that ", h("code", {}, "/cobranch.yaml"),
      " names by ID when it holds Secret reader on the secret, or on the repository that keeps it. A job becomes a principal you can grant to when it first queues a run."),
    list.length ? h("table", { class: "table" }, h("thead", {}, h("tr", {}, h("th", {}, "Secret"), h("th", {}, "Granted on it to"), h("th", {}, "Changed"), h("th", { "aria-label": "Actions" }))),
      h("tbody", {}, list.map(row)))
      : h("div", { class: "card" }, h("div", { class: "empty" }, h("div", { class: "empty-icon" }, icon("key-round")), h("h2", { class: "empty-title" }, "No secrets here yet"),
        h("p", { class: "empty-description" }, "Add a token or a password that jobs need, and grant it to them.")))].filter(Boolean);
}

// addSecret asks for a secret's name and value, and adds it at path.
function addSecret(path) {
  const name = input({ name: "name", required: true, maxlength: 100, placeholder: "npm-token" });
  const value = textarea({ name: "value", required: true, rows: 3 });
  openDialog({ title: "Add a secret", action: "Add",
    body: [field("Name", name, "Unique here; jobs name the secret by its ID, so renaming it breaks nothing."), field("Value", value, "At most 64 KiB. Never shown again.")],
    submit: () => api("POST", path, { name: name.value, value: value.value }).then(render) });
  name.focus();
}

// changeSecret renames a secret, and replaces its value when one is given, from the version shown.
function changeSecret(item, s, ifMatch) {
  const name = input({ name: "name", required: true, maxlength: 100, value: s.name });
  const value = textarea({ name: "value", rows: 3, placeholder: "Leave empty to keep the value it has" });
  openDialog({ title: "Change " + s.name, action: "Save", body: [field("Name", name), field("New value", value)],
    submit: () => api("PUT", item, { name: name.value, value: value.value || undefined }, ifMatch).then(render) });
}

// grantSecret grants Secret reader on the secret alone to a job, a member, an API key or an agent.
function grantSecret(item, s, { members, keys, agents, jobs }) {
  const principal = h("select", { name: "principalId", required: true },
    jobs.length ? h("optgroup", { label: "Jobs" }, jobs.map((j) => h("option", { value: j.principalId }, j.repo.slug + " / " + j.displayName))) : null,
    h("optgroup", { label: "Members" }, members.map((m) => h("option", { value: m.userId }, m.displayName))),
    keys.length ? h("optgroup", { label: "API keys" }, keys.map((k) => h("option", { value: k.id }, k.name))) : null,
    agents.length ? h("optgroup", { label: "Agents" }, agents.map((a) => h("option", { value: a.id }, a.name))) : null);
  openDialog({ title: "Grant " + s.name, action: "Grant", body: [h("p", {}, "Grants Secret reader on this secret alone. Only a secret you may read yourself."), field("To", selectBox(principal))],
    submit: () => api("POST", item + "/access", { principalId: principal.value }).then(render) });
}
