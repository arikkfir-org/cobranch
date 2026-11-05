import { api } from "../../../lib/api.js";
import { openDialog } from "../../../lib/dialog.js";
import { h } from "../../../lib/dom.js";
import { failure, field, input, selectBox } from "../../../lib/forms.js";
import { ago, when } from "../../../lib/format.js";
import { avatar, icon } from "../../../lib/icons.js";
import { popover } from "../../../lib/menu.js";
import { roleSelect } from "../../../lib/organization.js";
import { base, me, roles } from "../../../lib/state.js";

export const tab = { label: "Members", order: 10 };
export const count = ({ org }) => api("GET", "/orgs/" + org.slug + "/members").then((d) => d.members.length);
export const view = "organization.members";
export default ({ org, main, can, actions }) => showMembers(org, main, can, actions);

// joined is the month a member joined, as their row says it.
const joined = (at) => "Joined " + new Date(at).toLocaleDateString(undefined, { month: "short", year: "numeric" });

// showMembers lists the organization's members, and, for a viewer with org.members.manage, the people it invited, who
// show among them until they accept, and the requests to join above them; with org.grants.manage each member's roles
// change from their row.
async function showMembers(org, main, can, actions) {
  const manage = can("org.members.manage");
  const grants = can("org.grants.manage");
  const error = failure();
  const refresh = () => showMembers(org, main, can, actions);
  const act = (call) => call.then(refresh).catch(error.fail);
  const [members, invitations, requests] = await Promise.all([
    api("GET", "/orgs/" + org.slug + "/members").then((d) => d.members),
    manage ? api("GET", "/orgs/" + org.slug + "/invitations").then((d) => d.invitations) : [],
    manage ? api("GET", "/orgs/" + org.slug + "/join-requests").then((d) => d.joinRequests) : [],
  ]);
  actions.replaceChildren(manage ? h("button", { class: "btn btn-primary", type: "button", onclick: () => invite(org, refresh) }, icon("plus"), "Invite people") : "");
  const membership = roles.find((r) => r.id === org.membershipRoleId);

  // rolesButton is a member's roles, a ghost button that opens them as a menu to grant and revoke for a viewer with
  // org.grants.manage, and their names otherwise.
  const rolesButton = (m) => {
    const names = m.roles.map((r) => r.name).join(", ") || "No role";
    if (!grants) return h("span", { class: "row-aside" }, names);
    const button = h("button", { class: "btn btn-ghost btn-sm", type: "button", "aria-haspopup": "menu", title: "The roles " + m.displayName + " holds" }, names, icon("chevron-down"));
    button.addEventListener("click", () => popover(button, "end", (close) => roles.map((r) => {
      const held = m.roles.some((x) => x.id === r.id);
      return h("button", { class: "menu-item", type: "button", role: "menuitemcheckbox", "aria-checked": String(held), title: r.description || null, onclick: () => {
        close();
        act(held ? api("DELETE", "/orgs/" + org.slug + "/members/" + m.userId + "/roles/" + r.id)
          : api("POST", "/orgs/" + org.slug + "/members/" + m.userId + "/roles", { roleId: r.id }));
      } }, h("span", {}, r.name));
    })));
    return button;
  };
  // more is a row's menu of what else the viewer may do with it, or nothing when there is none.
  const more = (label, items) => {
    if (!items.length) return null;
    const button = h("button", { class: "icon-btn icon-btn-ghost icon-btn-sm", type: "button", "aria-haspopup": "menu", "aria-label": label, title: label }, icon("ellipsis"));
    button.addEventListener("click", () => popover(button, "end", (close) => items.map(([word, glyph, run, danger]) =>
      h("button", { class: "menu-item" + (danger ? " menu-item-danger" : ""), type: "button", role: "menuitem", onclick: (e) => { close(); run(e); } }, icon(glyph), h("span", {}, word)))));
    return button;
  };
  const text = (title, meta) => h("div", { class: "row-text" }, h("span", { class: "row-title" }, title), meta ? h("span", { class: "row-meta" }, meta) : null);
  const memberRow = (m) => h("li", { class: "member-row", "data-filter": m.displayName.toLowerCase() },
    avatar(m.displayName, false, 32), text(m.displayName, m.userId === me.id ? "You" : null), h("span", { class: "row-aside" }, joined(m.joinedAt)), rolesButton(m),
    more("More for " + m.displayName, m.userId === me.id
      ? [["Leave " + org.name, "log-out", () => leave(org), true]]
      : manage ? [["Remove from " + org.name, "trash-2", () => remove(org, m, refresh), true]] : []));
  const inviteLink = (inv) => location.origin + base + "invitations#" + inv.id;
  const invitationRow = (inv) => h("li", { class: "member-row", "data-filter": inv.email.toLowerCase() },
    avatar(inv.email, false, 32), text(inv.email, "Invited " + ago(inv.createdAt) + " · until " + when(inv.expiresAt)), h("span", { class: "row-aside" }, "Invited · pending"),
    h("span", { class: "row-aside" }, inv.roleName || "A role since gone"),
    more("More for the invitation of " + inv.email, [
      ["Copy the invitation's link", "link", () => navigator.clipboard.writeText(inviteLink(inv)).catch(error.fail)],
      ["Cancel the invitation", "x", () => act(api("DELETE", "/orgs/" + org.slug + "/invitations/" + inv.id)), true]]));
  const requestBanner = (r) => h("div", { class: "banner banner-info" }, avatar(r.userName, false, 32),
    h("div", { class: "banner-text" }, h("span", { class: "banner-title" }, r.userName + " asked to join " + org.name),
      h("p", {}, [r.note, ago(r.createdAt), "until " + when(r.expiresAt)].filter(Boolean).join(" · "))),
    h("button", { class: "btn btn-ghost btn-sm", type: "button", onclick: () => act(api("POST", "/orgs/" + org.slug + "/join-requests/" + r.id + "/decline")) }, "Decline"),
    h("button", { class: "btn btn-sm", type: "button", onclick: () => act(api("POST", "/orgs/" + org.slug + "/join-requests/" + r.id + "/approve", { roleId: org.membershipRoleId })) },
      "Approve as " + (membership ? membership.name : "a member")));

  const rows = [...members.map(memberRow), ...invitations.map(invitationRow)];
  const counted = h("span", { class: "filters-count" });
  const list = h("ul", { class: "list" }, rows);
  const none = h("p", { class: "filters-count", hidden: true }, "Nobody matches.");
  const filter = h("input", { type: "search", "aria-label": "Filter by name or email", placeholder: "Filter by name or email", oninput: () => {
    const q = filter.value.trim().toLowerCase();
    const shown = rows.filter((r) => { r.hidden = !!q && !r.dataset.filter.includes(q); return !r.hidden; }).length;
    list.hidden = !shown;
    none.hidden = !!shown;
  } });
  counted.textContent = members.length + (members.length === 1 ? " member" : " members") + (invitations.length ? " · " + invitations.length + " invited" : "");
  main.replaceChildren(error.el, ...requests.map(requestBanner),
    h("div", { class: "filters" }, h("div", { class: "input" }, icon("search"), filter), h("span", { class: "filters-space" }), counted),
    list, none);
}

