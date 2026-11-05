import { api } from "../../lib/api.js";
import { h } from "../../lib/dom.js";
import { permissions, roles, setRoles } from "../../lib/state.js";

export const nav = { label: "Roles", order: 30, under: "settings" };
export const crumbs = ({ root }) => [{ label: "Settings", href: root + "/settings" }, { label: "Roles" }];
export default ({ org, main, can }) => showRoles(org, main, can);

// The roles page: what each role holds, and, with org.grants.manage, the organization's own roles to define,
// change and delete.
function showRoles(org, main, can) {
  const manage = can("org.grants.manage");
  const error = h("p", { class: "alert", hidden: true });
  const fail = (err) => { error.textContent = err.message; error.hidden = false; };
  const refresh = () => api("GET", "/orgs/" + org.slug + "/roles").then((d) => { setRoles(d.roles); showRoles(org, main, can); }).catch(fail);
  // roleForm edits a role's definition, a new one when role is null, and saves it through save.
  const roleForm = (role, save, cancel) => {
    const name = h("input", { name: "name", required: true, maxlength: 50, value: role ? role.name : "", placeholder: "Release manager" });
    const description = h("input", { name: "description", maxlength: 500, value: role ? role.description : "" });
    const picks = permissions.map((p) => h("label", {}, h("input", { type: "checkbox", value: p, checked: !!role && role.permissions.includes(p) }), h("code", {}, p)));
    return h("form", { onsubmit: (e) => {
      e.preventDefault();
      const chosen = picks.map((l) => l.firstChild).filter((c) => c.checked).map((c) => c.value);
      save({ name: name.value, description: description.value, permissions: chosen }).then(refresh).catch(fail);
    } },
      h("label", {}, "Name", name), h("label", {}, "Description", description),
      h("label", {}, "Permissions", h("span", {}, "Only what you hold here yourself."), h("div", { class: "picks" }, picks)),
      h("div", {}, h("button", { class: "button primary", type: "submit" }, role ? "Save" : "Define"), " ", cancel ? h("button", { class: "button", type: "button", onclick: cancel }, "Cancel") : null));
  };
  const rows = h("tbody", {});
  const roleRow = (r) => {
    const row = h("tr", {}, h("td", {}, r.name, r.builtIn ? h("span", { class: "chip" }, "built in") : null, r.description ? h("p", {}, r.description) : null),
      h("td", {}, h("div", { class: "perms" }, r.permissions.map((p) => h("code", {}, p)))),
      h("td", { class: "actions" }, manage && !r.builtIn ? [
        h("button", { class: "button small", type: "button", onclick: () => {
          const editor = h("tr", {}, h("td", { colspan: 3 }, roleForm(r, (body) => api("PATCH", "/orgs/" + org.slug + "/roles/" + r.id, body), () => editor.replaceWith(row))));
          row.replaceWith(editor);
        } }, "Edit"), " ",
        h("button", { class: "button small danger", type: "button", onclick: () => api("DELETE", "/orgs/" + org.slug + "/roles/" + r.id).then(refresh).catch(fail) }, "Delete"),
      ] : null));
    return row;
  };
  rows.append(...roles.map(roleRow));
  const sections = [error,
    h("section", { class: "panel" }, h("table", {}, h("thead", {}, h("tr", {}, h("th", {}, "Role"), h("th", {}, "Permissions"), h("th", {}))), rows))];
  if (manage) sections.push(h("section", { class: "panel" }, h("h3", {}, "Define a role"), h("p", {}, "A role of this organization alone, to grant beside the built-in ones. Changing it changes every grant of it."),
    roleForm(null, (body) => api("POST", "/orgs/" + org.slug + "/roles", body), null)));
  main.replaceChildren(...sections);
}
