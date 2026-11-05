import { api } from "../../../lib/api.js";
import { openDialog } from "../../../lib/dialog.js";
import { h } from "../../../lib/dom.js";
import { field, selectBox } from "../../../lib/forms.js";
import { since } from "../../../lib/format.js";
import { avatar, icon, machineAvatar } from "../../../lib/icons.js";
import { render } from "../../../lib/router.js";
import { me } from "../../../lib/state.js";

export const tab = { label: "Access", order: 10 };
export default ({ org, repo, path, orgCan, fail }) => showAccess(org, repo, path, orgCan("org.grants.manage"), fail);

// sectionHeader is a section's title over the line on what it holds, with its action beside them.
const sectionHeader = (title, description, action) => h("header", { class: "section-header" },
  h("div", { class: "section-header-text" }, h("h2", { class: "section-header-title" }, title), h("p", { class: "section-header-description" }, description)), action);

// whoAvatar is a principal's avatar as its kind draws it: a person's initials, an agent's sparkles, an API key's key and
// a job's cog.
const whoAvatar = (a) => a.kind === "user" || a.kind === "agent" ? avatar(a.displayName, a.kind === "agent")
  : machineAvatar(a.displayName, 20, a.kind === "api_key" ? "key-round" : "cog");

// showAccess lists every role that reaches the repository, a row each: who holds it, how (granted here, or a role at
// the organization, which reaches every repository of it) and since when. A viewer who manages the organization's
// grants grants roles here, and revokes those granted here, but their own; the organization's roles change on its
// Members page.
async function showAccess(org, repo, path, manage, fail) {
  const access = (await api("GET", path + "/access")).access;
  const through = (g) => g.through === "repository" ? "Direct" : org.name + " · " + (g.role.id === org.membershipRoleId ? "default role" : g.role.name);
  const revoke = (a, g) => h("button", { class: "btn btn-ghost btn-sm", type: "button",
    onclick: () => api("DELETE", path + "/access/" + a.principalId + "/roles/" + g.role.id).then(render).catch(fail) }, "Revoke");
  const row = (a, g) => h("tr", {},
    h("td", {}, h("span", { class: "cell-person" }, whoAvatar(a), a.displayName)),
    h("td", {}, g.role.name),
    h("td", { class: g.through === "repository" ? null : "cell-muted" }, through(g)),
    h("td", { class: "cell-muted", title: new Date(g.since).toLocaleString() }, since(g.since)),
    h("td", { class: "cell-actions" }, manage && g.through === "repository" && a.principalId !== me.id ? revoke(a, g) : null));
  const grant = manage ? h("button", { class: "btn btn-primary", type: "button", onclick: () => grantRole(org, path) }, icon("plus"), "Grant role") : null;
  return [
    sectionHeader("Access", "Who holds this repository's roles: granted here, or through a role at " + org.name + ", which changes on its Members page.", grant),
    h("div", { class: "card" }, h("table", { class: "table access-table" },
      h("thead", {}, h("tr", {}, h("th", {}, "Who"), h("th", {}, "Role"), h("th", {}, "Through"), h("th", {}, "Since"), h("th", { "aria-label": "Actions" }))),
      h("tbody", {}, access.flatMap((a) => a.grants.map((g) => row(a, g)))))),
  ];
}

// grantRole asks who to grant which of the repository's roles, and grants it here: the organization's members, API
// keys and agents, and the roles whose every permission the viewer holds on the repository, which the API checks.
async function grantRole(org, path) {
  const [roles, members, keys, agents] = await Promise.all([
    api("GET", path + "/roles").then((d) => d.roles),
    api("GET", "/orgs/" + org.slug + "/members").then((d) => d.members),
    api("GET", "/orgs/" + org.slug + "/api-keys").then((d) => d.apiKeys),
    api("GET", "/orgs/" + org.slug + "/agents").then((d) => d.agents),
  ]);
  const who = h("select", { name: "principalId", required: true },
    h("optgroup", { label: "Members" }, members.map((m) => h("option", { value: m.userId }, m.displayName))),
    keys.length ? h("optgroup", { label: "API keys" }, keys.map((k) => h("option", { value: k.id }, k.name))) : null,
    agents.length ? h("optgroup", { label: "Agents" }, agents.map((a) => h("option", { value: a.id }, a.name))) : null);
  const role = h("select", { name: "roleId", required: true }, roles.map((r) => h("option", { value: r.id }, r.name)));
  openDialog({ title: "Grant a role", action: "Grant",
    body: [h("p", {}, "A grant made here is direct: it shows under Access and is revoked there."),
      field("Who", selectBox(who), "A member, API key or agent of " + org.name),
      field("Role", selectBox(role), "Only roles whose every permission you hold on this repository yourself.")],
    submit: () => api("POST", path + "/access/" + who.value + "/roles", { roleId: role.value }).then(render) });
}
