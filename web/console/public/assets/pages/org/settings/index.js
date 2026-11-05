import { api } from "../../../lib/api.js";
import { h } from "../../../lib/dom.js";
import { banner, checkbox, failure, field, input, selectBox } from "../../../lib/forms.js";
import { roleSelect } from "../../../lib/organization.js";
import { go } from "../../../lib/router.js";
import { roles, setOrgs } from "../../../lib/state.js";

export const tab = { label: "General", order: 0 };
export const view = "organization.settings";
export default ({ org, main, can }) => showGeneral(org, main, can);

// showGeneral is how the organization is named and who may join it: a form that changes them for a viewer with
// org.manage, and what they are for anyone else who reads the organization.
function showGeneral(org, main, can) {
  const card = (body) => h("section", { class: "card" }, h("header", { class: "card-header" }, h("h2", { class: "card-title" }, "Organization")), body);
  if (!can("org.manage")) {
    const membership = roles.find((r) => r.id === org.membershipRoleId);
    return main.replaceChildren(card(h("dl", { class: "card-body facts" },
      h("dt", {}, "Name"), h("dd", {}, org.name), h("dt", {}, "Slug"), h("dd", {}, h("code", {}, org.slug)),
      h("dt", {}, "Issue key"), h("dd", {}, h("code", {}, org.issueKey)), h("dt", {}, "Membership role"), h("dd", {}, membership ? membership.name : "None"),
      h("dt", {}, "Join requests"), h("dd", {}, org.acceptsJoinRequests ? "Accepted, by its slug" : "Not accepted"),
      h("dt", {}, "Identity providers"), h("dd", {}, org.providers && org.providers.length ? org.providers.join(", ") : "All of this installation's"))));
  }
  const error = failure();
  const saved = banner("success", null, "Saved.");
  saved.hidden = true;
  const name = input({ name: "name", required: true, maxlength: 100, value: org.name });
  const slug = input({ name: "slug", required: true, value: org.slug, pattern: "[a-z0-9]+(-[a-z0-9]+)*", minlength: 2, maxlength: 39 });
  const role = roleSelect(org.membershipRoleId);
  const accepts = h("input", { name: "acceptsJoinRequests", type: "checkbox", checked: org.acceptsJoinRequests });
  const providers = input({ name: "providers", value: (org.providers || []).join(", "), placeholder: "All of this installation's" });
  const issueKey = input({ name: "issueKey", required: true, value: org.issueKey, pattern: "[A-Z][A-Z0-9]{0,9}", maxlength: 10,
    oninput: (e) => { e.target.value = e.target.value.toUpperCase(); } });
  const save = h("button", { class: "btn btn-primary", type: "submit" }, "Save");
  const form = h("form", { class: "card-body", onsubmit: async (e) => {
    e.preventDefault();
    error.el.hidden = saved.hidden = true;
    save.disabled = true;
    try {
      const updated = await api("PATCH", "/orgs/" + org.slug, { name: name.value, slug: slug.value, membershipRoleId: role.value, acceptsJoinRequests: accepts.checked,
        providers: providers.value.split(",").map((s) => s.trim()).filter(Boolean), issueKey: issueKey.value });
      setOrgs((await api("GET", "/me/organizations")).organizations);
      if (updated.slug !== org.slug) return go("/" + updated.slug + "/settings");
      org = updated;
      saved.hidden = false;
    } catch (err) {
      error.fail(err);
    } finally {
      save.disabled = false;
    }
  } },
  field("Name", name),
  field("Slug", slug, "Its URL. The old slug keeps pointing here after a change."),
  field("Issue key", issueKey, "What its issues' identifiers start with, such as " + org.issueKey + "-42: 1 to 10 uppercase letters and digits, from a letter. " +
    "A change renames every issue; links to the old identifiers still open them."),
  field("Membership role", selectBox(role), "The role everyone who joins gets."),
  checkbox(accepts, "Accept join requests by slug"),
  field("Identity providers", providers, "The providers members may sign in with, by ID, comma-separated; empty for all of this installation's."),
  h("div", {}, save));
  main.replaceChildren(error.el, saved, card(form));
}
