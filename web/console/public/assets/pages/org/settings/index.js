import { api } from "../../../lib/api.js";
import { h } from "../../../lib/dom.js";
import { banner, checkbox, failure, field, input, selectBox } from "../../../lib/forms.js";
import { icon } from "../../../lib/icons.js";
import { roleSelect } from "../../../lib/organization.js";
import { go, render } from "../../../lib/router.js";
import { config, setOrgs } from "../../../lib/state.js";

export const tab = { label: "General", order: 0 };
export const view = "organization.settings";
export default ({ org, main, can }) => showGeneral(org, main, can);

// sectionHeader is a section's title over the line on what it holds.
const sectionHeader = (title, description) => h("header", { class: "section-header" },
  h("div", { class: "section-header-text" }, h("h2", { class: "section-header-title" }, title), h("p", { class: "section-header-description" }, description)));

// installationProviders are the identity providers this installation signs people in with, as its Auth server lists
// them: none when sign-in isn't set up or the server doesn't answer.
const installationProviders = () => config.authUrl
  ? fetch(config.authUrl + "/providers", { headers: { Accept: "application/json" } }).then((r) => r.ok ? r.json() : { providers: [] }).then((d) => d.providers || [], () => [])
  : Promise.resolve([]);

// showGeneral is how the organization is named and who may join it, a card each with its own Save, and the identity
// providers its members may sign in with, a row each that turns one on or off. A viewer without org.manage sees them
// as they are, with nothing to change.
async function showGeneral(org, main, can) {
  const manage = can("org.manage");
  const error = failure();
  const saved = banner("success", null, "Saved.");
  saved.hidden = true;
  const providers = await installationProviders();

  // change sends one card's fields, and draws the tab again with what the API answers; a new slug opens its address.
  const change = async (fields, button) => {
    error.el.hidden = saved.hidden = true;
    button.disabled = true;
    try {
      const updated = await api("PATCH", "/orgs/" + org.slug, fields);
      setOrgs((await api("GET", "/me/organizations")).organizations);
      if (updated.slug !== org.slug) return go("/" + updated.slug + "/settings");
      // The whole page draws again, so the sidebar, breadcrumb and heading take a new name too.
      await render();
      document.querySelector("main .banner-success").hidden = false;
    } catch (err) {
      error.fail(err);
      button.disabled = false;
    }
  };
  // card is a form whose fields change together, with Save in its footer for a viewer who may.
  const card = (body, fields) => {
    const save = h("button", { class: "btn btn-primary", type: "submit" }, "Save");
    return h("form", { class: "card", onsubmit: (e) => { e.preventDefault(); change(fields(), save); } },
      h("div", { class: "card-body" }, body), manage ? h("footer", { class: "card-footer" }, save) : null);
  };

  const name = input({ name: "name", required: true, maxlength: 100, value: org.name, disabled: !manage });
  const slug = input({ name: "slug", required: true, value: org.slug, pattern: "[a-z0-9]+(-[a-z0-9]+)*", minlength: 2, maxlength: 39, disabled: !manage });
  const issueKey = input({ name: "issueKey", required: true, value: org.issueKey, pattern: "[A-Z][A-Z0-9]{0,9}", maxlength: 10, disabled: !manage,
    oninput: (e) => { e.target.value = e.target.value.toUpperCase(); } });
  const role = roleSelect(org.membershipRoleId);
  role.disabled = !manage;
  const accepts = h("input", { name: "acceptsJoinRequests", type: "checkbox", checked: org.acceptsJoinRequests, disabled: !manage });

  main.replaceChildren(error.el, saved,
    sectionHeader("Organization", "How the organization appears across Cobranch."),
    card(h("div", { class: "general-fields" },
      field("Name", name),
      field("Slug", slug, "Its address. The old slug keeps pointing here after a change."),
      field("Issue key", issueKey, "Issues are numbered " + org.issueKey + "-1, " + org.issueKey + "-2, …. A change renames every issue; links to the old identifiers still open them.")),
    () => ({ name: name.value, slug: slug.value, issueKey: issueKey.value })),
    sectionHeader("Membership", "Who gets in, and what they can do when they do."),
    card([h("div", { class: "general-fields" }, field("Default role", selectBox(role), "Given to everyone who joins, by invitation or request.")),
      h("div", { class: "field" }, checkbox(accepts, "Anyone signed in may request to join"), h("span", { class: "field-help" }, "Anyone who manages members approves the requests, on the Members tab."))],
    () => ({ membershipRoleId: role.value, acceptsJoinRequests: accepts.checked })),
    ...(providers.length ? [sectionHeader("Identity providers", "How members sign in. At least one stays enabled."), providerRows(org, providers, manage, change)] : []));
}

// providerRows lists the installation's identity providers, each on or off for the organization's members, with what
// turns it the other way: the organization keeps a list of the ones it allows, empty for all of them.
function providerRows(org, providers, manage, change) {
  const allowed = (p) => !org.providers || !org.providers.length || org.providers.includes(p.id);
  const on = providers.filter(allowed).map((p) => p.id);
  const toggle = (p, button) => {
    const next = allowed(p) ? on.filter((id) => id !== p.id) : [...on, p.id];
    change({ providers: next.length === providers.length ? [] : next }, button);
  };
  return h("section", { class: "card" }, providers.map((p) => {
    const enabled = allowed(p);
    const button = h("button", { class: "btn btn-ghost btn-sm", type: "button", disabled: enabled && on.length === 1,
      title: enabled && on.length === 1 ? "At least one provider stays enabled." : null, onclick: () => toggle(p, button) }, enabled ? "Disable" : "Enable");
    return h("div", { class: "card-row" }, icon("globe"),
      h("div", { class: "card-row-text" }, h("span", { class: "card-row-title" }, p.name || p.id),
        h("span", { class: "card-row-meta" }, enabled ? "Members may sign in with it" : "Members can't sign in with it")),
      h("span", { class: "tag " + (enabled ? "tone-success" : "tone-neutral") }, enabled ? "Enabled" : "Off"),
      manage ? h("div", { class: "card-row-actions" }, button) : null);
  }));
}
