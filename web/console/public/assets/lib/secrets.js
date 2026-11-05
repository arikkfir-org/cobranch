import { api } from "./api.js";
import { openDialog } from "./dialog.js";
import { h } from "./dom.js";
import { field, input, selectBox, textarea } from "./forms.js";
import { ago, plural } from "./format.js";
import { icon } from "./icons.js";
import { render } from "./router.js";

// secretsSections lists the secrets at path, an organization's or a repository's, who may read each and when it last
// changed, with Add secret, in actions when the page gives its heading's actions, otherwise beside the filter, and
// each row's Change and Delete. The values are never shown: the API keeps them sealed for those granted them. A viewer
// who manages the organization's grants grants them, in Change, to its members, its API keys, its agents and the jobs
// of repos.
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
  const names = new Map([...members.map((m) => [m.userId, m.displayName]), ...agents.map((a) => [a.id, a.name + " (agent)"]), ...keys.map((k) => [k.id, k.name + " (API key)"])]);
  const add = h("button", { class: "btn btn-primary", type: "button", onclick: () => addSecret(path) }, icon("plus"), "Add secret");
  if (actions) actions.replaceChildren(add);
  const ghost = (label, onclick) => h("button", { class: "btn btn-ghost btn-sm", type: "button", onclick }, label);
  const row = (s, i) => h("tr", { "data-filter": s.name.toLowerCase() },
    h("td", { class: "cell-two-lines" }, h("code", {}, s.name), h("span", {}, "Value set")),
    h("td", {}, access[i].length ? access[i].map((a) => h("span", { class: "tag", title: kinds[a.kind] || "Member" }, a.displayName))
      : h("span", { class: "tag tone-warning", title: "Only those who read every secret here, by a role, read it" }, "Nobody yet")),
    h("td", { class: "cell-muted" }, ago(s.updatedAt) + (names.has(s.updatedBy) ? " by " + names.get(s.updatedBy) : "")),
    h("td", { class: "cell-actions" }, ghost("Change", () => changeSecret(item(s), s, ifMatch(s), grants ? { access: access[i], members, keys, agents, jobs } : null, fail)),
      ghost("Delete", () => openDialog({ title: "Delete " + s.name + "?", action: "Delete", danger: true, body: h("p", {}, "Jobs and agents that name it get no value from their next run on."),
        submit: () => api("DELETE", item(s), undefined, ifMatch(s)).then(render) }))));
  const rows = list.map(row);
  const table = h("table", { class: "table" }, h("thead", {}, h("tr", {}, h("th", {}, "Name"), h("th", {}, "Readable by"), h("th", {}, "Changed"), h("th", { "aria-label": "Actions" }))),
    h("tbody", {}, rows));
  const none = h("p", { class: "cell-muted", hidden: true }, "No secret matches.");
  const filter = h("input", { type: "search", placeholder: "Filter secrets", "aria-label": "Filter secrets", oninput: () => {
    const q = filter.value.trim().toLowerCase();
    const shown = rows.filter((r) => { r.hidden = !!q && !r.dataset.filter.includes(q); return !r.hidden; }).length;
    table.hidden = !shown;
    none.hidden = !!shown;
  } });
  return [
    h("header", { class: "section-header" }, h("div", { class: "section-header-text" }, h("h2", { class: "section-header-title" }, "Secrets"),
      h("p", { class: "section-header-description" }, "Values jobs and agents read by ID, from ", h("code", {}, "/cobranch.yaml"), ". A value is sealed as it arrives, never shown again; change it to replace it.")),
      actions ? null : add),
    ...(list.length ? [h("div", { class: "filters" }, h("div", { class: "input" }, icon("search"), filter), h("span", { class: "filters-count" }, plural(list.length, "secret"))), table, none]
      : [h("div", { class: "card" }, h("div", { class: "empty" }, h("div", { class: "empty-icon" }, icon("key-round")), h("h2", { class: "empty-title" }, "No secrets here yet"),
        h("p", { class: "empty-description" }, "Add a token or a password that jobs and agents need, and grant it to them.")))])];
}

// addSecret asks for a secret's name and value, and adds it at path.
function addSecret(path) {
  const name = input({ name: "name", required: true, maxlength: 100, placeholder: "NPM_TOKEN" });
  const value = textarea({ name: "value", required: true, rows: 3 });
  openDialog({ title: "Add secret", action: "Add",
    body: [field("Name", name, "Unique here; jobs name the secret by its ID, so renaming it breaks nothing."), field("Value", value, "At most 64 KiB. Never shown again.")],
    submit: () => api("POST", path, { name: name.value, value: value.value }).then(render) });
  name.focus();
}

// changeSecret renames a secret and replaces its value when one is given, from the version shown; for a viewer who
// manages grants (who), it also lists who reads it alone, each with Revoke, and grants it to one more.
function changeSecret(item, s, ifMatch, who, fail) {
  const name = input({ name: "name", required: true, maxlength: 100, value: s.name });
  const value = textarea({ name: "value", rows: 3, placeholder: "Leave empty to keep the value it has" });
  let principal = null;
  const readers = [];
  if (who) {
    const { access, members, keys, agents, jobs } = who;
    principal = h("select", { name: "principalId" }, h("option", { value: "" }, "Nobody more"),
      jobs.length ? h("optgroup", { label: "Jobs" }, jobs.map((j) => h("option", { value: j.principalId }, j.repo.slug + " / " + j.displayName))) : null,
      h("optgroup", { label: "Members" }, members.map((m) => h("option", { value: m.userId }, m.displayName))),
      keys.length ? h("optgroup", { label: "API keys" }, keys.map((k) => h("option", { value: k.id }, k.name))) : null,
      agents.length ? h("optgroup", { label: "Agents" }, agents.map((a) => h("option", { value: a.id }, a.name))) : null);
    readers.push(h("div", { class: "field" }, h("span", { class: "field-label" }, "Readable by"),
      access.length ? access.map((a) => h("div", { class: "card-row" }, h("div", { class: "card-row-text" }, h("span", { class: "card-row-title" }, a.displayName)),
        h("div", { class: "card-row-actions" }, h("button", { class: "btn btn-ghost btn-sm", type: "button",
          onclick: (e) => api("DELETE", item + "/access/" + a.principalId).then(() => e.target.closest(".card-row").remove()).catch(fail) }, "Revoke"))))
        : h("span", { class: "field-help" }, "Nobody on this secret alone; those who read every secret here, by a role, read it too.")),
    field("Grant to", selectBox(principal), "Secret reader on this secret alone. Only a secret you may read yourself."));
  }
  openDialog({ title: "Change " + s.name, action: "Save", body: [field("Name", name), field("New value", value), ...readers],
    submit: async () => {
      await api("PUT", item, { name: name.value, value: value.value || undefined }, ifMatch);
      if (principal && principal.value) await api("POST", item + "/access", { principalId: principal.value });
      await render();
    } });
}
