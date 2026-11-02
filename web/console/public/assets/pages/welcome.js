import { api } from "../lib/api.js";
import { app, h } from "../lib/dom.js";
import { when } from "../lib/format.js";
import { signOut } from "../lib/layout.js";
import { go } from "../lib/router.js";
import { base, me, orgs, setOrgs } from "../lib/state.js";

// The welcome page: a user who belongs nowhere creates, asks to join, or accepts an invitation. It is also /new.
export async function showWelcome() {
  const [invitations, requests] = await Promise.all([api("GET", "/me/invitations").then((d) => d.invitations), api("GET", "/me/join-requests").then((d) => d.joinRequests)]);
  const error = h("p", { class: "alert", hidden: true });
  const fail = (err) => { error.textContent = err.message; error.hidden = false; };
  const sections = [h("div", { class: "row" }, h("h2", {}, "Welcome, " + me.displayName),
    h("div", {}, h("a", { class: "button small", href: base + "settings", "data-nav": "" }, "Your settings"), " ", h("button", { class: "button small", type: "button", onclick: signOut }, "Sign out"))), error];
  if (orgs.length) sections.push(h("p", {}, "You belong to ", orgs.map((o, i) => [i ? ", " : "", h("a", { href: base + o.slug, "data-nav": "" }, o.name)]), "."));
  if (invitations.length) {
    sections.push(h("section", { class: "panel" }, h("h3", {}, "Invitations"), ...invitations.map((inv) => h("div", { class: "row" },
      h("div", {}, h("strong", {}, inv.orgName), h("p", {}, "as " + (inv.roleName || "a member") + ", to " + inv.email + ", until " + when(inv.expiresAt))),
      h("div", {}, h("button", { class: "button small primary", type: "button", onclick: () => api("POST", "/me/invitations/" + inv.id + "/accept").then(reloadTo).catch(fail) }, "Accept"), " ",
        h("button", { class: "button small", type: "button", onclick: () => api("POST", "/me/invitations/" + inv.id + "/decline").then(() => showWelcome()).catch(fail) }, "Decline"))))));
  }
  if (me.permissions.includes("orgs.create")) sections.push(await createOrganization(fail));
  const joinSlug = h("input", { name: "slug", required: true, placeholder: "acme" });
  const note = h("input", { name: "note", placeholder: "A word for its managers (optional)", maxlength: 500 });
  sections.push(h("section", { class: "panel" }, h("h3", {}, "Ask to join"),
    h("form", { onsubmit: (e) => { e.preventDefault(); api("POST", "/orgs/" + encodeURIComponent(joinSlug.value) + "/join-requests", { note: note.value }).then(() => showWelcome()).catch(fail); } },
      h("label", {}, "Organization slug", joinSlug), h("label", {}, "Note", note), h("div", {}, h("button", { class: "button", type: "submit" }, "Ask to join"))),
    requests.length ? h("p", {}, "Waiting: ", requests.map((r, i) => [i ? ", " : "", h("strong", {}, r.orgName)]), ".") : null));
  document.body.className = "centered";
  app.replaceChildren(h("main", { class: "card" }, h("h1", {}, "Cobranch"), ...sections));
}

// createOrganization is the form that creates an organization, with how many the viewer created against the most a
// user may create; once they reach it, the reason takes the button's place.
async function createOrganization(fail) {
  const created = (await api("GET", "/me/usage")).organizations;
  const count = (n) => n + (n === 1 ? " organization" : " organizations");
  const reached = created.limit > 0 && created.used >= created.limit;
  const slug = h("input", { name: "slug", required: true, placeholder: "acme", pattern: "[a-z0-9]+(-[a-z0-9]+)*", minlength: 2, maxlength: 39, disabled: reached });
  const name = h("input", { name: "name", required: true, placeholder: "Acme", disabled: reached });
  const submit = reached
    ? h("p", { class: "muted" }, "You created " + count(created.used) + ", and a user may create at most " + created.limit + " here. An administrator can create more for you.")
    : h("div", {}, h("button", { class: "button primary", type: "submit" }, "Create"));
  return h("section", { class: "panel" }, h("div", { class: "row" }, h("h3", {}, "Create an organization"),
    created.limit > 0 && !reached ? h("span", { class: "muted" }, "You created " + created.used + " of " + count(created.limit)) : null),
    h("form", { onsubmit: (e) => { e.preventDefault(); if (!reached) api("POST", "/orgs", { slug: slug.value, name: name.value }).then(reloadTo).catch(fail); } },
      h("label", {}, "Name", name), h("label", {}, "Slug", h("span", {}, "Its URL: 2 to 39 lowercase letters, digits and hyphens."), slug), submit));
}

// reloadTo reloads the viewer's organizations and opens org.
async function reloadTo(org) {
  setOrgs((await api("GET", "/me/organizations")).organizations);
  go("/" + org.slug);
}
