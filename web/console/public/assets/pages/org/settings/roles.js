import { api } from "../../../lib/api.js";
import { openDialog } from "../../../lib/dialog.js";
import { h } from "../../../lib/dom.js";
import { checkbox, failure, field, input } from "../../../lib/forms.js";
import { plural } from "../../../lib/format.js";
import { icon } from "../../../lib/icons.js";
import { setRoles } from "../../../lib/state.js";

export const tab = { label: "Roles", order: 20 };
export const view = "organization.roles";
export default ({ org, main, can, actions }) => showRoles(org, main, can, actions);

// sectionHeader is a section's title over the line on what it holds, with its action on the right.
const sectionHeader = (title, description, action) => h("header", { class: "section-header" },
  h("div", { class: "section-header-text" }, h("h2", { class: "section-header-title" }, title), h("p", { class: "section-header-description" }, description)), action || null);

// held is how many hold a role at the organization, as its row says it.
const held = (r) => r.holders ? plural(r.holders, "holder") : "Nobody yet";

// byWeight orders the built-in roles first, those that hold most first, then the organization's own by name.
const byWeight = (a, b) => b.builtIn - a.builtIn || (a.builtIn ? b.permissions.length - a.permissions.length : 0) || a.name.localeCompare(b.name);

// showRoles lists what each role holds, built in or the organization's own, and the permissions of the one picked, by
// area, as ticks; a viewer with org.grants.manage defines, renames, changes and deletes the organization's own. open is
// the ID of the role picked.
async function showRoles(org, main, can, actions, open = null) {
  const manage = can("org.grants.manage");
  const error = failure();
  const [roles, labels] = await Promise.all([
    api("GET", "/orgs/" + org.slug + "/roles").then((d) => d.roles),
    api("GET", "/permissions?scope=organization").then((d) => d.labels),
  ]);
  setRoles(roles);
  const redraw = (pick) => showRoles(org, main, can, actions, pick === undefined ? open : pick);
  const editable = (r) => manage && !r.builtIn;
  actions.replaceChildren(manage ? h("button", { class: "btn btn-primary", type: "button",
    onclick: () => defineRole(org, labels, (made) => redraw(made.id)) }, icon("plus"), "New role") : "");

  const row = (r) => {
    const mayDelete = !r.holders && r.id !== org.membershipRoleId;
    const reason = r.id === org.membershipRoleId ? "Everyone who joins gets it: pick another membership role first."
      : r.holders ? "Revoke it from everyone who holds it first." : "Delete " + r.name;
    const ghost = (label, onclick, glyph, attrs = {}) => h("button", { class: "btn btn-ghost btn-sm", type: "button", onclick, ...attrs }, glyph ? icon(glyph) : null, label);
    return h("div", { class: "card-row", "aria-selected": String(r.id === open) },
      icon("users"),
      h("div", { class: "card-row-text" },
        h("span", { class: "card-row-title" }, r.name, h("span", { class: "tag" + (r.builtIn ? "" : " tone-info") }, r.builtIn ? "Built-in" : "Custom")),
        h("span", { class: "card-row-meta" }, (r.permissions.length === labels.length ? "Everything" : plural(r.permissions.length, "permission")) + " · " + held(r))),
      h("div", { class: "card-row-actions" }, editable(r)
        ? [ghost("Edit", () => redraw(r.id), "pencil"),
          ghost("Delete", () => openDialog({ title: "Delete " + r.name + "?", action: "Delete", danger: true,
            body: h("p", {}, "Nobody holds it, so nothing changes for anyone. Invitations that offer it are cancelled."),
            submit: () => api("DELETE", "/orgs/" + org.slug + "/roles/" + r.id).then(() => redraw(open === r.id ? null : open)) }),
          null, { disabled: !mayDelete, title: reason })]
        : ghost("View", () => redraw(r.id))));
  };
  const picked = roles.find((r) => r.id === open);
  main.replaceChildren(error.el,
    sectionHeader("Roles", "A role is a set of permissions. Built-in roles cannot change; a custom role can be edited, and deleted while nobody holds it."),
    h("section", { class: "card" }, [...roles].sort(byWeight).map(row)),
    ...(picked ? editor(org, picked, labels, editable(picked), error, redraw) : []));
}

