import { api } from "../../../lib/api.js";
import { openDialog } from "../../../lib/dialog.js";
import { h } from "../../../lib/dom.js";
import { checkbox, failure, field, input } from "../../../lib/forms.js";
import { icon } from "../../../lib/icons.js";
import { popover } from "../../../lib/menu.js";
import { permissions, roles, setRoles } from "../../../lib/state.js";

export const tab = { label: "Roles", order: 20 };
export const view = "organization.roles";
export default ({ org, main, can, actions }) => showRoles(org, main, can, actions);

// showRoles lists what each role holds, built in or the organization's own; a viewer with org.grants.manage defines,
// changes and deletes its own.
function showRoles(org, main, can, actions) {
  const manage = can("org.grants.manage");
  const error = failure();
  const refresh = () => api("GET", "/orgs/" + org.slug + "/roles").then((d) => { setRoles(d.roles); showRoles(org, main, can, actions); });
  actions.replaceChildren(manage ? h("button", { class: "btn btn-primary", type: "button",
    onclick: () => roleDialog(null, (body) => api("POST", "/orgs/" + org.slug + "/roles", body).then(refresh)) }, icon("plus"), "Define a role") : "");
  const menu = (r) => {
    const button = h("button", { class: "icon-btn icon-btn-ghost icon-btn-sm", type: "button", "aria-haspopup": "menu", "aria-label": "More for " + r.name, title: "More for " + r.name }, icon("ellipsis"));
    button.addEventListener("click", () => popover(button, "end", (close) => [
      h("button", { class: "menu-item", type: "button", role: "menuitem", onclick: () => {
        close();
        roleDialog(r, (body) => api("PATCH", "/orgs/" + org.slug + "/roles/" + r.id, body).then(refresh));
      } }, icon("pencil"), h("span", {}, "Edit")),
      h("hr", { class: "menu-divider" }),
      h("button", { class: "menu-item menu-item-danger", type: "button", role: "menuitem", onclick: () => {
        close();
        openDialog({ title: "Delete " + r.name + "?", action: "Delete", danger: true,
          body: h("p", {}, "Everyone and everything granted " + r.name + " loses what it holds. Built-in roles stay."),
          submit: () => api("DELETE", "/orgs/" + org.slug + "/roles/" + r.id).then(refresh) });
      } }, icon("trash-2"), h("span", {}, "Delete")),
    ]));
    return button;
  };
  const row = (r) => h("tr", {},
    h("td", {}, h("div", { class: "row-text" }, h("span", { class: "row-title" }, r.name, r.builtIn ? [" ", h("span", { class: "tag tone-neutral" }, "Built in")] : null),
      r.description ? h("span", { class: "row-meta" }, r.description) : null)),
    h("td", { class: "cell-muted cell-mono" }, r.permissions.length ? r.permissions.join(", ") : "Nothing"),
    h("td", { class: "cell-actions" }, manage && !r.builtIn ? menu(r) : null));
  main.replaceChildren(error.el,
    h("table", { class: "table" }, h("thead", {}, h("tr", {}, h("th", {}, "Role"), h("th", {}, "Permissions"), h("th", { "aria-label": "Actions" }))),
      h("tbody", {}, roles.map(row))));
}

// roleDialog asks for a role's name, what it is for and its permissions, those of role when it changes one, and saves
// them through save.
function roleDialog(role, save) {
  const name = input({ name: "name", required: true, maxlength: 50, value: role ? role.name : "", placeholder: "Release manager" });
  const description = input({ name: "description", maxlength: 500, value: role ? role.description : "" });
  const picks = permissions.map((p) => h("input", { type: "checkbox", value: p, checked: !!role && role.permissions.includes(p) }));
  openDialog({ title: role ? "Edit " + role.name : "Define a role", action: role ? "Save" : "Define",
    body: [field("Name", name), field("Description", description),
      h("div", { class: "field" }, h("span", { class: "field-label" }, "Permissions"), h("span", { class: "field-help" }, "Only what you hold here yourself. Changing a role changes every grant of it."),
        h("div", { class: "picks" }, picks.map((box) => checkbox(box, h("code", {}, box.value)))))],
    submit: () => save({ name: name.value, description: description.value, permissions: picks.filter((c) => c.checked).map((c) => c.value) }) });
  name.focus();
}