// invite asks for an email and a role, and invites them: the invitee accepts it once they sign in with that email,
// verified, or through the link its row copies.
function invite(org, refresh) {
  const email = input({ name: "email", type: "email", required: true, placeholder: "pat@example.com", autocomplete: "off" });
  const role = roleSelect(org.membershipRoleId);
  openDialog({ title: "Invite people", action: "Invite",
    body: [field("Email", email, "They accept on their welcome page once they sign in with it, or through the invitation's link."), field("Role", selectBox(role))],
    submit: () => api("POST", "/orgs/" + org.slug + "/invitations", { email: email.value, roleId: role.value }).then(refresh) });
  email.focus();
}

// remove asks before it removes a member, and what they held here, from the organization.
function remove(org, m, refresh) {
  openDialog({ title: "Remove " + m.displayName + "?", action: "Remove", danger: true,
    body: h("p", {}, m.displayName + " loses every role they hold in " + org.name + " and its repositories. They can be invited again."),
    submit: () => api("DELETE", "/orgs/" + org.slug + "/members/" + m.userId).then(refresh) });
}

// leave asks before the viewer leaves the organization, then opens the Console's home.
function leave(org) {
  openDialog({ title: "Leave " + org.name + "?", action: "Leave", danger: true,
    body: h("p", {}, "You lose every role you hold in " + org.name + " and its repositories. Someone who manages its members can invite you again."),
    submit: () => api("POST", "/orgs/" + org.slug + "/leave").then(() => location.replace(base)) });
}