// editor shows a role's permissions by area, as ticks, which change it when it is the organization's own and the viewer
// may: Save sends them, Discard draws them again as they were.
function editor(org, r, labels, editable, error, redraw) {
  const groups = Map.groupBy(labels, (p) => p.group);
  const boxes = labels.map((p) => h("input", { type: "checkbox", value: p.id, checked: r.permissions.includes(p.id), disabled: !editable, "aria-label": p.label }));
  const box = (id) => boxes.find((b) => b.value === id);
  const save = h("button", { class: "btn btn-primary", type: "submit" }, "Save");
  const form = h("form", { class: "card", onsubmit: async (e) => {
    e.preventDefault();
    error.el.hidden = true;
    save.disabled = true;
    try {
      await api("PATCH", "/orgs/" + org.slug + "/roles/" + r.id, { permissions: boxes.filter((b) => b.checked).map((b) => b.value) });
      await redraw();
    } catch (err) {
      error.fail(err);
      save.disabled = false;
    }
  } },
  [...groups].map(([group, perms]) => h("div", { class: "card-row" }, h("span", { class: "card-row-label" }, group),
    h("div", { class: "card-row-picks" }, perms.map((p) => checkbox(box(p.id), p.label))))),
  editable ? h("footer", { class: "card-footer" }, h("button", { class: "btn", type: "button", onclick: () => redraw() }, "Discard"), save) : null);
  const line = r.builtIn ? "Built-in role · " + held(r) + ". It cannot change." : "Custom role · " + held(r) + "." + (editable ? " Tick what it may do: only what you hold here yourself." : "");
  return [
    h("header", { class: "section-header" },
      h("div", { class: "section-header-text" }, h("h2", { class: "section-header-title" }, r.name), h("p", { class: "section-header-description" }, [r.description, line].filter(Boolean).join(" "))),
      editable ? h("button", { class: "btn", type: "button", onclick: () => renameRole(org, r, () => redraw()) }, icon("pencil"), "Rename") : null),
    form];
}

// defineRole asks for a new role's name, what it is for and its permissions, by area, and defines it.
function defineRole(org, labels, done) {
  const name = input({ name: "name", required: true, maxlength: 50, placeholder: "Release manager" });
  const description = input({ name: "description", maxlength: 500 });
  const boxes = labels.map((p) => h("input", { type: "checkbox", value: p.id, "aria-label": p.label }));
  const groups = Map.groupBy(labels.map((p, i) => ({ ...p, box: boxes[i] })), (p) => p.group);
  openDialog({ title: "New role", action: "Define",
    body: [field("Name", name), field("Description", description),
      h("div", { class: "field" }, h("span", { class: "field-label" }, "Permissions"), h("span", { class: "field-help" }, "Only what you hold here yourself. Changing a role later changes every grant of it."),
        [...groups].map(([group, perms]) => h("div", { class: "field" }, h("span", { class: "field-help" }, group), perms.map((p) => checkbox(p.box, p.label)))))],
    submit: () => api("POST", "/orgs/" + org.slug + "/roles", { name: name.value, description: description.value, permissions: boxes.filter((b) => b.checked).map((b) => b.value) }).then(done) });
  name.focus();
}

// renameRole asks for a role's new name and what it is for, and saves them.
function renameRole(org, r, done) {
  const name = input({ name: "name", required: true, maxlength: 50, value: r.name });
  const description = input({ name: "description", maxlength: 500, value: r.description });
  openDialog({ title: "Rename " + r.name, action: "Save", body: [field("Name", name), field("Description", description)],
    submit: () => api("PATCH", "/orgs/" + org.slug + "/roles/" + r.id, { name: name.value, description: description.value }).then(done) });
  name.focus();
}
