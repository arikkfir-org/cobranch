import { api } from "./api.js";
import { h } from "./dom.js";
import { whenExactly } from "./format.js";
import { render } from "./router.js";

// secretsSections lists the secrets at path, an organization's or a repository's, who receives each, and the forms
// that add, change, delete and grant them. The values are never shown: the API keeps them sealed for the jobs they
// are granted to. A viewer who manages the organization's grants grants them to its members, its API keys, its agents
// and the jobs of repos.
export async function secretsSections(org, path, grants, fail, repos) {
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
  const kinds = { api_key: "API key", job: "job", agent: "agent" };
  const receiver = (s, a) => h("span", { class: "chip" }, a.displayName, kinds[a.kind] ? " (" + kinds[a.kind] + ")" : null, grants
    ? h("button", { type: "button", title: "Revoke " + a.displayName, onclick: () => api("DELETE", item(s) + "/access/" + a.principalId).then(render).catch(fail) }, "×") : null);
  const row = (s, i) => {
    const tr = h("tr", {}, h("td", {}, h("code", {}, s.name), h("p", {}, "ID ", h("code", {}, s.id))),
      h("td", {}, access[i].length ? access[i].map((a) => receiver(s, a)) : h("span", { class: "muted" }, "nobody granted on it alone")),
      h("td", {}, whenExactly(s.updatedAt)),
      h("td", { class: "actions" },
        h("button", { class: "button small", type: "button", onclick: () => {
          const name = h("input", { name: "name", required: true, maxlength: 100, value: s.name });
          const value = h("textarea", { name: "value", rows: 3, placeholder: "Leave empty to keep the value it has" });
          const editor = h("tr", {}, h("td", { colspan: 4 }, h("form", { onsubmit: (e) => {
            e.preventDefault();
            api("PUT", item(s), { name: name.value, value: value.value || undefined }, ifMatch(s)).then(render).catch(fail);
          } }, h("label", {}, "Name", name), h("label", {}, "New value", value),
            h("div", {}, h("button", { class: "button primary", type: "submit" }, "Save"), " ", h("button", { class: "button", type: "button", onclick: () => editor.replaceWith(tr) }, "Cancel")))));
          tr.replaceWith(editor);
        } }, "Change"), " ",
        h("button", { class: "button small danger", type: "button", onclick: () => api("DELETE", item(s), undefined, ifMatch(s)).then(render).catch(fail) }, "Delete")));
    return tr;
  };
  const sections = [h("section", { class: "panel" },
    h("p", {}, "Values are sealed with this installation's secrets key as they arrive and never shown again. A job receives a secret that ", h("code", {}, "/cobranch.yaml"), " names by ID when it holds Secret reader on the secret, or on the repository that keeps it. A job becomes a principal you can grant to when it first queues a run."),
    list.length ? h("table", {}, h("thead", {}, h("tr", {}, h("th", {}, "Secret"), h("th", {}, "Granted on it to"), h("th", {}, "Changed"), h("th", {}))), h("tbody", {}, list.map(row)))
      : h("p", {}, "No secrets here yet."))];
  const name = h("input", { name: "name", required: true, maxlength: 100, placeholder: "npm-token" });
  const value = h("textarea", { name: "value", required: true, rows: 3 });
  sections.push(h("section", { class: "panel" }, h("h3", {}, "Add a secret"),
    h("form", { onsubmit: (e) => { e.preventDefault(); api("POST", path, { name: name.value, value: value.value }).then(render).catch(fail); } },
      h("label", {}, "Name", h("span", {}, "Unique here; jobs name the secret by its ID, so renaming it breaks nothing."), name),
      h("label", {}, "Value", h("span", {}, "At most 64 KiB."), value),
      h("div", {}, h("button", { class: "button primary", type: "submit" }, "Add")))));
  if (grants && list.length) {
    const secret = h("select", { name: "secret" }, list.map((s) => h("option", { value: s.id }, s.name)));
    const principal = h("select", { name: "principalId", required: true },
      jobs.length ? h("optgroup", { label: "Jobs" }, jobs.map((j) => h("option", { value: j.principalId }, j.repo.slug + " / " + j.displayName))) : null,
      h("optgroup", { label: "Members" }, members.map((m) => h("option", { value: m.userId }, m.displayName))),
      keys.length ? h("optgroup", { label: "API keys" }, keys.map((k) => h("option", { value: k.id }, k.name))) : null,
      agents.length ? h("optgroup", { label: "Agents" }, agents.map((a) => h("option", { value: a.id }, a.name))) : null);
    sections.push(h("section", { class: "panel" }, h("h3", {}, "Grant a secret"), h("p", {}, "Grants Secret reader on the secret alone. Only a secret you may read yourself."),
      h("form", { class: "inline with-role", onsubmit: (e) => {
        e.preventDefault();
        api("POST", "/orgs/" + org.slug + "/secrets/" + secret.value + "/access", { principalId: principal.value }).then(render).catch(fail);
      } }, h("label", {}, "Secret", secret), h("label", {}, "To", principal), h("button", { class: "button primary", type: "submit" }, "Grant"))));
  }
  return sections;
}
