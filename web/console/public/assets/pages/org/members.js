import { api } from "../../lib/api.js";
import { h } from "../../lib/dom.js";
import { when } from "../../lib/format.js";
import { roleSelect } from "../../lib/organization.js";
import { base, me, roles } from "../../lib/state.js";

export const nav = { label: "Members", order: 20 };
export default ({ org, main, can }) => showMembers(org, main, can);

async function showMembers(org, main, can) {
  const manage = can("org.members.manage");
  const grants = can("org.grants.manage");
  const error = h("p", { class: "alert", hidden: true });
  const fail = (err) => { error.textContent = err.message; error.hidden = false; };
  const refresh = () => showMembers(org, main, can);
  // A member's roles: with org.grants.manage, each revocable, and the roles they lack grantable.
  const rolesOf = (m) => {
    const held = m.roles.map((r) => h("span", { class: "chip" }, r.name, grants
      ? h("button", { type: "button", title: "Revoke " + r.name, onclick: () => api("DELETE", "/orgs/" + org.slug + "/members/" + m.userId + "/roles/" + r.id).then(refresh).catch(fail) }, "×") : null));
    const missing = roles.filter((r) => !m.roles.some((held) => held.id === r.id));
    if (!grants || !missing.length) return held;
    const pick = h("select", {}, missing.map((r) => h("option", { value: r.id }, r.name)));
    return [held, h("span", { class: "grant" }, pick, h("button", { class: "button small", type: "button", onclick: () => api("POST", "/orgs/" + org.slug + "/members/" + m.userId + "/roles", { roleId: pick.value }).then(refresh).catch(fail) }, "Grant"))];
  };
  const [members, invitations, requests] = await Promise.all([
    api("GET", "/orgs/" + org.slug + "/members").then((d) => d.members),
    manage ? api("GET", "/orgs/" + org.slug + "/invitations").then((d) => d.invitations) : [],
    manage ? api("GET", "/orgs/" + org.slug + "/join-requests").then((d) => d.joinRequests) : [],
  ]);
  const memberRow = (m) => h("tr", {}, h("td", {}, m.displayName, m.userId === me.id ? h("span", { class: "chip" }, "you") : null),
    h("td", {}, rolesOf(m)), h("td", {}, when(m.joinedAt)),
    h("td", { class: "actions" }, m.userId === me.id
      ? h("button", { class: "button small danger", type: "button", onclick: () => api("POST", "/orgs/" + org.slug + "/leave").then(() => location.replace(base)).catch(fail) }, "Leave")
      : manage ? h("button", { class: "button small danger", type: "button", onclick: () => api("DELETE", "/orgs/" + org.slug + "/members/" + m.userId).then(refresh).catch(fail) }, "Remove") : null));
  const sections = [h("h1", {}, "Members"), error,
    h("section", { class: "panel" }, h("table", {}, h("thead", {}, h("tr", {}, h("th", {}, "Member"), h("th", {}, "Roles"), h("th", {}, "Joined"), h("th", {}))), h("tbody", {}, members.map(memberRow))))];
  if (manage) {
    const email = h("input", { name: "email", type: "email", required: true, placeholder: "pat@example.com" });
    const role = roleSelect(org.membershipRoleId);
    const inviteLink = (inv) => location.origin + base + "invitations#" + inv.id;
    sections.push(h("section", { class: "panel" }, h("h3", {}, "Invitations"),
      h("form", { class: "inline with-role", onsubmit: (e) => { e.preventDefault(); api("POST", "/orgs/" + org.slug + "/invitations", { email: email.value, roleId: role.value }).then(refresh).catch(fail); } },
        h("label", {}, "Email", email), h("label", {}, "Role", role), h("button", { class: "button primary", type: "submit" }, "Invite")),
      invitations.length ? h("table", {}, h("tbody", {}, invitations.map((inv) => h("tr", {}, h("td", {}, inv.email), h("td", {}, h("span", { class: "chip" }, inv.roleName || "?")), h("td", {}, "until " + when(inv.expiresAt)),
        h("td", { class: "actions" }, h("button", { class: "button small", type: "button", onclick: (e) => navigator.clipboard.writeText(inviteLink(inv)).then(() => { e.target.textContent = "Copied"; }).catch(() => {}) }, "Copy link"), " ",
          h("button", { class: "button small danger", type: "button", onclick: () => api("DELETE", "/orgs/" + org.slug + "/invitations/" + inv.id).then(refresh).catch(fail) }, "Cancel")))))) : h("p", {}, "No pending invitations. An invitee signs in with the email you invite, and accepts it on their welcome page.")));
    sections.push(h("section", { class: "panel" }, h("h3", {}, "Join requests"),
      requests.length ? h("table", {}, h("tbody", {}, requests.map((r) => {
        const role = roleSelect(org.membershipRoleId);
        return h("tr", {}, h("td", {}, r.userName, r.note ? h("p", {}, r.note) : null), h("td", {}, role), h("td", {}, "until " + when(r.expiresAt)),
          h("td", { class: "actions" }, h("button", { class: "button small primary", type: "button", onclick: () => api("POST", "/orgs/" + org.slug + "/join-requests/" + r.id + "/approve", { roleId: role.value }).then(refresh).catch(fail) }, "Approve"), " ",
            h("button", { class: "button small", type: "button", onclick: () => api("POST", "/orgs/" + org.slug + "/join-requests/" + r.id + "/decline").then(refresh).catch(fail) }, "Decline")));
      }))) : h("p", {}, org.acceptsJoinRequests ? "Nobody is asking to join." : "This organization doesn't accept join requests; its settings turn them on.")));
  }
  main.replaceChildren(...sections);
}
