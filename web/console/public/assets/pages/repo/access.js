import { api } from "../../lib/api.js";
import { h } from "../../lib/dom.js";
import { visibilities } from "../../lib/organization.js";
import { render } from "../../lib/router.js";
import { base, me } from "../../lib/state.js";

export const nav = { label: "Access", order: 40 };
export default ({ org, repo, path, orgCan, fail }) => repositoryAccess(org, repo, path, orgCan("org.grants.manage"), fail);

// repositoryAccess lists who holds roles on repo, and, for a viewer who manages the organization's grants, grants
// and revokes them: roles of the repository, to the organization's members and API keys.
async function repositoryAccess(org, repo, path, manage, fail) {
  const [access, repoRoles, members, keys] = await Promise.all([
    api("GET", path + "/access").then((d) => d.access),
    api("GET", path + "/roles").then((d) => d.roles),
    manage ? api("GET", "/orgs/" + org.slug + "/members").then((d) => d.members) : [],
    manage ? api("GET", "/orgs/" + org.slug + "/api-keys").then((d) => d.apiKeys) : [],
  ]);
  const chip = (a, r) => h("span", { class: "chip" }, r.name, manage
    ? h("button", { type: "button", title: "Revoke " + r.name, onclick: () => api("DELETE", path + "/access/" + a.principalId + "/roles/" + r.id).then(render).catch(fail) }, "×") : null);
  const row = (a) => h("tr", {}, h("td", {}, a.displayName, a.kind === "api_key" ? h("span", { class: "chip" }, "API key") : null, a.principalId === me.id ? h("span", { class: "chip" }, "you") : null),
    h("td", {}, a.roles.map((r) => chip(a, r))));
  const sections = [h("section", { class: "panel" }, h("p", {}, visibilities[repo.visibility].title + " Organization roles that hold repository permissions reach every repository of " + org.name + " too: see ",
      h("a", { href: base + org.slug + "/roles", "data-nav": "" }, "its roles"), "."),
    access.length ? h("table", {}, h("thead", {}, h("tr", {}, h("th", {}, "Who"), h("th", {}, "Roles on this repository"))), h("tbody", {}, access.map(row)))
      : h("p", {}, "Nobody holds a role on it."))];
  if (manage) {
    const principal = h("select", { name: "principalId", required: true },
      h("optgroup", { label: "Members" }, members.map((m) => h("option", { value: m.userId }, m.displayName))),
      keys.length ? h("optgroup", { label: "API keys" }, keys.map((k) => h("option", { value: k.id }, k.name))) : null);
    const role = h("select", { name: "roleId" }, repoRoles.map((r) => h("option", { value: r.id }, r.name)));
    sections.push(h("section", { class: "panel" }, h("h3", {}, "Grant a role"), h("p", {}, "Only roles whose every permission you hold on this repository yourself."),
      h("form", { class: "inline with-role", onsubmit: (e) => { e.preventDefault(); api("POST", path + "/access/" + principal.value + "/roles", { roleId: role.value }).then(render).catch(fail); } },
        h("label", {}, "Who", principal), h("label", {}, "Role", role), h("button", { class: "button primary", type: "submit" }, "Grant"))));
  }
  sections.push(h("section", { class: "panel" }, h("h3", {}, "Repository roles"),
    h("table", {}, h("tbody", {}, repoRoles.map((r) => h("tr", {}, h("td", {}, r.name, r.builtIn ? h("span", { class: "chip" }, "built in") : null, r.description ? h("p", {}, r.description) : null),
      h("td", {}, h("div", { class: "perms" }, r.permissions.map((p) => h("code", {}, p))))))))));
  return sections;
}
